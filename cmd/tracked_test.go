package cmd_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/h3y6e/skolar/cmd"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/h3y6e/skolar/internal/testutil"
)

func initBareRepoWithTwoTags(t *testing.T) string {
	t.Helper()

	work := testutil.InitWorkRepo(t)
	testutil.Commit(t, work, "v1", map[string]string{
		"skills/alpha/SKILL.md": "# Alpha v1\n",
		"skills/beta/SKILL.md":  "# Beta v1\n",
	})
	testutil.Git(t, work, "tag", "v1.0.0")
	testutil.Commit(t, work, "v2", map[string]string{
		"skills/alpha/SKILL.md": "# Alpha v2\n",
		"skills/beta/SKILL.md":  "# Beta v2\n",
	})
	testutil.Git(t, work, "tag", "v2.0.0")

	return testutil.BareClone(t, work)
}

func stubGitHubTransport(t *testing.T, canonical, bareURL string) {
	t.Helper()

	repoURL := "https://github.com/" + canonical

	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+bareURL+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", repoURL)
	t.Setenv("GIT_CONFIG_KEY_1", "url."+bareURL+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_1", repoURL+".git")
}

func runSkills(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	root := cmd.NewRootCmd("test")
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.Execute()

	return out.String(), err
}

func readMetadata(t *testing.T, skillsDir, name string) skill.SourceMetadata {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(skillsDir, name, "SKILL.md"))
	if err != nil {
		t.Fatalf("expected %s/SKILL.md: %v", name, err)
	}

	meta, err := skill.ParseSourceMetadata(string(data))
	if err != nil {
		t.Fatalf("ParseSourceMetadata() error = %v", err)
	}

	return meta
}

