package skill_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/h3y6e/skolar/internal/testutil"
)

func writeSkillWithMetadata(t *testing.T, skillsDir, name string, meta skill.SourceMetadata) {
	t.Helper()

	content, err := skill.InjectSourceMetadata("# "+name+"\n", meta)
	if err != nil {
		t.Fatalf("InjectSourceMetadata() error = %v", err)
	}

	testutil.WriteFiles(t, skillsDir, map[string]string{
		name + "/SKILL.md": content,
	})
}

func TestScanInstalledSkills(t *testing.T) {
	t.Parallel()

	githubMeta := skill.SourceMetadata{
		RepoURL: "https://github.com/h3y6e/agent-skills",
		Ref:     "refs/tags/v2026.7.1",
		TreeSHA: "7a5507a41b20be505102f26e6b75927edd1493fd",
		Path:    "skills/creating-issues",
	}

	t.Run("dir に github のメタデータを持つスキルがあるとき、走査は名前順に返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skillsDir := t.TempDir()
		writeSkillWithMetadata(t, skillsDir, "zeta", githubMeta)
		writeSkillWithMetadata(t, skillsDir, "alpha", githubMeta)

		// Act
		installed, err := skill.ScanInstalledSkills(skillsDir)
		// Assert
		if err != nil {
			t.Fatalf("ScanInstalledSkills() error = %v", err)
		}

		if len(installed) != 2 {
			t.Fatalf("expected 2 skills, got %d", len(installed))
		}

		if installed[0].Name != "alpha" || installed[1].Name != "zeta" {
			t.Errorf("order = %q, %q; want alpha, zeta", installed[0].Name, installed[1].Name)
		}

		if installed[0].Meta.RepoURL != githubMeta.RepoURL {
			t.Errorf("Meta.RepoURL = %q", installed[0].Meta.RepoURL)
		}

		if installed[0].Dir != filepath.Join(skillsDir, "alpha") {
			t.Errorf("Dir = %q", installed[0].Dir)
		}
	})

	t.Run("SKILL.md にソースのメタデータがないとき、そのスキルは返らない", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skillsDir := t.TempDir()
		testutil.WriteFiles(t, skillsDir, map[string]string{
			"plain/SKILL.md": "---\nname: plain\n---\n# Plain\n",
		})

		// Act
		installed, err := skill.ScanInstalledSkills(skillsDir)
		// Assert
		if err != nil {
			t.Fatalf("ScanInstalledSkills() error = %v", err)
		}

		if len(installed) != 0 {
			t.Errorf("expected no skills, got %d", len(installed))
		}
	})

	t.Run("SKILL.md のフロントマターが不正なとき、そのスキルはスキップされる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skillsDir := t.TempDir()
		testutil.WriteFiles(t, skillsDir, map[string]string{
			"broken/SKILL.md": "---\n: : invalid\n---\n# Broken\n",
		})
		writeSkillWithMetadata(t, skillsDir, "valid", githubMeta)

		// Act
		installed, err := skill.ScanInstalledSkills(skillsDir)
		// Assert
		if err != nil {
			t.Fatalf("ScanInstalledSkills() error = %v", err)
		}

		if len(installed) != 1 || installed[0].Name != "valid" {
			t.Errorf("expected only the valid skill, got %+v", installed)
		}
	})

	t.Run("dir が存在しないとき、走査はスキルもエラーも返さない", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skillsDir := filepath.Join(t.TempDir(), "does-not-exist")

		// Act
		installed, err := skill.ScanInstalledSkills(skillsDir)
		// Assert
		if err != nil {
			t.Fatalf("ScanInstalledSkills() error = %v", err)
		}

		if len(installed) != 0 {
			t.Errorf("expected no skills, got %d", len(installed))
		}
	})
}

func collectTracked(installed []skill.InstalledSkill, fns skill.UpstreamFuncs) []skill.UpdateCandidate {
	candidates, _ := skill.CollectUpdateCandidates(nil, installed, nil, fns, func(int, int) {})

	return candidates
}

