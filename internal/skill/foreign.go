package skill

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/lock"
)

// InstalledSkill はインストール先のディレクトリにあるスキルで、その SKILL.md の
// フロントマターに gh 互換のソースのメタデータがあるものである
type InstalledSkill struct {
	Name string
	Dir  string
	Meta SourceMetadata
}

// ScanInstalledSkills は skillsDir のスキルのうち gh 互換のソースのメタデータを持つものを
// 名前順に返す。メタデータのないスキル、SKILL.md を読めないスキル、
// フロントマターが不正なスキルはスキップする
func ScanInstalledSkills(skillsDir string) ([]InstalledSkill, error) {
	entries, err := os.ReadDir(skillsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("スキルのディレクトリ %q の読み取り: %w", skillsDir, err)
	}

	var installed []InstalledSkill

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(skillsDir, entry.Name())

		data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			continue
		}

		meta, err := ParseSourceMetadata(string(data))
		if err != nil || (!meta.IsGitHub() && !meta.IsLocal()) {
			continue
		}

		installed = append(installed, InstalledSkill{
			Name: entry.Name(),
			Dir:  dir,
			Meta: meta,
		})
	}

	slices.SortFunc(installed, func(a, b InstalledSkill) int {
		return cmp.Compare(a.Name, b.Name)
	})

	return installed, nil
}

// PreferTrackedInstall は add が gh skill 形式 (フロントマターのメタデータでロックファイルなし) で
// インストールすべきかを返す。まだロックファイルがなくソースが GitHub リポジトリの場合に
// true になる。ほかのソース種別はロックファイル形式のままにする。ロックファイルなら
// gh skill のメタデータと違って更新を追跡できるからである
func PreferTrackedInstall(lockfileExists bool, sourceType lock.SourceType) bool {
	return !lockfileExists && sourceType == lock.SourceGitHub
}

// UnmanagedSkills は名前がロックファイルの管理下のエントリにないインストール済みスキルを返す
func UnmanagedSkills(installed []InstalledSkill, managed map[string]lock.Entry) []InstalledSkill {
	var foreign []InstalledSkill

	for _, s := range installed {
		if _, ok := managed[s.Name]; !ok {
			foreign = append(foreign, s)
		}
	}

	return foreign
}

// FilterInstalledBySource はインストール済みスキルを指定したソースリポジトリから
// インストールされたものに絞る。ソースに ref があればその ref に固定されたものに絞る。
// ソースが空なら全件を残す。ローカルのパスからインストールされたスキルは
// リポジトリのソースには一致しない
func FilterInstalledBySource(installed []InstalledSkill, source string) ([]InstalledSkill, error) {
	if source == "" {
		return installed, nil
	}

	ref, err := ParseSource(source)
	if err != nil {
		return nil, err
	}

	var filtered []InstalledSkill

	for _, s := range installed {
		if !s.Meta.IsGitHub() {
			continue
		}

		ownerRepo, err := OwnerRepoFromURL(s.Meta.RepoURL)
		if err != nil || ownerRepo != ref.CanonicalSource || s.Meta.Pinned != ref.Ref {
			continue
		}

		filtered = append(filtered, s)
	}

	return filtered, nil
}

// UpstreamFuncs は上流のリポジトリの調査に使う git 操作をまとめる。
// テストでは差し替えられる
type UpstreamFuncs struct {
	// FetchLatest は repoURL の最新の ref を取得する (gh skill の意味論では
	// 最新のリリース、なければデフォルトブランチ)
	FetchLatest func(repoURL string) (git.Snapshot, error)
	// FetchTree は repoURL のツリー treeSHA を読む。ツリーがキャッシュになければ
	// ref を取得する
	FetchTree func(repoURL, ref, treeSHA string) (fs.FS, error)
}

// NewUpstreamFuncs は c を通して取得する本番用の UpstreamFuncs を返す
func NewUpstreamFuncs(ctx context.Context, c *git.Cache) UpstreamFuncs {
	var prefetched onceMap[[2]string, struct{}]

	return UpstreamFuncs{
		FetchLatest: func(repoURL string) (git.Snapshot, error) {
			resolved, err := git.ResolveLatest(ctx, repoURL, "")
			if err != nil {
				return git.Snapshot{}, err
			}

			return c.Fetch(ctx, repoURL, resolved)
		},
		FetchTree: func(repoURL, ref, treeSHA string) (fs.FS, error) {
			tree, err := c.ReadTree(ctx, repoURL, treeSHA)
			if !errors.Is(err, git.ErrNotCached) {
				return tree, err
			}

			if err := prefetched.doErr([2]string{repoURL, ref}, func() error {
				resolved, err := git.ResolveDefault(ctx, repoURL, ref)
				if err != nil {
					return err
				}

				return c.Prefetch(ctx, repoURL, resolved)
			}); err != nil {
				return nil, fmt.Errorf("%s の取得: %w", ref, err)
			}

			tree, err = c.ReadTree(ctx, repoURL, treeSHA)
			if errors.Is(err, git.ErrNotCached) {
				return nil, fmt.Errorf("%s にツリー %s はもうありません", ref, treeSHA)
			}

			return tree, err
		},
	}
}

