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
	"github.com/h3y6e/skolar/internal/testutil"
)

func TestMain(m *testing.M) {
	if os.Getenv("RUN_SKOLAR_MAIN") == "1" {
		os.Exit(cmd.Execute("test"))
	}

	testutil.Main(m)
}

func initBareRepoWithRefs(t *testing.T) string {
	t.Helper()

	work := testutil.InitWorkRepo(t)
	skillFile := "skills/alpha/SKILL.md"
	testutil.Commit(t, work, "main", map[string]string{skillFile: "# Alpha main\n"})
	testutil.Git(t, work, "tag", "v1.0.0")

	testutil.Git(t, work, "checkout", "-b", "feature/install")
	testutil.Commit(t, work, "feature", map[string]string{skillFile: "# Alpha feature\n"})
	testutil.Git(t, work, "checkout", "main")

	return testutil.BareClone(t, work)
}

func initBareRepoWithSplitRefSkills(t *testing.T) string {
	t.Helper()

	work := testutil.InitWorkRepo(t)
	testutil.Commit(t, work, "main", map[string]string{"skills/alpha/SKILL.md": "# Alpha main\n"})

	testutil.Git(t, work, "checkout", "-b", "feature/install")
	testutil.Commit(t, work, "feature", map[string]string{"skills/beta/SKILL.md": "# Beta feature\n"})
	testutil.Git(t, work, "checkout", "main")

	return testutil.BareClone(t, work)
}

func TestAddDryRun(t *testing.T) {
	t.Parallel()

	bareURL := testutil.BareRepo(t, map[string]string{
		"skills/alpha/SKILL.md": "# Alpha\n",
		"skills/beta/SKILL.md":  "# Beta\n",
	})

	t.Run("--dry-run を指定したとき、探索したスキルを一覧表示する", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"add", "--dry-run", bareURL})

		if err := root.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}

		got := out.String()
		if !strings.Contains(got, "alpha") {
			t.Errorf("output should contain 'alpha', got: %q", got)
		}

		if !strings.Contains(got, "beta") {
			t.Errorf("output should contain 'beta', got: %q", got)
		}
	})

	t.Run("ソース引数がないとき、エラーになる", func(t *testing.T) {
		t.Parallel()

		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"add", "--dry-run"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error when no source provided")
		}
	})
}

func TestAddSkillFlag(t *testing.T) {
	t.Parallel()

	t.Run("--skill フラグが登録されている", func(t *testing.T) {
		t.Parallel()

		root := cmd.NewRootCmd("test")
		addCmd, _, _ := root.Find([]string{"add"})

		flag := addCmd.Flags().Lookup("skill")
		if flag == nil {
			t.Fatal("expected --skill flag on add command")
		}
	})

	t.Run("-s の短縮形が使える", func(t *testing.T) {
		t.Parallel()

		root := cmd.NewRootCmd("test")
		addCmd, _, _ := root.Find([]string{"add"})

		flag := addCmd.Flags().ShorthandLookup("s")
		if flag == nil {
			t.Fatal("expected -s shorthand on add command")
		}
	})
}

