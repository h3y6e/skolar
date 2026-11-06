package skill

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/lock"
)

// Status はインストール済みスキルまたはインストールされていない上流スキルの
// 更新状態を表す
type Status string

// update が報告し、処理の対象とする状態
const (
	StatusUpToDate        Status = "up-to-date"
	StatusUpdateAvailable Status = "update-available"
	StatusMoved           Status = "moved"
	StatusRemoved         Status = "removed"
	StatusNew             Status = "new"
	StatusCheckFailed     Status = "check-failed"
	StatusPinned          Status = "pinned"
	StatusLocal           Status = "local"
)

// UpdateCandidate はインストール済みスキルまたはまだインストールされていない
// 上流スキル (StatusNew) の更新状態を表す
type UpdateCandidate struct {
	SkillName   string
	Source      string
	Ref         string
	CurrentHash string
	LatestHash  string
	Status      Status
	// Upstream は update がインストールする内容をメタデータ込みで保持する
	Upstream fs.FS
	// Path はスキルが解決された上流のディレクトリで、リポジトリからの相対パスである
	Path string
	// PreviousPath はインストール時に記録されたディレクトリで、上流で移動または
	// 削除されたスキルに使う
	PreviousPath string
	// Reinstall は StatusNew のスキルをインストールする add の引数である
	Reinstall string
	Reason    string
	// Foreign は gh 互換ツールがインストールし、ロックファイルにエントリがない
	// スキルを示す。適用するとディレクトリだけを置き換え、ロックファイルは変更しない
	Foreign bool
}

// Applicable は update がインストール済みのスキルを置き換えるかを返す
func (c UpdateCandidate) Applicable() bool {
	return c.Status == StatusUpdateAvailable || c.Status == StatusMoved
}

// CloneFunc はソースの ref 時点のファイルを返す
type CloneFunc func(source string) (fs.FS, error)

// SkippedEntry は未対応のソース種別のためスキップしたロックファイルのエントリである
type SkippedEntry struct {
	SkillName  string
	SourceType lock.SourceType
}

var errRemoved = errors.New("上流で削除されました")

// ResolveUpdateCandidates は 1 つのソースのロックファイルのエントリを上流のスキルと比較し、
// 名前順の更新候補を返す。続けてインストールされていない上流スキルごとに
// StatusNew の候補を返す。エントリは skillPath のスキルに解決され、そのパスが
// なくなった場合や未記録の場合は同名の上流スキルが 1 つだけならそれに解決される
func ResolveUpdateCandidates(entries map[string]lock.Entry, upstream []DiscoveredSkill) []UpdateCandidate {
	candidates := make([]UpdateCandidate, 0, len(entries))
	reinstall := ""

	for name, entry := range entries {
		reinstall = SourceInput(entry)
		candidate := UpdateCandidate{
			SkillName:   name,
			Source:      entry.Source,
			Ref:         entry.Ref,
			CurrentHash: entry.ComputedHash,
		}

		recorded := recordedDir(entry)

		up, err := resolveUpstream(name, recorded, upstream)
		switch {
		case errors.Is(err, errRemoved):
			candidate.Status, candidate.PreviousPath = StatusRemoved, recorded
		case err != nil:
			candidate.Status, candidate.Reason = StatusCheckFailed, err.Error()
		default:
			candidate.Path, candidate.Upstream = up.Path, up.FS
			if candidate.LatestHash, err = up.Hash(); err != nil {
				candidate.Status, candidate.Reason = StatusCheckFailed, err.Error()
				break
			}

			candidate.Status, candidate.PreviousPath = changeStatus(recorded, up.Path, entry.ComputedHash != candidate.LatestHash)
		}

		candidates = append(candidates, candidate)
	}

	sortCandidates(candidates)

	return append(candidates, newCandidates(candidates, upstream, reinstall)...)
}

func recordedDir(entry lock.Entry) string {
	if entry.SkillPath == "" {
		return ""
	}

	return path.Dir(entry.SkillPath)
}

func changeStatus(recorded, current string, changed bool) (Status, string) {
	switch {
	case recorded != "" && recorded != current:
		return StatusMoved, recorded
	case changed:
		return StatusUpdateAvailable, ""
	default:
		return StatusUpToDate, ""
	}
}

