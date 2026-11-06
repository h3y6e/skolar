package skill_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/h3y6e/skolar/internal/testutil"
)

func initGitRepo(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := testutil.InitWorkRepo(t)
	testutil.Commit(t, dir, "init", files)

	return dir
}

func TestMain(m *testing.M) { testutil.Main(m) }

func checkoutRepo(t *testing.T, dir string) git.Snapshot {
	t.Helper()

	ref, err := git.ResolveDefault(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}

	snap, err := newCache(t).Fetch(t.Context(), dir, ref)
	if err != nil {
		t.Fatal(err)
	}

	return snap
}

func newCache(t *testing.T) *git.Cache {
	t.Helper()

	c := git.NewCache()

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func snapshotOf(t *testing.T, dir, tree string, trees map[string]string) git.Snapshot {
	t.Helper()

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = root.Close() })

	treeSHA := func(path string) (string, error) {
		if sha, ok := trees[path]; ok {
			return sha, nil
		}

		if _, err := fs.Stat(root.FS(), path); err != nil {
			return "", err
		}

		return tree, nil
	}

	return git.Snapshot{FS: root.FS(), Ref: git.ResolvedRef{Full: "refs/tags/v2.0.0"}, TreeSHA: treeSHA}
}

func installTracked(t *testing.T, repo string, ref skill.SourceRef, resolved git.ResolvedRef, skillsDir string) []skill.DiscoveredSkill {
	t.Helper()

	snap := checkoutRepo(t, repo)
	snap.Ref = resolved

	discovered, err := skill.DiscoverSkills(snap.FS)
	if err != nil {
		t.Fatalf("DiscoverSkills() error = %v", err)
	}

	if err := skill.InstallTrackedSkills(discovered, snap, ref, lock.NewLayout(skillsDir)); err != nil {
		t.Fatalf("InstallTrackedSkills() error = %v", err)
	}

	return discovered
}

func readInstalledMetadata(t *testing.T, skillsDir, name string) skill.SourceMetadata {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(skillsDir, name, "SKILL.md"))
	if err != nil {
		t.Fatalf("expected installed SKILL.md: %v", err)
	}

	meta, err := skill.ParseSourceMetadata(string(data))
	if err != nil {
		t.Fatalf("ParseSourceMetadata() error = %v", err)
	}

	return meta
}