func TestAddTrackedMode(t *testing.T) {
	t.Run("ロックファイルがないとき、追加はロックファイルを作らず gh skill 形式でインストールする", func(t *testing.T) {
		// Arrange
		stubGitHubTransport(t, "h3y6e/spec-skills", initBareRepoWithTwoTags(t))
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		// Act
		output, err := runSkills(t, "add", "-s", "alpha", "-d", skillsDir, "h3y6e/spec-skills")
		// Assert
		if err != nil {
			t.Fatalf("add: %v", err)
		}

		if !strings.Contains(output, "インストール "+filepath.Join(skillsDir, "alpha")) {
			t.Errorf("output = %q, want it to contain %q", output, "インストール "+filepath.Join(skillsDir, "alpha"))
		}

		meta := readMetadata(t, skillsDir, "alpha")
		if meta.RepoURL != "https://github.com/h3y6e/spec-skills" {
			t.Errorf("RepoURL = %q", meta.RepoURL)
		}

		if meta.Ref != "refs/tags/v2.0.0" {
			t.Errorf("Ref = %q, want %q (latest release)", meta.Ref, "refs/tags/v2.0.0")
		}

		if meta.TreeSHA == "" || meta.Path != "skills/alpha" || meta.Pinned != "" {
			t.Errorf("unexpected metadata: %+v", meta)
		}

		if _, err := os.Stat(lock.FilePath(skillsDir)); !errors.Is(err, os.ErrNotExist) {
			t.Error("no lockfile should be created in gh skill format")
		}
	})

	t.Run("ロックファイルがあるとき、追加はメタデータを埋め込まずロックモードを使う", func(t *testing.T) {
		// Arrange
		bareURL := initBareRepoWithTwoTags(t)
		stubGitHubTransport(t, "h3y6e/spec-skills", bareURL)
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		seed := lock.File{Version: 1, Skills: map[string]lock.Entry{}}
		if err := lock.WriteFile(lock.FilePath(skillsDir), seed); err != nil {
			t.Fatalf("seed lockfile: %v", err)
		}

		// Act
		_, err := runSkills(t, "add", "-s", "alpha", "-d", skillsDir, bareURL)
		// Assert
		if err != nil {
			t.Fatalf("add: %v", err)
		}

		lf, err := lock.ReadFile(lock.FilePath(skillsDir))
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}

		if _, ok := lf.Skills["alpha"]; !ok {
			t.Error("lockfile should contain alpha in lock mode")
		}

		data, err := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md"))
		if err != nil {
			t.Fatalf("read SKILL.md: %v", err)
		}

		if string(data) != "# Alpha v2\n" {
			t.Errorf("lock mode must not inject metadata, content = %q", string(data))
		}
	})

	t.Run("ソースが ref フラグメントを使うとき、インストールはそのバージョンに固定される", func(t *testing.T) {
		// Arrange
		stubGitHubTransport(t, "h3y6e/spec-skills", initBareRepoWithTwoTags(t))
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		// Act
		_, err := runSkills(t, "add", "-s", "alpha", "-d", skillsDir, "h3y6e/spec-skills#v1.0.0")
		// Assert
		if err != nil {
			t.Fatalf("add: %v", err)
		}

		meta := readMetadata(t, skillsDir, "alpha")
		if meta.Pinned != "v1.0.0" {
			t.Errorf("Pinned = %q, want %q", meta.Pinned, "v1.0.0")
		}

		if meta.Ref != "refs/tags/v1.0.0" {
			t.Errorf("Ref = %q, want %q", meta.Ref, "refs/tags/v1.0.0")
		}

		data, err := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md"))
		if err != nil {
			t.Fatalf("read SKILL.md: %v", err)
		}

		if !strings.Contains(string(data), "# Alpha v1") {
			t.Errorf("content = %q, want Alpha v1", string(data))
		}
	})

	t.Run("ソースがローカルでロックファイルもないとき、追加はロックモードを保つ", func(t *testing.T) {
		// Arrange
		bareURL := testutil.BareRepo(t, map[string]string{
			"skills/foo/SKILL.md": "# Foo\n",
		})
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		// Act
		_, err := runSkills(t, "add", "-d", skillsDir, bareURL)
		// Assert
		if err != nil {
			t.Fatalf("add: %v", err)
		}

		lf, err := lock.ReadFile(lock.FilePath(skillsDir))
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}

		if _, ok := lf.Skills["foo"]; !ok {
			t.Error("local source should use lock mode even without a lockfile")
		}
	})

	t.Run("ソースが github でもローカルでもなくロックファイルもないとき、追加はロックモードに切り替わる", func(t *testing.T) {
		// Arrange
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		// Act
		_, err := runSkills(t, "add", "-d", skillsDir, "https://gitlab.com/example/repo.git")

		// Assert
		if err == nil {
			t.Fatal("add should fail at clone in lock mode")
		}

		if strings.Contains(err.Error(), "gh skill 形式") {
			t.Errorf("gitlab source must not enter gh skill format, error = %v", err)
		}
	})
}