func newCandidates(installed []UpdateCandidate, upstream []DiscoveredSkill, reinstall string) []UpdateCandidate {
	names, paths := map[string]bool{}, map[string]bool{}
	for _, c := range installed {
		names[c.SkillName], paths[c.Path] = true, true
	}

	var news []UpdateCandidate

	for _, s := range upstream {
		if !s.Default || names[s.Name] || paths[s.Path] {
			continue
		}

		names[s.Name] = true
		news = append(news, UpdateCandidate{
			SkillName: s.Name,
			Source:    installed[0].Source,
			Ref:       installed[0].Ref,
			Status:    StatusNew,
			Path:      s.Path,
			Reinstall: reinstall,
			Foreign:   installed[0].Foreign,
		})
	}

	return news
}

func resolveUpstream(name, recorded string, upstream []DiscoveredSkill) (DiscoveredSkill, error) {
	if i := slices.IndexFunc(upstream, func(s DiscoveredSkill) bool { return s.Path == recorded }); recorded != "" && i >= 0 {
		return upstream[i], nil
	}

	var named []DiscoveredSkill

	for _, s := range upstream {
		if s.Name == name {
			named = append(named, s)
		}
	}

	switch len(named) {
	case 0:
		return DiscoveredSkill{}, errRemoved
	case 1:
		return named[0], nil
	}

	paths := make([]string, len(named))
	for i, s := range named {
		paths[i] = s.Path
	}

	return DiscoveredSkill{}, fmt.Errorf("上流に候補が複数あります: %s", strings.Join(paths, ", "))
}

// FilterCandidates は filter が選ぶ名前の候補だけを返す。filter が空なら全件を返す
func FilterCandidates(candidates []UpdateCandidate, filter []string) []UpdateCandidate {
	if len(filter) == 0 {
		return candidates
	}

	var result []UpdateCandidate

	for _, c := range candidates {
		if Selected(filter, c.SkillName) {
			result = append(result, c)
		}
	}

	return result
}

// FilterDiscoveredSkills は filter が選ぶスキルを返す。filter が空なら既定のスキルを返す。
// 同名のスキルが複数あれば最初のものを採る
func FilterDiscoveredSkills(skills []DiscoveredSkill, filter []string) []DiscoveredSkill {
	var result []DiscoveredSkill
	for _, s := range skills {
		if slices.ContainsFunc(result, func(r DiscoveredSkill) bool { return r.Name == s.Name }) {
			continue
		}

		if len(filter) == 0 && s.Default || Selected(filter, s.Name) {
			result = append(result, s)
		}
	}

	return result
}

// CollectUpdateCandidates はロックファイルのエントリと gh skill のインストールについて
// 更新候補を解決し、全ソースを並列に取得する。取得に失敗したソースはそのソースの
// スキルだけを失敗させる。done は各ソースの取得後に取得済みの数と総数を渡して呼ばれる。
// 未対応のソース種別のエントリはスキップする
func CollectUpdateCandidates(
	entries map[string]lock.Entry, installed []InstalledSkill, cloneFn CloneFunc, fns UpstreamFuncs, done func(n, total int),
) ([]UpdateCandidate, []SkippedEntry) {
	supported, skipped := partitionSupportedEntries(entries)
	bySource := groupEntriesBySource(supported)
	candidates, byRepo := partitionInstalled(installed)

	var (
		mu sync.Mutex
		n  int
		wg sync.WaitGroup
	)

	finish := func(cs []UpdateCandidate) {
		mu.Lock()
		defer mu.Unlock()

		candidates = append(candidates, cs...)
		n++
		done(n, len(bySource)+len(byRepo))
	}

	for source, group := range bySource {
		wg.Go(func() { finish(resolveSourceCandidates(source, group, cloneFn)) })
	}

	for _, group := range byRepo {
		wg.Go(func() { finish(resolveRepoCandidates(group, fns)) })
	}

	wg.Wait()
	sortCandidates(candidates)

	return dropInstalledNews(candidates), skipped
}

