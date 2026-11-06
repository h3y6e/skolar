package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/h3y6e/skolar/cmd"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/h3y6e/skolar/internal/testutil"
)

func TestRemoveFlags(t *testing.T) {
	t.Parallel()

	root := cmd.NewRootCmd("test")
	removeCmd, _, _ := root.Find([]string{"remove"})

	t.Run("--dir フラグが登録されている", func(t *testing.T) {
		t.Parallel()

		flag := removeCmd.Flags().Lookup("dir")
		if flag == nil {
			t.Fatal("expected --dir flag on remove command")
		}
	})
}

func TestRemoveAlias(t *testing.T) {
	t.Parallel()

	bareURL := testutil.BareRepo(t, map[string]string{
		"skills/alpha/SKILL.md": "# Alpha\n",
	})

	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

	root1 := cmd.NewRootCmd("test")
	root1.SetArgs([]string{"add", "-d", skillsDir, bareURL})

	if err := root1.Execute(); err != nil {
		t.Fatalf("add error: %v", err)
	}

	var out bytes.Buffer

	root2 := cmd.NewRootCmd("test")
	root2.SetOut(&out)
	root2.SetArgs([]string{"rm", "-y", "-d", skillsDir, "alpha"})

	if err := root2.Execute(); err != nil {
		t.Fatalf("rm error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(skillsDir, "alpha")); !errors.Is(err, os.ErrNotExist) {
		t.Error("expected alpha directory to be removed via rm alias")
	}

	lf, err := lock.ReadFile(lock.FilePath(skillsDir))
	if err != nil {
		t.Fatalf("read lockfile: %v", err)
	}

	if _, ok := lf.Skills["alpha"]; ok {
		t.Error("expected alpha to be removed from lockfile via rm alias")
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()

	t.Run("スキルを削除したとき、スキルのディレクトリとロックファイルのエントリが消える", func(t *testing.T) {
		t.Parallel()

		bareURL := testutil.BareRepo(t, map[string]string{
			"skills/alpha/SKILL.md": "# Alpha\n",
			"skills/beta/SKILL.md":  "# Beta\n",
		})

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		root1 := cmd.NewRootCmd("test")
		root1.SetArgs([]string{"add", "-d", skillsDir, bareURL})

		if err := root1.Execute(); err != nil {
			t.Fatalf("add error: %v", err)
		}

		root2 := cmd.NewRootCmd("test")
		root2.SetArgs([]string{"remove", "-y", "-d", skillsDir, "alpha"})

		if err := root2.Execute(); err != nil {
			t.Fatalf("remove error: %v", err)
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "alpha")); !errors.Is(err, os.ErrNotExist) {
			t.Error("expected alpha directory to be removed")
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "beta", "SKILL.md")); err != nil {
			t.Error("expected beta to remain after removing alpha")
		}

		lf, err := lock.ReadFile(lock.FilePath(skillsDir))
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}

		if _, ok := lf.Skills["alpha"]; ok {
			t.Error("alpha should not be in lockfile after removal")
		}

		if _, ok := lf.Skills["beta"]; !ok {
			t.Error("beta should still be in lockfile after removing alpha")
		}
	})

	t.Run("カスタムディレクトリでロックファイルにしかないスキルを削除したとき、失敗する", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()
		dirA := filepath.Join(rootDir, ".agents", "skills")

		lockData := []byte(`{
		  "version": 1,
		  "skills": {
		    "beta": {
		      "source": "h3y6e/spec-skills",
		      "sourceType": "github",
		      "computedHash": "abc123"
		    }
		  }
		}`)

		if err := os.MkdirAll(filepath.Dir(lock.FilePath(dirA)), 0o755); err != nil {
			t.Fatalf("mkdir lockfile parent: %v", err)
		}

		if err := os.WriteFile(lock.FilePath(dirA), lockData, 0o644); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"remove", "-d", filepath.Join(rootDir, "custom", "skills"), "beta"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error when removing skill installed in a different dir")
		}
	})

	t.Run("スキルがロックファイルにないとき、エラーになる", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		lf := lock.File{
			Version: 1,
			Skills: map[string]lock.Entry{
				"alpha": {Source: "h3y6e/spec-skills", SourceType: "github", ComputedHash: "abc123"},
			},
		}
		if err := lock.WriteFile(lock.FilePath(skillsDir), lf); err != nil {
			t.Fatal(err)
		}

		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"remove", "-d", skillsDir, "nonexistent"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error when removing nonexistent skill")
		}
	})

	t.Run("ロックファイルがないとき、エラーになる", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"remove", "-d", skillsDir, "alpha"})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error when lockfile missing")
		}
	})

	t.Run("カスタムディレクトリの gh 形式のスキルがロックファイルのエントリと同名のとき、削除してもエントリは残る", func(t *testing.T) {
		t.Parallel()

		// Arrange
		rootDir := t.TempDir()
		dirA := filepath.Join(rootDir, ".agents", "skills")
		custom := filepath.Join(rootDir, "custom", "skills")

		content, err := skill.InjectSourceMetadata("# Beta\n", skill.SourceMetadata{
			RepoURL: "https://github.com/h3y6e/spec-skills",
			Ref:     "refs/tags/v1.0.0",
			TreeSHA: "abc123",
			Path:    "skills/beta",
		})
		if err != nil {
			t.Fatalf("InjectSourceMetadata() error = %v", err)
		}

		if err := os.MkdirAll(filepath.Join(custom, "beta"), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(custom, "beta", "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		lf := lock.File{
			Version: 1,
			Skills: map[string]lock.Entry{
				"beta": {Source: "h3y6e/spec-skills", SourceType: "github", ComputedHash: "abc123"},
			},
		}
		if err := lock.WriteFile(lock.FilePath(dirA), lf); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		// Act
		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"remove", "-y", "-d", custom, "beta"})

		if err := root.Execute(); err != nil {
			t.Fatalf("remove: %v", err)
		}

		// Assert
		if _, err := os.Stat(filepath.Join(custom, "beta")); !errors.Is(err, os.ErrNotExist) {
			t.Error("expected beta directory in the custom directory to be removed")
		}

		updated, err := lock.ReadFile(lock.FilePath(dirA))
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}

		if _, ok := updated.Skills["beta"]; !ok {
			t.Errorf("lockfile entry for the default directory must be kept, got %+v", updated)
		}
	})
}