func TestE2ETrackedLifecycle(t *testing.T) {
	origIsTTY := cmd.IsTTY
	cmd.IsTTY = func() bool { return false }

	t.Cleanup(func() { cmd.IsTTY = origIsTTY })

	stubGitHubTransport(t, "h3y6e/spec-skills", initBareRepoWithTwoTags(t))
	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

	if _, err := runSkills(t, "add", "-s", "alpha", "-d", skillsDir, "h3y6e/spec-skills"); err != nil {
		t.Fatalf("add: %v", err)
	}

	listOut, err := runSkills(t, "list", "-d", skillsDir)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if !strings.Contains(listOut, "alpha") {
		t.Fatalf("list should show alpha, got: %q", listOut)
	}

	checkOut, err := runSkills(t, "update", "--dry-run", "-d", skillsDir)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if !strings.Contains(checkOut, "最新 1 件") {
		t.Fatalf("check should report alpha up-to-date, got: %q", checkOut)
	}

	alphaPath := filepath.Join(skillsDir, "alpha", "SKILL.md")
	meta := readMetadata(t, skillsDir, "alpha")
	meta.TreeSHA = "stale-tree-sha"
	data, _ := os.ReadFile(alphaPath)

	tampered, err := skill.InjectSourceMetadata(string(data), meta)
	if err != nil {
		t.Fatalf("InjectSourceMetadata() error = %v", err)
	}

	tampered = strings.Replace(tampered, "# Alpha v2", "# Alpha outdated", 1)
	if err := os.WriteFile(alphaPath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}

	checkOut, err = runSkills(t, "update", "--dry-run", "-d", skillsDir)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if !strings.Contains(checkOut, "  M  alpha  ") {
		t.Fatalf("check should report update available, got: %q", checkOut)
	}

	if _, err := runSkills(t, "update", "-y", "-d", skillsDir); err != nil {
		t.Fatalf("update: %v", err)
	}

	data, _ = os.ReadFile(alphaPath)
	if !strings.Contains(string(data), "# Alpha v2") {
		t.Errorf("update should restore upstream content, got:\n%s", data)
	}

	meta = readMetadata(t, skillsDir, "alpha")
	if meta.TreeSHA == "stale-tree-sha" || meta.TreeSHA == "" {
		t.Errorf("update should refresh github-tree-sha, got %q", meta.TreeSHA)
	}

	if _, err := os.Stat(lock.FilePath(skillsDir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("update must not create a lockfile for tracked skills")
	}

	checkOut, err = runSkills(t, "update", "--dry-run", "-d", skillsDir)
	if err != nil {
		t.Fatalf("check after update: %v", err)
	}

	if !strings.Contains(checkOut, "最新 1 件") {
		t.Fatalf("check after update should be up-to-date, got: %q", checkOut)
	}

	if _, err := runSkills(t, "remove", "-y", "-d", skillsDir, "alpha"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(skillsDir, "alpha")); !errors.Is(err, os.ErrNotExist) {
		t.Error("alpha directory should not exist after remove")
	}

	if _, err := os.Stat(lock.FilePath(skillsDir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("remove must not create a lockfile for tracked skills")
	}
}

func TestE2ETrackedPinnedIsSkipped(t *testing.T) {
	origIsTTY := cmd.IsTTY
	cmd.IsTTY = func() bool { return false }

	t.Cleanup(func() { cmd.IsTTY = origIsTTY })

	stubGitHubTransport(t, "h3y6e/spec-skills", initBareRepoWithTwoTags(t))
	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

	if _, err := runSkills(t, "add", "-s", "alpha", "-d", skillsDir, "h3y6e/spec-skills#v1.0.0"); err != nil {
		t.Fatalf("add: %v", err)
	}

	checkOut, err := runSkills(t, "update", "--dry-run", "-d", skillsDir)
	if err != nil {
		t.Fatalf("check: %v", err)
	}

	if !strings.Contains(checkOut, "固定 1 件") {
		t.Fatalf("check should report alpha pinned, got: %q", checkOut)
	}

	updateOut, err := runSkills(t, "update", "-y", "-d", skillsDir)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updateOut != "固定 1 件\n" {
		t.Fatalf("update should skip pinned alpha, got: %q", updateOut)
	}

	meta := readMetadata(t, skillsDir, "alpha")
	if meta.Pinned != "v1.0.0" || meta.Ref != "refs/tags/v1.0.0" {
		t.Errorf("pinned metadata should be untouched, got %+v", meta)
	}
}

func TestListMixedLockfileAndTrackedSkills(t *testing.T) {
	t.Parallel()

	// Arrange
	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

	seed := lock.File{
		Version: 1,
		Skills: map[string]lock.Entry{
			"beta": {Source: "h3y6e/spec-skills", SourceType: "github", ComputedHash: "abc123"},
		},
	}
	if err := lock.WriteFile(lock.FilePath(skillsDir), seed); err != nil {
		t.Fatalf("seed lockfile: %v", err)
	}

	content, err := skill.InjectSourceMetadata("# Alpha\n", skill.SourceMetadata{
		RepoURL: "https://github.com/h3y6e/agent-skills",
		Ref:     "refs/tags/v2026.7.1",
		TreeSHA: "7a5507a41b20be505102f26e6b75927edd1493fd",
		Path:    "skills/alpha",
	})
	if err != nil {
		t.Fatalf("InjectSourceMetadata() error = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(skillsDir, "alpha"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillsDir, "alpha", "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	listOut, err := runSkills(t, "list", "-d", skillsDir)
	// Assert
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	if !strings.Contains(listOut, "alpha") || !strings.Contains(listOut, "beta") {
		t.Fatalf("list should show both alpha and beta, got: %q", listOut)
	}
}