func readMeta(t *testing.T, fsys fs.FS) skill.SourceMetadata {
	t.Helper()

	data, err := fs.ReadFile(fsys, "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}

	meta, err := skill.ParseSourceMetadata(string(data))
	if err != nil {
		t.Fatal(err)
	}

	return meta
}

func TestCollectTrackedUpdateCandidates(t *testing.T) {
	t.Parallel()

	installed := func(name, repoURL, treeSHA, path string) skill.InstalledSkill {
		return skill.InstalledSkill{
			Name: name,
			Dir:  filepath.Join("dir", name),
			Meta: skill.SourceMetadata{RepoURL: repoURL, Ref: "refs/tags/v1.0.0", TreeSHA: treeSHA, Path: path},
		}
	}

	fakeFetch := func(t *testing.T, files map[string]string, tree string) func(string) (git.Snapshot, error) {
		t.Helper()

		return func(_ string) (git.Snapshot, error) {
			dir := t.TempDir()
			testutil.WriteFiles(t, dir, files)

			return snapshotOf(t, dir, tree, nil), nil
		}
	}

	t.Run("ルートのスキルの上流の SKILL.md が不正なとき、削除と誤認されず候補は失敗する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skills := []skill.InstalledSkill{installed("whole", "https://github.com/o/r", "tree-1", ".")}
		fns := skill.UpstreamFuncs{
			FetchLatest: fakeFetch(t, map[string]string{"SKILL.md": "---\nname: [\n---\n"}, "tree-2"),
		}

		// Act
		candidates := collectTracked(skills, fns)

		// Assert
		if len(candidates) != 1 || candidates[0].Status != skill.StatusCheckFailed {
			t.Errorf("candidates = %+v, want one check-failed", candidates)
		}
	})

	t.Run("上流のスキルのディレクトリがシンボリックリンクのとき、リンク先をステージせず候補は失敗する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		outside := t.TempDir()
		testutil.WriteFiles(t, outside, map[string]string{"SKILL.md": "---\nname: foo\n---\n", "secret": "s\n"})

		clone := filepath.Join(t.TempDir(), "repo")
		if err := os.MkdirAll(filepath.Join(clone, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(outside, filepath.Join(clone, "skills", "foo")); err != nil {
			t.Fatal(err)
		}

		skills := []skill.InstalledSkill{installed("foo", "https://github.com/o/r", "tree-1", "skills/foo")}
		fns := skill.UpstreamFuncs{
			FetchLatest: func(string) (git.Snapshot, error) {
				return snapshotOf(t, clone, "tree-2", map[string]string{"skills/foo": "tree-2"}), nil
			},
		}

		// Act
		candidates := collectTracked(skills, fns)

		// Assert
		if len(candidates) != 1 || candidates[0].Status != skill.StatusCheckFailed {
			t.Errorf("candidates = %+v, want one check-failed", candidates)
		}
	})

	t.Run("上流のツリー SHA が一致するとき、候補は最新になる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skills := []skill.InstalledSkill{installed("foo", "https://github.com/o/r", "tree-1", "skills/foo")}
		fns := skill.UpstreamFuncs{
			FetchLatest: fakeFetch(t, map[string]string{"skills/foo/SKILL.md": "# Foo\n"}, "tree-1"),
		}

		// Act
		candidates := collectTracked(skills, fns)

		// Assert
		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(candidates))
		}

		c := candidates[0]
		if c.Status != skill.StatusUpToDate {
			t.Errorf("Status = %q, want %q", c.Status, skill.StatusUpToDate)
		}

		if !c.Foreign {
			t.Error("Foreign = false, want true")
		}

		if c.CurrentHash != "tree-1" || c.LatestHash != "tree-1" {
			t.Errorf("hashes = %q -> %q, want tree-1 -> tree-1", c.CurrentHash, c.LatestHash)
		}
	})

	t.Run("上流のツリー SHA が異なるとき、候補は更新ありになってメタデータを注入した内容がステージされる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skills := []skill.InstalledSkill{installed("foo", "https://github.com/o/r", "tree-old", "skills/foo")}
		fetches := 0
		fns := skill.UpstreamFuncs{
			FetchLatest: func(_ string) (git.Snapshot, error) {
				fetches++
				dir := t.TempDir()
				testutil.WriteFiles(t, dir, map[string]string{
					"skills/foo/SKILL.md": "# Foo v2\n",
				})

				return snapshotOf(t, dir, "tree-new", nil), nil
			},
		}

		// Act
		candidates := collectTracked(skills, fns)

		// Assert
		if len(candidates) != 1 {
			t.Fatalf("expected 1 candidate, got %d", len(candidates))
		}

		c := candidates[0]
		if c.Status != skill.StatusUpdateAvailable {
			t.Fatalf("Status = %q, want %q", c.Status, skill.StatusUpdateAvailable)
		}

		if c.CurrentHash != "tree-old" || c.LatestHash != "tree-new" {
			t.Errorf("hashes = %q -> %q", c.CurrentHash, c.LatestHash)
		}

		if c.Ref != "refs/tags/v2.0.0" {
			t.Errorf("Ref = %q", c.Ref)
		}

		data, readErr := fs.ReadFile(c.Upstream, "SKILL.md")
		if readErr != nil {
			t.Fatalf("expected staged SKILL.md: %v", readErr)
		}

		stagedMeta, parseErr := skill.ParseSourceMetadata(string(data))
		if parseErr != nil {
			t.Fatalf("staged SKILL.md has no parseable metadata: %v", parseErr)
		}

		if stagedMeta.Ref != "refs/tags/v2.0.0" || stagedMeta.TreeSHA != "tree-new" || stagedMeta.RepoURL != "https://github.com/o/r" {
			t.Errorf("staged metadata = %+v", stagedMeta)
		}

		if !strings.Contains(string(data), "# Foo v2") {
			t.Errorf("staged content lost body:\n%s", data)
		}
	})

	t.Run("上流の SKILL.md が clone の外へのシンボリックリンクのとき、メタデータのステージはリンク先を変更しない", func(t *testing.T) {
		t.Parallel()

		// Arrange
		outside := filepath.Join(t.TempDir(), "victim.md")
		if err := os.WriteFile(outside, []byte("# Victim\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		skills := []skill.InstalledSkill{installed("foo", "https://github.com/o/r", "tree-old", "skills/foo")}
		fns := skill.UpstreamFuncs{
			FetchLatest: func(string) (git.Snapshot, error) {
				dir := t.TempDir()
				if err := os.MkdirAll(filepath.Join(dir, "skills/foo"), 0o755); err != nil {
					t.Fatal(err)
				}

				if err := os.Symlink(outside, filepath.Join(dir, "skills/foo/SKILL.md")); err != nil {
					t.Fatal(err)
				}

				return snapshotOf(t, dir, "tree-new", nil), nil
			},
		}

		// Act
		_ = collectTracked(skills, fns)

		// Assert
		data, readErr := os.ReadFile(outside)
		if readErr != nil {
			t.Fatal(readErr)
		}

		if string(data) != "# Victim\n" {
			t.Errorf("file outside the clone was modified:\n%s", data)
		}
	})

	t.Run("パスも名前も一致する上流スキルがないとき、候補は上流で削除されたと判定される", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skills := []skill.InstalledSkill{installed("foo", "https://github.com/o/r", "tree-1", "skills/foo")}
		fns := skill.UpstreamFuncs{
			FetchLatest: fakeFetch(t, map[string]string{"skills/bar/SKILL.md": "# Bar\n"}, "tree-1"),
		}

		// Act
		candidates := collectTracked(skills, fns)

		// Assert
		if candidates[1].SkillName != "foo" || candidates[1].Status != skill.StatusRemoved || candidates[1].PreviousPath != "skills/foo" {
			t.Errorf("candidates = %+v, want foo removed from skills/foo", candidates)
		}
	})

	t.Run("スキルが上流で移動したとき、候補は新しいパスでステージされてほかの上流スキルは新規になる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skills := []skill.InstalledSkill{installed("foo", "https://github.com/o/r", "tree-1", "skills/old/foo")}
		fns := skill.UpstreamFuncs{
			FetchLatest: fakeFetch(t, map[string]string{"skills/foo/SKILL.md": "# Foo\n", "skills/bar/SKILL.md": "# Bar\n"}, "tree-1"),
		}

		// Act
		candidates := collectTracked(skills, fns)

		// Assert
		if len(candidates) != 2 {
			t.Fatalf("candidates = %+v, want foo and bar", candidates)
		}

		bar, foo := candidates[0], candidates[1]
		if foo.Status != skill.StatusMoved || foo.PreviousPath != "skills/old/foo" || foo.Path != "skills/foo" {
			t.Errorf("foo = %+v, want moved from skills/old/foo to skills/foo", foo)
		}

		if got := readMeta(t, foo.Upstream).Path; got != "skills/foo" {
			t.Errorf("staged github-path = %q, want skills/foo", got)
		}

		if bar.Status != skill.StatusNew || bar.Path != "skills/bar" {
			t.Errorf("bar = %+v, want new at skills/bar", bar)
		}
	})

	t.Run("スキルが固定されているとき、候補は取得せずに固定と報告する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		pinned := installed("foo", "https://github.com/o/r", "tree-1", "skills/foo")
		pinned.Meta.Pinned = "v1.0.0"
		fns := skill.UpstreamFuncs{
			FetchLatest: func(string) (git.Snapshot, error) {
				t.Fatal("FetchLatest called for pinned skill")
				return git.Snapshot{}, nil
			},
		}

		// Act
		candidates := collectTracked([]skill.InstalledSkill{pinned}, fns)

		// Assert
		c := candidates[0]
		if c.Status != skill.StatusPinned {
			t.Errorf("Status = %q, want %q", c.Status, skill.StatusPinned)
		}

		if c.Reason != "v1.0.0" {
			t.Errorf("Reason = %q, want %q", c.Reason, "v1.0.0")
		}
	})

	t.Run("スキルがローカルのパスからインストールされているとき、候補は取得せずにローカルと報告する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		local := skill.InstalledSkill{
			Name: "mine",
			Dir:  filepath.Join("dir", "mine"),
			Meta: skill.SourceMetadata{LocalPath: "/home/user/skills/mine"},
		}
		fns := skill.UpstreamFuncs{
			FetchLatest: func(string) (git.Snapshot, error) {
				t.Fatal("FetchLatest called for local skill")
				return git.Snapshot{}, nil
			},
		}

		// Act
		candidates := collectTracked([]skill.InstalledSkill{local}, fns)

		// Assert
		c := candidates[0]
		if c.Status != skill.StatusLocal {
			t.Errorf("Status = %q, want %q", c.Status, skill.StatusLocal)
		}

		if c.Reason != "/home/user/skills/mine" {
			t.Errorf("Reason = %q", c.Reason)
		}
	})

	t.Run("リポジトリを取得できないとき、そのスキルは確認失敗になってほかのリポジトリは解決される", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skills := []skill.InstalledSkill{
			installed("gone", "https://github.com/o/gone", "tree-1", "skills/gone"),
			installed("foo", "https://github.com/o/r", "tree-2", "skills/foo"),
		}
		fns := skill.UpstreamFuncs{
			FetchLatest: func(repoURL string) (git.Snapshot, error) {
				if repoURL == "https://github.com/o/gone" {
					return git.Snapshot{}, errors.New("repository not found")
				}

				dir := t.TempDir()
				testutil.WriteFiles(t, dir, map[string]string{"skills/foo/SKILL.md": "# Foo\n"})

				return snapshotOf(t, dir, "tree-2", nil), nil
			},
		}

		// Act
		candidates := collectTracked(skills, fns)

		// Assert
		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}

		byName := map[string]skill.UpdateCandidate{}
		for _, c := range candidates {
			byName[c.SkillName] = c
		}

		if byName["gone"].Status != skill.StatusCheckFailed {
			t.Errorf("gone Status = %q, want %q", byName["gone"].Status, skill.StatusCheckFailed)
		}

		if byName["foo"].Status != skill.StatusUpToDate {
			t.Errorf("foo Status = %q, want %q", byName["foo"].Status, skill.StatusUpToDate)
		}
	})

	t.Run("上流の 1 つのスキルのフロントマターが不正なとき、そのスキルだけが確認失敗になって同じ階層のスキルは更新ありのままになる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skills := []skill.InstalledSkill{
			installed("bad", "https://github.com/o/r", "tree-old", "skills/bad"),
			installed("good", "https://github.com/o/r", "tree-old", "skills/good"),
		}
		fns := skill.UpstreamFuncs{
			FetchLatest: func(string) (git.Snapshot, error) {
				dir := t.TempDir()
				testutil.WriteFiles(t, dir, map[string]string{
					"skills/bad/SKILL.md":  "---\nname: bad\ncompatibility: MCP (tools: search)\n---\n",
					"skills/good/SKILL.md": "---\nname: good\n---\n",
				})

				return snapshotOf(t, dir, "tree-new", nil), nil
			},
		}

		// Act
		candidates := collectTracked(skills, fns)

		// Assert
		byName := map[string]skill.UpdateCandidate{}
		for _, c := range candidates {
			byName[c.SkillName] = c
		}

		if byName["bad"].Status != skill.StatusCheckFailed {
			t.Errorf("bad Status = %q, want %q", byName["bad"].Status, skill.StatusCheckFailed)
		}

		if byName["good"].Status != skill.StatusUpdateAvailable {
			t.Errorf("good Status = %q, want %q", byName["good"].Status, skill.StatusUpdateAvailable)
		}
	})

	t.Run("スキルに github-path がないとき、候補は取得せずに確認失敗になる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		broken := installed("foo", "https://github.com/o/r", "tree-1", "")
		fns := skill.UpstreamFuncs{
			FetchLatest: func(string) (git.Snapshot, error) {
				t.Fatal("FetchLatest called for skill without github-path")
				return git.Snapshot{}, nil
			},
		}

		// Act
		candidates := collectTracked([]skill.InstalledSkill{broken}, fns)

		// Assert
		c := candidates[0]
		if c.Status != skill.StatusCheckFailed {
			t.Errorf("Status = %q, want %q", c.Status, skill.StatusCheckFailed)
		}

		if !strings.Contains(c.Reason, "github-path") {
			t.Errorf("Reason = %q, want it to mention github-path", c.Reason)
		}
	})

	t.Run("2 つのスキルが同じリポジトリを共有するとき、リポジトリは 1 度だけ取得される", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skills := []skill.InstalledSkill{
			installed("foo", "https://github.com/o/r", "tree-1", "skills/foo"),
			installed("bar", "https://github.com/o/r", "tree-2", "skills/bar"),
		}

		var fetches atomic.Int32

		fns := skill.UpstreamFuncs{
			FetchLatest: func(_ string) (git.Snapshot, error) {
				fetches.Add(1)

				dir := t.TempDir()
				testutil.WriteFiles(t, dir, map[string]string{
					"skills/foo/SKILL.md": "# Foo\n",
					"skills/bar/SKILL.md": "# Bar\n",
				})

				return snapshotOf(t, dir, "tree-2", map[string]string{"skills/foo": "tree-1"}), nil
			},
		}

		// Act
		candidates := collectTracked(skills, fns)

		// Assert
		if fetches.Load() != 1 {
			t.Errorf("fetches = %d, want 1", fetches.Load())
		}

		if len(candidates) != 2 {
			t.Fatalf("expected 2 candidates, got %d", len(candidates))
		}

		for _, c := range candidates {
			if c.Status != skill.StatusUpToDate {
				t.Errorf("%s Status = %q, want up-to-date", c.SkillName, c.Status)
			}
		}
	})
}