func TestRemoveConfirmation(t *testing.T) {
	origIsTTY := cmd.IsTTY

	t.Cleanup(func() { cmd.IsTTY = origIsTTY })

	t.Run("--dry-run を指定したとき、削除はディレクトリを表示して残す", func(t *testing.T) {
		// Arrange
		cmd.IsTTY = func() bool { return false }
		skillsDir := filepath.Join(t.TempDir(), "skills")
		writeTrackedSkill(t, skillsDir, "alpha")

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"remove", "--dry-run", "-d", skillsDir, "alpha"})

		// Act
		err := root.Execute()
		// Assert
		if err != nil {
			t.Fatalf("remove: %v", err)
		}

		if !strings.Contains(out.String(), filepath.Join(skillsDir, "alpha")) {
			t.Errorf("output = %q, want the directory to remove", out.String())
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "alpha")); err != nil {
			t.Error("alpha should remain")
		}
	})

	t.Run("TTY も --yes もないとき、削除は失敗してスキルを残す", func(t *testing.T) {
		// Arrange
		cmd.IsTTY = func() bool { return false }
		skillsDir := filepath.Join(t.TempDir(), "skills")
		writeTrackedSkill(t, skillsDir, "alpha")

		root := cmd.NewRootCmd("test")
		root.SetOut(&bytes.Buffer{})
		root.SetArgs([]string{"remove", "-d", skillsDir, "alpha"})

		// Act
		err := root.Execute()

		// Assert
		if err == nil || !strings.Contains(err.Error(), "確認が必要です") {
			t.Fatalf("err = %v, want confirmation required", err)
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "alpha")); err != nil {
			t.Error("alpha should remain")
		}
	})

	t.Run("TTY でユーザーが拒否したとき、削除は失敗してスキルを残す", func(t *testing.T) {
		// Arrange
		cmd.IsTTY = func() bool { return true }
		skillsDir := filepath.Join(t.TempDir(), "skills")
		writeTrackedSkill(t, skillsDir, "alpha")

		root := cmd.NewRootCmd("test")
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetIn(strings.NewReader("n\n"))
		root.SetArgs([]string{"remove", "-d", skillsDir, "alpha"})

		// Act
		err := root.Execute()

		// Assert
		if err == nil {
			t.Fatal("expected an error when declined")
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "alpha")); err != nil {
			t.Error("alpha should remain")
		}
	})

	t.Run("TTY で回答待ちの間にキャンセルされたとき、削除は失敗してスキルを残す", func(t *testing.T) {
		// Arrange
		cmd.IsTTY = func() bool { return true }
		skillsDir := filepath.Join(t.TempDir(), "skills")
		writeTrackedSkill(t, skillsDir, "alpha")

		stdin, _ := io.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		root := cmd.NewRootCmd("test")
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetIn(stdin)
		root.SetArgs([]string{"remove", "-d", skillsDir, "alpha"})
		cancel()

		// Act
		err := root.ExecuteContext(ctx)

		// Assert
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "alpha")); err != nil {
			t.Error("alpha should remain")
		}
	})

	t.Run("TTY でユーザーが承諾したとき、削除はスキルを削除する", func(t *testing.T) {
		// Arrange
		cmd.IsTTY = func() bool { return true }
		skillsDir := filepath.Join(t.TempDir(), "skills")
		writeTrackedSkill(t, skillsDir, "alpha")

		root := cmd.NewRootCmd("test")
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetIn(strings.NewReader("y\n"))
		root.SetArgs([]string{"remove", "-d", skillsDir, "alpha"})

		// Act
		err := root.Execute()
		// Assert
		if err != nil {
			t.Fatalf("remove: %v", err)
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "alpha")); !errors.Is(err, os.ErrNotExist) {
			t.Error("alpha should be removed")
		}
	})
}

