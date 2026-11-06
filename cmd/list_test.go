package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/h3y6e/skolar/cmd"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/testutil"
)

func TestListFlags(t *testing.T) {
	t.Parallel()

	root := cmd.NewRootCmd("test")
	listCmd, _, _ := root.Find([]string{"list"})

	t.Run("--json フラグが登録されている", func(t *testing.T) {
		t.Parallel()

		flag := listCmd.Flags().Lookup("json")
		if flag == nil {
			t.Fatal("expected --json flag on list command")
		}
	})

	t.Run("カスタムディレクトリのとき、一覧表示はロックファイルのスキルを表示しない", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()
		dirA := filepath.Join(rootDir, ".agents", "skills")

		lockData := []byte(`{
		  "version": 1,
		  "skills": {
		    "alpha": {
		      "source": "h3y6e/spec-skills",
		      "sourceType": "github",
		      "computedHash": "abc123"
		    },
		    "beta": {
		      "source": "h3y6e/spec-skills",
		      "sourceType": "github",
		      "computedHash": "def456"
		    }
		  }
		}`)

		if err := os.MkdirAll(filepath.Dir(lock.FilePath(dirA)), 0o755); err != nil {
			t.Fatalf("mkdir lockfile parent: %v", err)
		}

		if err := os.WriteFile(lock.FilePath(dirA), lockData, 0o644); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"list", "-d", filepath.Join(rootDir, "custom", "skills")})

		if err := root.Execute(); err != nil {
			t.Fatalf("list error: %v", err)
		}

		got := out.String()
		if strings.Contains(got, "alpha") || strings.Contains(got, "beta") {
			t.Errorf("did not expect skills of another directory in output for %q, got: %q", dirA, got)
		}
	})
}

func TestListIgnoresUnrelatedLockfile(t *testing.T) {
	t.Parallel()
	t.Run("カスタムディレクトリのとき、一覧表示は不正な skills-lock.json を無視する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		base := t.TempDir()
		if err := os.WriteFile(filepath.Join(base, "skills-lock.json"), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}

		root := cmd.NewRootCmd("test")
		root.SetOut(&bytes.Buffer{})
		root.SetArgs([]string{"list", "-d", filepath.Join(base, "custom", "skills")})

		// Act
		err := root.Execute()
		// Assert
		if err != nil {
			t.Fatalf("list: %v", err)
		}
	})
}

func TestListAlias(t *testing.T) {
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
	root2.SetArgs([]string{"ls", "-d", skillsDir})

	if err := root2.Execute(); err != nil {
		t.Fatalf("ls error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "alpha") {
		t.Errorf("ls should list alpha, got: %q", got)
	}
}

func TestListHuman(t *testing.T) {
	t.Parallel()

	t.Run("スキルがインストール済みのとき、一覧表示する", func(t *testing.T) {
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

		var out bytes.Buffer

		root2 := cmd.NewRootCmd("test")
		root2.SetOut(&out)
		root2.SetArgs([]string{"list", "-d", skillsDir})

		if err := root2.Execute(); err != nil {
			t.Fatalf("list error: %v", err)
		}

		got := out.String()
		if !strings.Contains(got, "alpha") {
			t.Errorf("expected alpha in output, got: %q", got)
		}

		if !strings.Contains(got, "beta") {
			t.Errorf("expected beta in output, got: %q", got)
		}
	})

	t.Run("ロックファイルが空のとき、スキルなしのメッセージを表示する", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		lf := lock.File{Version: 1, Skills: map[string]lock.Entry{}}
		if err := lock.WriteFile(lock.FilePath(skillsDir), lf); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetErr(&out)
		root.SetArgs([]string{"list", "-d", skillsDir})

		if err := root.Execute(); err != nil {
			t.Fatalf("list error: %v", err)
		}

		got := out.String()
		if !strings.Contains(got, "インストール済みのスキルがありません") {
			t.Errorf("expected 'no ...' message for empty lockfile, got: %q", got)
		}
	})

	t.Run("ロックファイルがないとき、インストール済みのスキルなしと報告する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		// Act
		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetErr(&out)
		root.SetArgs([]string{"list", "-d", skillsDir})
		err := root.Execute()
		// Assert
		if err != nil {
			t.Fatalf("list: %v", err)
		}

		if !strings.Contains(out.String(), "インストール済みのスキルがありません") {
			t.Errorf("output = %q, want %q", out.String(), "インストール済みのスキルがありません")
		}
	})
}

func TestListJSON(t *testing.T) {
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
	root2.SetArgs([]string{"list", "--json", "-d", skillsDir})

	if err := root2.Execute(); err != nil {
		t.Fatalf("list --json error: %v", err)
	}

	results := decodeRecord(t, out.Bytes(), "skills")

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r["skillName"] != "alpha" {
		t.Errorf("skillName = %v, want alpha", r["skillName"])
	}

	for _, field := range []string{"skillName", "source", "sourceType", "computedHash"} {
		if _, ok := r[field]; !ok {
			t.Errorf("missing required JSON field %q", field)
		}
	}
}

func TestListJSONEmpty(t *testing.T) {
	t.Parallel()

	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

	lf := lock.File{Version: 1, Skills: map[string]lock.Entry{}}
	if err := lock.WriteFile(lock.FilePath(skillsDir), lf); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	root := cmd.NewRootCmd("test")
	root.SetOut(&out)
	root.SetArgs([]string{"list", "--json", "-d", skillsDir})

	if err := root.Execute(); err != nil {
		t.Fatalf("list --json error: %v", err)
	}

	results := decodeRecord(t, out.Bytes(), "skills")

	if len(results) != 0 {
		t.Errorf("expected empty array, got %d items", len(results))
	}
}

func TestListJSONIncludesRef(t *testing.T) {
	t.Parallel()

	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

	lf := lock.File{
		Version: 1,
		Skills: map[string]lock.Entry{
			"alpha": {
				Source:       "h3y6e/spec-skills",
				Ref:          "feature/install",
				SourceType:   "github",
				ComputedHash: "abc123",
			},
		},
	}
	if err := lock.WriteFile(lock.FilePath(skillsDir), lf); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}

	var out bytes.Buffer

	root := cmd.NewRootCmd("test")
	root.SetOut(&out)
	root.SetArgs([]string{"list", "--json", "-d", skillsDir})

	if err := root.Execute(); err != nil {
		t.Fatalf("list --json error: %v", err)
	}

	results := decodeRecord(t, out.Bytes(), "skills")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0]["ref"] != "feature/install" {
		t.Errorf("ref = %v, want %q", results[0]["ref"], "feature/install")
	}
}
