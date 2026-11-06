package skill

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/h3y6e/skolar/internal/lock"
)

// VerifyStatus はインストール済みスキルの検証結果を表す
type VerifyStatus string

// verify が報告する状態
const (
	VerifyVerified     VerifyStatus = "verified"
	VerifyModified     VerifyStatus = "modified"
	VerifyUnverifiable VerifyStatus = "unverifiable"
	VerifyLocal        VerifyStatus = "local"
)

// VerifyResult はインストール済みスキルを記録と比較した結果である。Files は記録から
// ファイル単位の比較ができる場合に差異のあるパスを列挙する
type VerifyResult struct {
	SkillName string       `json:"skillName"`
	Status    VerifyStatus `json:"status"`
	Source    string       `json:"source"`
	Files     []string     `json:"files,omitempty"`
	Reason    string       `json:"reason,omitempty"`
	// Reinstall は記録どおりにスキルを復元する add の引数である
	Reinstall string `json:"-"`
}

// VerifyLocked はロックファイルの各スキルの computedHash をディスクから再計算する。
// オフラインで動く。ハッシュが異なる場合はソースを clone し、上流のスキルが記録と同じハッシュを
// 保っていれば、vercel-labs/skills がスキルのインストール時に除くものを除いたうえで、
// インストール結果と上流をファイル単位で比較する
func VerifyLocked(layout lock.Layout, entries map[string]lock.Entry, cloneFn CloneFunc) []VerifyResult {
	var sources onceMap[string, []DiscoveredSkill]

	return parallelMap(slices.Sorted(maps.Keys(entries)), func(name string) VerifyResult {
		entry := entries[name]
		result := VerifyResult{SkillName: name, Source: entry.Source, Status: VerifyVerified, Reinstall: SourceInput(entry)}

		hash, err := ComputeHash(os.DirFS(layout.SkillDir(name)))
		switch {
		case err != nil:
			result.Status, result.Reason = VerifyUnverifiable, err.Error()
		case hash != entry.ComputedHash:
			upstream, err := sources.do(SourceInput(entry), func() ([]DiscoveredSkill, error) {
				_, upstream, err := cloneAndDiscover(SourceInput(entry), cloneFn)
				return upstream, err
			})
			if err != nil {
				result.Status, result.Reason = VerifyUnverifiable, err.Error()
				break
			}

			result.Status, result.Files, result.Reason = verifyLockedOnline(name, entry, layout.SkillDir(name), upstream)
		}

		return result
	})
}

func verifyLockedOnline(name string, entry lock.Entry, installed string, upstream []DiscoveredSkill) (VerifyStatus, []string, string) {
	up, err := resolveUpstream(name, recordedDir(entry), upstream)
	if err != nil {
		return VerifyUnverifiable, nil, err.Error()
	}

	hash, err := up.Hash()
	if err != nil {
		return VerifyUnverifiable, nil, err.Error()
	}

	if hash != entry.ComputedHash {
		return VerifyUnverifiable, nil, "インストール後に上流が変更されました"
	}

	files, err := diffDirs(up.FS, os.DirFS(installed), vercelStyle)
	if err != nil {
		return VerifyUnverifiable, nil, err.Error()
	}

	if len(files) > 0 {
		return VerifyModified, files, ""
	}

	return VerifyVerified, nil, ""
}

// VerifyTracked は gh 形式の各スキルをそのメタデータが記録する上流のツリーと比較する。
// ツリーはキャッシュにない限り github-ref で取得する
func VerifyTracked(installed []InstalledSkill, fns UpstreamFuncs) []VerifyResult {
	return parallelMap(installed, func(s InstalledSkill) VerifyResult {
		source, _ := OwnerRepoFromURL(s.Meta.RepoURL)

		result := VerifyResult{SkillName: s.Name, Source: source, Reinstall: FormatSourceInput(source, s.Meta.Pinned)}
		switch {
		case s.Meta.IsLocal():
			result.Status, result.Reason = VerifyLocal, s.Meta.LocalPath
		case s.Meta.Path == "":
			result.Status, result.Reason = VerifyUnverifiable, "スキルのメタデータに github-path がありません"
		default:
			result.Status, result.Files, result.Reason = verifyTracked(s, fns)
		}

		return result
	})
}

func verifyTracked(s InstalledSkill, fns UpstreamFuncs) (VerifyStatus, []string, string) {
	tree, err := fns.FetchTree(s.Meta.RepoURL, s.Meta.Ref, s.Meta.TreeSHA)
	if err != nil {
		return VerifyUnverifiable, nil, err.Error()
	}

	want, err := withMetadata(tree, s.Meta)
	if err != nil {
		return VerifyUnverifiable, nil, err.Error()
	}

	files, err := diffDirs(want, os.DirFS(s.Dir), ghStyle)
	if err != nil {
		return VerifyUnverifiable, nil, err.Error()
	}

	if len(files) > 0 {
		return VerifyModified, files, ""
	}

	return VerifyVerified, nil, ""
}

// VerifyUnrecorded は skillsDir のスキルのディレクトリのうちロックファイルにも
// 読み取れる gh skill のメタデータにも記録されていないものをすべて報告する
func VerifyUnrecorded(skillsDir string, recorded func(name string) bool) ([]VerifyResult, error) {
	entries, err := os.ReadDir(skillsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("スキルのディレクトリ %q の読み取り: %w", skillsDir, err)
	}

	var results []VerifyResult

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || recorded(e.Name()) {
			continue
		}

		reason := "ロックファイルのエントリも gh skill のメタデータもありません"
		if data, err := os.ReadFile(filepath.Join(skillsDir, e.Name(), "SKILL.md")); err != nil {
			reason = err.Error()
		} else if _, err := ParseSourceMetadata(string(data)); err != nil {
			reason = err.Error()
		}

		results = append(results, VerifyResult{SkillName: e.Name(), Status: VerifyUnverifiable, Reason: reason})
	}

	return results, nil
}

func diffDirs(want, got fs.FS, s style) ([]string, error) {
	wantFiles, gotFiles, err := readBoth(want, got, s)
	if err != nil {
		return nil, err
	}

	return changedFiles(wantFiles, gotFiles), nil
}

func readBoth(want, got fs.FS, s style) (map[string][]byte, map[string][]byte, error) {
	wantFiles, err := readFiles(want, s)
	if err != nil {
		return nil, nil, err
	}

	gotFiles, err := readFiles(got, installed)

	return wantFiles, gotFiles, err
}

func changedFiles(want, got map[string][]byte) []string {
	var diff []string

	for path, data := range want {
		if other, ok := got[path]; !ok || !bytes.Equal(data, other) {
			diff = append(diff, path)
		}
	}

	for path := range got {
		if _, ok := want[path]; !ok {
			diff = append(diff, path)
		}
	}

	slices.Sort(diff)

	return diff
}