func dropInstalledNews(candidates []UpdateCandidate) []UpdateCandidate {
	installed := map[string]bool{}

	for _, c := range candidates {
		if c.Status != StatusNew {
			installed[c.SkillName] = true
		}
	}

	listed := map[[2]string]bool{}

	return slices.DeleteFunc(candidates, func(c UpdateCandidate) bool {
		key := [2]string{c.Source, c.SkillName}
		drop := c.Status == StatusNew && (installed[c.SkillName] || listed[key])
		listed[key] = listed[key] || c.Status == StatusNew

		return drop
	})
}

func resolveSourceCandidates(source string, group map[string]lock.Entry, cloneFn CloneFunc) []UpdateCandidate {
	repo, upstream, err := cloneAndDiscover(source, cloneFn)
	if err == nil {
		candidates := ResolveUpdateCandidates(group, upstream)
		for i, c := range candidates {
			if c.Status == StatusRemoved && c.PreviousPath != "" && hasSkillMD(repo, c.PreviousPath) {
				candidates[i].Status = StatusCheckFailed
				candidates[i].Reason = fmt.Sprintf("上流の %s/SKILL.md は有効なスキルではありません", c.PreviousPath)
			}
		}

		return candidates
	}

	candidates := make([]UpdateCandidate, 0, len(group))
	for name, entry := range group {
		candidates = append(candidates, UpdateCandidate{
			SkillName:   name,
			Source:      entry.Source,
			Ref:         entry.Ref,
			CurrentHash: entry.ComputedHash,
			Status:      StatusCheckFailed,
			Reason:      err.Error(),
		})
	}

	return candidates
}

func cloneAndDiscover(source string, cloneFn CloneFunc) (fs.FS, []DiscoveredSkill, error) {
	repo, err := cloneFn(source)
	if err != nil {
		return nil, nil, fmt.Errorf("%s の取得: %w", source, err)
	}

	upstream, err := DiscoverSkills(repo)
	if err != nil {
		return nil, nil, fmt.Errorf("%s のスキルの探索: %w", source, err)
	}

	return repo, upstream, nil
}

func hasSkillMD(repo fs.FS, dir string) bool {
	_, err := fs.Stat(repo, path.Join(dir, "SKILL.md"))

	return err == nil
}

// NewCloneFunc は vercel-labs/skills と同じように c を通してソースをチェックアウトする
// CloneFunc を返す
func NewCloneFunc(ctx context.Context, c *git.Cache) CloneFunc {
	return func(source string) (fs.FS, error) {
		ref, err := ParseSource(source)
		if err != nil {
			return nil, err
		}

		resolved, err := git.ResolveDefault(ctx, ref.CloneURL, ref.Ref)
		if err != nil {
			return nil, err
		}

		return c.Checkout(ctx, ref.CloneURL, resolved)
	}
}

func sortCandidates(c []UpdateCandidate) {
	slices.SortFunc(c, func(a, b UpdateCandidate) int {
		return cmp.Or(cmp.Compare(a.SkillName, b.SkillName), cmp.Compare(a.Source, b.Source))
	})
}

func partitionSupportedEntries(entries map[string]lock.Entry) (map[string]lock.Entry, []SkippedEntry) {
	supported := make(map[string]lock.Entry)

	var skipped []SkippedEntry

	for name, entry := range entries {
		if entry.SourceType.Supported() {
			supported[name] = entry
			continue
		}

		skipped = append(skipped, SkippedEntry{SkillName: name, SourceType: entry.SourceType})
	}

	slices.SortFunc(skipped, func(a, b SkippedEntry) int {
		return cmp.Compare(a.SkillName, b.SkillName)
	})

	return supported, skipped
}

func groupEntriesBySource(entries map[string]lock.Entry) map[string]map[string]lock.Entry {
	bySource := make(map[string]map[string]lock.Entry)

	for name, entry := range entries {
		sourceKey := SourceInput(entry)

		group, ok := bySource[sourceKey]
		if !ok {
			group = make(map[string]lock.Entry)
			bySource[sourceKey] = group
		}

		group[name] = entry
	}

	return bySource
}