func partitionInstalled(installed []InstalledSkill) ([]UpdateCandidate, map[string][]InstalledSkill) {
	byRepo := make(map[string][]InstalledSkill)

	var candidates []UpdateCandidate

	for _, s := range installed {
		switch {
		case s.Meta.Pinned != "":
			candidates = append(candidates, pinnedCandidate(s))
		case s.Meta.IsLocal():
			candidates = append(candidates, UpdateCandidate{
				SkillName: s.Name,
				Status:    StatusLocal,
				Reason:    s.Meta.LocalPath,
				Foreign:   true,
			})
		case s.Meta.Path == "":
			candidates = append(candidates, failedCandidate(s, "スキルのメタデータに github-path がありません"))
		default:
			byRepo[s.Meta.RepoURL] = append(byRepo[s.Meta.RepoURL], s)
		}
	}

	return candidates, byRepo
}

func trackedCandidate(s InstalledSkill) UpdateCandidate {
	source, _ := OwnerRepoFromURL(s.Meta.RepoURL)

	return UpdateCandidate{SkillName: s.Name, Source: source, CurrentHash: s.Meta.TreeSHA, Foreign: true}
}

func failedCandidate(s InstalledSkill, reason string) UpdateCandidate {
	c := trackedCandidate(s)
	c.Status, c.Reason = StatusCheckFailed, reason

	return c
}

func resolveRepoCandidates(group []InstalledSkill, fns UpstreamFuncs) []UpdateCandidate {
	repoURL := group[0].Meta.RepoURL

	snap, upstream, err := fetchRepo(repoURL, fns)
	if err != nil {
		candidates := make([]UpdateCandidate, 0, len(group))
		for _, s := range group {
			candidates = append(candidates, failedCandidate(s, err.Error()))
		}

		return candidates
	}

	candidates := make([]UpdateCandidate, 0, len(group))
	for _, s := range group {
		candidate := trackedCandidate(s)
		candidate.Ref = snap.Ref.Full

		up, err := resolveTrackedUpstream(s, snap.FS, upstream)
		switch {
		case errors.Is(err, errRemoved):
			candidate.Status, candidate.PreviousPath = StatusRemoved, s.Meta.Path
		case err != nil:
			candidate.Status, candidate.Reason = StatusCheckFailed, err.Error()
		default:
			candidate = resolveTracked(candidate, s, up, snap)
		}

		candidates = append(candidates, candidate)
	}

	sortCandidates(candidates)

	return append(candidates, newCandidates(candidates, upstream, candidates[0].Source)...)
}

func resolveTrackedUpstream(s InstalledSkill, repo fs.FS, upstream []DiscoveredSkill) (DiscoveredSkill, error) {
	if mode, ok := lstat(repo, s.Meta.Path); ok && fs.ValidPath(s.Meta.Path) && (!mode.IsDir() || hasSkillMD(repo, s.Meta.Path)) {
		return DiscoveredSkill{Name: s.Name, Path: s.Meta.Path, FS: subFS(repo, s.Meta.Path)}, nil
	}

	return resolveUpstream(s.Name, s.Meta.Path, upstream)
}

func fetchRepo(repoURL string, fns UpstreamFuncs) (git.Snapshot, []DiscoveredSkill, error) {
	source, err := OwnerRepoFromURL(repoURL)
	if err != nil {
		return git.Snapshot{}, nil, err
	}

	snap, err := fns.FetchLatest(repoURL)
	if err != nil {
		return git.Snapshot{}, nil, fmt.Errorf("%s の取得: %w", source, err)
	}

	upstream, err := DiscoverSkills(snap.FS)
	if err != nil {
		return git.Snapshot{}, nil, fmt.Errorf("%s のスキルの探索: %w", source, err)
	}

	return snap, upstream, nil
}

func resolveTracked(candidate UpdateCandidate, s InstalledSkill, up DiscoveredSkill, snap git.Snapshot) UpdateCandidate {
	treeSHA, err := snap.TreeSHA(up.Path)
	if err != nil {
		candidate.Status, candidate.Reason = StatusCheckFailed, err.Error()
		return candidate
	}

	candidate.Path, candidate.LatestHash = up.Path, treeSHA
	candidate.Status, candidate.PreviousPath = changeStatus(s.Meta.Path, up.Path, treeSHA != s.Meta.TreeSHA)

	if !candidate.Applicable() {
		return candidate
	}

	candidate.Upstream, err = withMetadata(up.FS, SourceMetadata{
		RepoURL: s.Meta.RepoURL,
		Ref:     snap.Ref.Full,
		TreeSHA: treeSHA,
		Path:    up.Path,
	})
	if err != nil {
		candidate.Status, candidate.Reason = StatusCheckFailed, err.Error()
	}

	return candidate
}

func pinnedCandidate(s InstalledSkill) UpdateCandidate {
	source, _ := OwnerRepoFromURL(s.Meta.RepoURL)

	return UpdateCandidate{
		SkillName: s.Name,
		Source:    source,
		Ref:       s.Meta.Ref,
		Status:    StatusPinned,
		Reason:    s.Meta.Pinned,
		Foreign:   true,
	}
}