func TestInstallTrackedSkills(t *testing.T) {
	t.Parallel()

	t.Run("github のソースからインストールしたとき、SKILL.md にメタデータが注入されてロックファイルは書かれない", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repoRoot := initGitRepo(t, map[string]string{
			"skills/foo/SKILL.md": "# Foo\n",
		})
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		ref := skill.SourceRef{SourceType: "github", CanonicalSource: "h3y6e/agent-skills"}
		resolved := git.ResolvedRef{Full: "refs/heads/main"}

		// Act
		installTracked(t, repoRoot, ref, resolved, skillsDir)

		// Assert
		meta := readInstalledMetadata(t, skillsDir, "foo")
		if meta.RepoURL != "https://github.com/h3y6e/agent-skills" {
			t.Errorf("RepoURL = %q", meta.RepoURL)
		}

		if meta.Ref != "refs/heads/main" {
			t.Errorf("Ref = %q", meta.Ref)
		}

		if meta.Path != "skills/foo" {
			t.Errorf("Path = %q", meta.Path)
		}

		if meta.Pinned != "" {
			t.Errorf("Pinned = %q, want empty", meta.Pinned)
		}

		wantTree := gitTreeSHA(t, repoRoot, "skills/foo")
		if meta.TreeSHA != wantTree {
			t.Errorf("TreeSHA = %q, want %q", meta.TreeSHA, wantTree)
		}

		if _, err := os.Stat(lock.FilePath(skillsDir)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("lockfile must not be written in gh format, stat err = %v", err)
		}
	})

	t.Run("ソースに明示的な ref があるとき、メタデータはそれを固定として記録する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repoRoot := initGitRepo(t, map[string]string{
			"skills/foo/SKILL.md": "# Foo\n",
		})
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		ref := skill.SourceRef{SourceType: "github", CanonicalSource: "h3y6e/agent-skills", Ref: "v1.0.0"}
		resolved := git.ResolvedRef{Full: "refs/tags/v1.0.0"}

		// Act
		installTracked(t, repoRoot, ref, resolved, skillsDir)

		// Assert
		meta := readInstalledMetadata(t, skillsDir, "foo")
		if meta.Pinned != "v1.0.0" {
			t.Errorf("Pinned = %q, want %q", meta.Pinned, "v1.0.0")
		}

		if meta.Ref != "refs/tags/v1.0.0" {
			t.Errorf("Ref = %q", meta.Ref)
		}
	})

	t.Run("ソースが github でないとき、インストールはエラーを返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repoRoot := initGitRepo(t, map[string]string{
			"skills/foo/SKILL.md": "# Foo\n",
		})
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		ref := skill.SourceRef{SourceType: "gitlab", CanonicalSource: "h3y6e/agent-skills"}
		resolved := git.ResolvedRef{Full: "refs/heads/main"}

		discovered, err := skill.DiscoverSkills(os.DirFS(repoRoot))
		if err != nil {
			t.Fatalf("DiscoverSkills() error = %v", err)
		}

		// Act
		err = skill.InstallTrackedSkills(discovered, git.Snapshot{FS: os.DirFS(repoRoot), Ref: resolved, TreeSHA: func(string) (string, error) { return "", nil }}, ref, lock.NewLayout(skillsDir))

		// Assert
		if err == nil {
			t.Fatal("InstallTrackedSkills() expected error, got nil")
		}
	})

	t.Run("同名のスキルがあるとき、置き換えられる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repoRoot := initGitRepo(t, map[string]string{
			"skills/foo/SKILL.md": "# Foo v2\n",
		})
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		testutil.WriteFiles(t, skillsDir, map[string]string{
			"foo/SKILL.md": "# Foo v1\n",
		})

		ref := skill.SourceRef{SourceType: "github", CanonicalSource: "h3y6e/agent-skills"}
		resolved := git.ResolvedRef{Full: "refs/heads/main"}

		// Act
		installTracked(t, repoRoot, ref, resolved, skillsDir)

		// Assert
		data, err := os.ReadFile(filepath.Join(skillsDir, "foo", "SKILL.md"))
		if err != nil {
			t.Fatalf("expected installed SKILL.md: %v", err)
		}

		if got := string(data); !strings.Contains(got, "# Foo v2") {
			t.Errorf("installed content = %q, want it to contain %q", got, "# Foo v2")
		}
	})
}

func gitTreeSHA(t *testing.T, repoDir, path string) string {
	t.Helper()

	return strings.TrimSpace(testutil.Git(t, repoDir, "rev-parse", "HEAD:"+path))
}

func TestInstallTrackedSkillsLayout(t *testing.T) {
	t.Parallel()

	t.Run("スキルが実行ファイルとリンクを含むとき、インストールは gh skill と同様に両方を 0644 で書いてリンクはリンク先を内容とするファイルにする", func(t *testing.T) {
		t.Parallel()
		// Arrange
		repo := initGitRepo(t, map[string]string{"skills/s/SKILL.md": "# S\n", "skills/s/run.sh": "echo\n"})
		if err := os.Chmod(filepath.Join(repo, "skills", "s", "run.sh"), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink("SKILL.md", filepath.Join(repo, "skills", "s", "link.md")); err != nil {
			t.Fatal(err)
		}

		testutil.Commit(t, repo, "add", nil)

		skillsDir := filepath.Join(t.TempDir(), "skills")

		// Act
		installTracked(t, repo, skill.SourceRef{CanonicalSource: "o/r", SourceType: "github"}, git.ResolvedRef{Full: "refs/heads/main"}, skillsDir)

		// Assert
		for name, want := range map[string]string{"run.sh": "echo\n", "link.md": "SKILL.md"} {
			info, err := os.Lstat(filepath.Join(skillsDir, "s", name))
			if err != nil || info.Mode() != 0o644 {
				t.Errorf("%s mode = %v, %v, want -rw-r--r--", name, info, err)
				continue
			}

			if data, _ := os.ReadFile(filepath.Join(skillsDir, "s", name)); string(data) != want {
				t.Errorf("%s = %q, want %q", name, data, want)
			}
		}
	})
}