func writeTrackedSkill(t *testing.T, skillsDir, name string) {
	t.Helper()

	content, err := skill.InjectSourceMetadata("# "+name+"\n", skill.SourceMetadata{
		RepoURL: "https://github.com/h3y6e/spec-skills",
		Ref:     "refs/tags/v1.0.0",
		TreeSHA: "abc123",
		Path:    "skills/" + name,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(skillsDir, name), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(skillsDir, name, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveGlob(t *testing.T) {
	t.Parallel()

	t.Run("glob がインストール済みのスキルに一致したとき、削除は一致したスキルだけを削除する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		bareURL := testutil.BareRepo(t, map[string]string{
			"skills/spec-plan/SKILL.md":   "# Plan\n",
			"skills/spec-review/SKILL.md": "# Review\n",
			"skills/beta/SKILL.md":        "# Beta\n",
		})
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		addSkills(t, "-d", skillsDir, bareURL)

		// Act
		out, err := runSkills(t, "remove", "-y", "-d", skillsDir, "spec-*")
		// Assert
		if err != nil {
			t.Fatalf("remove error: %v\n%s", err, out)
		}

		entries, _ := os.ReadDir(skillsDir)
		if len(entries) != 1 || entries[0].Name() != "beta" {
			t.Errorf("left %v, want only beta", entries)
		}
	})

	t.Run("パターンが不正なとき、削除は使い方の誤りとして失敗する", func(t *testing.T) {
		t.Parallel()
		// Act
		_, err := runSkills(t, "remove", "-y", "-d", t.TempDir(), "spec-[")

		// Assert
		if !strings.Contains(err.Error(), "syntax error in pattern") {
			t.Errorf("remove error = %v, want a pattern syntax error", err)
		}
	})
}