func TestAddInstall(t *testing.T) {
	t.Parallel()

	bareURL := testutil.BareRepo(t, map[string]string{
		"skills/alpha/SKILL.md": "# Alpha\n",
		"skills/alpha/ref.md":   "alpha ref\n",
		"skills/beta/SKILL.md":  "# Beta\n",
	})

	t.Run("--skill を指定したとき、選択したスキルだけをインストールする", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"add", "-s", "alpha", "-d", skillsDir, bareURL})

		if err := root.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md"))
		if err != nil {
			t.Fatalf("expected alpha/SKILL.md: %v", err)
		}

		if string(data) != "# Alpha\n" {
			t.Errorf("content = %q, want %q", string(data), "# Alpha\n")
		}

		data, err = os.ReadFile(filepath.Join(skillsDir, "alpha", "ref.md"))
		if err != nil {
			t.Fatalf("expected alpha/ref.md: %v", err)
		}

		if string(data) != "alpha ref\n" {
			t.Errorf("content = %q, want %q", string(data), "alpha ref\n")
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "beta")); !errors.Is(err, os.ErrNotExist) {
			t.Error("beta should not be installed when --skill alpha is specified")
		}

		lockData, err := os.ReadFile(lock.FilePath(skillsDir))
		if err != nil {
			t.Fatalf("expected skills-lock.json: %v", err)
		}

		lockStr := string(lockData)
		if !strings.Contains(lockStr, `"alpha"`) {
			t.Errorf("lockfile should contain alpha entry, got: %s", lockStr)
		}

		if strings.Contains(lockStr, `"beta"`) {
			t.Errorf("lockfile should not contain beta entry, got: %s", lockStr)
		}
	})

	t.Run("--skill を指定しないとき、すべてのスキルをインストールする", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"add", "-d", skillsDir, bareURL})

		if err := root.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "alpha", "SKILL.md")); err != nil {
			t.Errorf("expected alpha/SKILL.md: %v", err)
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "beta", "SKILL.md")); err != nil {
			t.Errorf("expected beta/SKILL.md: %v", err)
		}

		lockData, err := os.ReadFile(lock.FilePath(skillsDir))
		if err != nil {
			t.Fatalf("expected skills-lock.json: %v", err)
		}

		lockStr := string(lockData)
		if !strings.Contains(lockStr, `"alpha"`) {
			t.Error("lockfile should contain alpha")
		}

		if !strings.Contains(lockStr, `"beta"`) {
			t.Error("lockfile should contain beta")
		}
	})

	t.Run("ソースが ref フラグメントを使うとき、ref が記録される", func(t *testing.T) {
		t.Parallel()

		bareURLWithRefs := initBareRepoWithRefs(t)
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"add", "-d", skillsDir, bareURLWithRefs + "#feature/install"})

		if err := root.Execute(); err != nil {
			t.Fatalf("execute error: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md"))
		if err != nil {
			t.Fatalf("expected alpha/SKILL.md: %v", err)
		}

		if string(data) != "# Alpha feature\n" {
			t.Errorf("content = %q, want %q", string(data), "# Alpha feature\n")
		}

		lf, err := lock.ReadFile(lock.FilePath(skillsDir))
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}

		if lf.Skills["alpha"].Ref != "feature/install" {
			t.Errorf("alpha.Ref = %q, want %q", lf.Skills["alpha"].Ref, "feature/install")
		}
	})

	t.Run("別のソースから2回目に追加したとき、ロックファイルがマージされる", func(t *testing.T) {
		t.Parallel()

		bareURL2 := testutil.BareRepo(t, map[string]string{
			"skills/gamma/SKILL.md": "# Gamma\n",
		})

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		root1 := cmd.NewRootCmd("test")
		root1.SetArgs([]string{"add", "-s", "alpha", "-d", skillsDir, bareURL})

		if err := root1.Execute(); err != nil {
			t.Fatalf("first add error: %v", err)
		}

		root2 := cmd.NewRootCmd("test")
		root2.SetArgs([]string{"add", "-d", skillsDir, bareURL2})

		if err := root2.Execute(); err != nil {
			t.Fatalf("second add error: %v", err)
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "alpha", "SKILL.md")); err != nil {
			t.Error("expected alpha to still be installed")
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "gamma", "SKILL.md")); err != nil {
			t.Error("expected gamma to be installed")
		}

		lockData, err := os.ReadFile(lock.FilePath(skillsDir))
		if err != nil {
			t.Fatalf("expected skills-lock.json: %v", err)
		}

		lockStr := string(lockData)
		if !strings.Contains(lockStr, `"alpha"`) {
			t.Error("lockfile should still contain alpha")
		}

		if !strings.Contains(lockStr, `"gamma"`) {
			t.Error("lockfile should contain gamma")
		}
	})

	t.Run("--skill が存在しないスキルを指定したとき、エラーになる", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"add", "-s", "nonexistent", "-d", skillsDir, bareURL})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error for nonexistent skill")
		}

		if !strings.Contains(err.Error(), "インストールするスキルが見つかりません") {
			t.Errorf("error = %q, want containing 'no skills found'", err.Error())
		}
	})

	t.Run("カスタムディレクトリで gh skill のメタデータに記録できないソースを --dry-run したとき、計画を表示する前に失敗する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"add", "--dry-run", "-d", filepath.Join(t.TempDir(), "custom", "skills"), bareURL})

		// Act
		err := root.Execute()

		// Assert
		if err == nil || !strings.Contains(err.Error(), "skills-lock.json に記録できるのは") {
			t.Fatalf("add error = %v, want an error about the lockfile's directory", err)
		}

		if out.Len() != 0 {
			t.Errorf("add printed a plan: %q", out.String())
		}
	})
}