func TestPreferTrackedInstall(t *testing.T) {
	t.Parallel()

	t.Run("ロックファイルがなくソースが github のとき、gh skill 形式が優先される", func(t *testing.T) {
		t.Parallel()

		// Arrange
		lockfileExists := false

		// Act
		got := skill.PreferTrackedInstall(lockfileExists, "github")

		// Assert
		if !got {
			t.Error("PreferTrackedInstall() = false, want true")
		}
	})

	t.Run("ロックファイルがあるとき、github のソースでもロックファイル形式が保たれる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		lockfileExists := true

		// Act
		got := skill.PreferTrackedInstall(lockfileExists, "github")

		// Assert
		if got {
			t.Error("PreferTrackedInstall() = true, want false")
		}
	})

	t.Run("ソースが github でないとき、更新を追跡できるようロックファイル形式が保たれる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		lockfileExists := false

		// Act
		for _, sourceType := range []lock.SourceType{lock.SourceLocal, lock.SourceGitLab, lock.SourceGit} {
			if got := skill.PreferTrackedInstall(lockfileExists, sourceType); got {
				t.Errorf("PreferTrackedInstall(%q) = true, want false", sourceType)
			}
		}
	})
}

func TestFilterInstalledBySource(t *testing.T) {
	t.Parallel()

	installed := []skill.InstalledSkill{
		{Name: "alpha", Meta: skill.SourceMetadata{RepoURL: "https://github.com/h3y6e/spec-skills"}},
		{Name: "beta", Meta: skill.SourceMetadata{RepoURL: "https://github.com/obra/superpowers"}},
		{Name: "pinned", Meta: skill.SourceMetadata{RepoURL: "https://github.com/h3y6e/spec-skills", Pinned: "v1"}},
		{Name: "local", Meta: skill.SourceMetadata{LocalPath: "/home/user/skills/local"}},
	}

	t.Run("ソースが空のとき、全スキルが返る", func(t *testing.T) {
		t.Parallel()

		// Act
		got, err := skill.FilterInstalledBySource(installed, "")
		// Assert
		if err != nil {
			t.Fatalf("FilterInstalledBySource() error = %v", err)
		}

		if len(got) != 4 {
			t.Errorf("expected 4 skills, got %d", len(got))
		}
	})

	t.Run("ソースを指定したとき、そのリポジトリのスキルだけが返る", func(t *testing.T) {
		t.Parallel()

		// Act
		got, err := skill.FilterInstalledBySource(installed, "h3y6e/spec-skills")
		// Assert
		if err != nil {
			t.Fatalf("FilterInstalledBySource() error = %v", err)
		}

		if len(got) != 1 || got[0].Name != "alpha" {
			t.Errorf("FilterInstalledBySource() = %+v", got)
		}
	})

	t.Run("ソースに ref があるとき、その ref に固定されたスキルだけが返る", func(t *testing.T) {
		t.Parallel()

		// Act
		got, err := skill.FilterInstalledBySource(installed, "h3y6e/spec-skills#v1")
		// Assert
		if err != nil {
			t.Fatalf("FilterInstalledBySource() error = %v", err)
		}

		if len(got) != 1 || got[0].Name != "pinned" {
			t.Errorf("FilterInstalledBySource() = %+v", got)
		}
	})

	t.Run("メタデータのリポジトリが github.com にないとき、ソースに一致しない", func(t *testing.T) {
		t.Parallel()

		// Arrange
		foreignHost := []skill.InstalledSkill{
			{Name: "spoofed", Meta: skill.SourceMetadata{RepoURL: "https://internal.example/h3y6e/spec-skills"}},
		}

		// Act
		got, err := skill.FilterInstalledBySource(foreignHost, "h3y6e/spec-skills")
		// Assert
		if err != nil {
			t.Fatalf("FilterInstalledBySource() error = %v", err)
		}

		if len(got) != 0 {
			t.Errorf("FilterInstalledBySource() = %+v", got)
		}
	})

	t.Run("ソースが不正なとき、エラーが返る", func(t *testing.T) {
		t.Parallel()

		// Act
		_, err := skill.FilterInstalledBySource(installed, "::invalid::")

		// Assert
		if err == nil {
			t.Fatal("FilterInstalledBySource() expected error, got nil")
		}
	})
}

func TestUnmanagedSkills(t *testing.T) {
	t.Parallel()

	t.Run("スキルがロックファイルにあるとき、管理外のスキルから除かれる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		installed := []skill.InstalledSkill{
			{Name: "managed", Meta: skill.SourceMetadata{RepoURL: "https://github.com/o/r"}},
			{Name: "foreign", Meta: skill.SourceMetadata{RepoURL: "https://github.com/o/r"}},
		}
		managed := map[string]lock.Entry{"managed": {Source: "o/r"}}

		// Act
		foreign := skill.UnmanagedSkills(installed, managed)

		// Assert
		if len(foreign) != 1 || foreign[0].Name != "foreign" {
			t.Errorf("UnmanagedSkills() = %+v", foreign)
		}
	})
}