func TestAddOverwrite(t *testing.T) {
	origIsTTY := cmd.IsTTY
	cmd.IsTTY = func() bool { return false }

	t.Cleanup(func() { cmd.IsTTY = origIsTTY })

	bareURL := testutil.BareRepo(t, map[string]string{
		"skills/alpha/SKILL.md": "# Alpha upstream\n",
	})
	setup := func(t *testing.T) string {
		t.Helper()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		if err := os.MkdirAll(filepath.Join(skillsDir, "alpha"), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(skillsDir, "alpha", "SKILL.md"), []byte("# Alpha local\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		return skillsDir
	}

	t.Run("既存のスキルに --dry-run を指定したとき、追加は上書きと表示してスキルを残す", func(t *testing.T) {
		// Arrange
		skillsDir := setup(t)

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"add", "--dry-run", "-d", skillsDir, bareURL})

		// Act
		err := root.Execute()
		// Assert
		if err != nil {
			t.Fatalf("add: %v", err)
		}

		if want := "上書き " + filepath.Join(skillsDir, "alpha"); !strings.Contains(out.String(), want) {
			t.Errorf("output = %q, want %q", out.String(), want)
		}

		if data, _ := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md")); string(data) != "# Alpha local\n" {
			t.Error("alpha should be unchanged")
		}
	})

	t.Run("TTY も --yes もなく既存のスキルを上書きしようとしたとき、追加は失敗してスキルを残す", func(t *testing.T) {
		// Arrange
		skillsDir := setup(t)
		root := cmd.NewRootCmd("test")
		root.SetOut(&bytes.Buffer{})
		root.SetArgs([]string{"add", "-d", skillsDir, bareURL})

		// Act
		err := root.Execute()

		// Assert
		if err == nil || !strings.Contains(err.Error(), "確認が必要です") {
			t.Fatalf("err = %v, want confirmation required", err)
		}

		if data, _ := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md")); string(data) != "# Alpha local\n" {
			t.Error("alpha should be unchanged")
		}
	})
}

func TestAddSelection(t *testing.T) {
	t.Parallel()
	bareURL := testutil.BareRepo(t, map[string]string{
		"skills/near/SKILL.md":     "# Near\n",
		"extras/deep/far/SKILL.md": "# Far\n",
	})

	t.Run("スキルを指定しないとき、追加はどちらのツールも既定で見つけるスキルだけをインストールする", func(t *testing.T) {
		t.Parallel()
		// Arrange
		dir := filepath.Join(t.TempDir(), ".agents", "skills")

		// Act
		addSkillsTo(t, "-d", dir, bareURL)

		// Assert
		if _, err := os.Stat(filepath.Join(dir, "near")); err != nil {
			t.Errorf("near should be installed: %v", err)
		}

		if _, err := os.Stat(filepath.Join(dir, "far")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("far should not be installed by default: %v", err)
		}
	})

	t.Run("既定の対象外のスキルを指定したとき、追加はそのスキルをインストールする", func(t *testing.T) {
		t.Parallel()
		// Arrange
		dir := filepath.Join(t.TempDir(), ".agents", "skills")

		// Act
		addSkillsTo(t, "-s", "far", "-d", dir, bareURL)

		// Assert
		if _, err := os.Stat(filepath.Join(dir, "far", "SKILL.md")); err != nil {
			t.Errorf("far should be installed: %v", err)
		}
	})
}

func addSkillsTo(t *testing.T, args ...string) {
	t.Helper()

	root := cmd.NewRootCmd("test")
	root.SetArgs(append([]string{"add", "-y"}, args...))

	if err := root.Execute(); err != nil {
		t.Fatalf("add %v: %v", args, err)
	}
}
