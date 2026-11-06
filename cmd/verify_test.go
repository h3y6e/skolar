package cmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/h3y6e/skolar/cmd"
	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
	"github.com/h3y6e/skolar/internal/testutil"
)

type verifyRecord struct {
	Results []struct {
		SkillName string   `json:"skillName"`
		Status    string   `json:"status"`
		Files     []string `json:"files"`
		Reason    string   `json:"reason"`
	} `json:"results"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func runVerifyJSON(t *testing.T, dir string) (verifyRecord, error) {
	t.Helper()

	var out bytes.Buffer

	root := cmd.NewRootCmd("test")
	root.SetOut(&out)
	root.SetArgs([]string{"verify", "--json", "-d", dir})
	err := root.Execute()

	var rec verifyRecord
	if jsonErr := json.Unmarshal(out.Bytes(), &rec); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out.String())
	}

	return rec, err
}

func addSkills(t *testing.T, args ...string) {
	t.Helper()

	root := cmd.NewRootCmd("test")
	root.SetArgs(append([]string{"add", "-y"}, args...))

	if err := root.Execute(); err != nil {
		t.Fatalf("add %v: %v", args, err)
	}
}

func TestVerifyLockfile(t *testing.T) {
	t.Parallel()
	bareURL := testutil.BareRepo(t, map[string]string{"skills/alpha/SKILL.md": "# Alpha\n"})

	t.Run("ロックファイルのスキルが未変更のとき、検証は検証済みと報告する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		dir := filepath.Join(t.TempDir(), ".agents", "skills")
		addSkills(t, "-d", dir, bareURL)

		// Act
		rec, err := runVerifyJSON(t, dir)
		// Assert
		if err != nil {
			t.Fatalf("verify: %v", err)
		}

		if len(rec.Results) != 1 || rec.Results[0].Status != "verified" {
			t.Errorf("results = %+v, want alpha verified", rec.Results)
		}
	})

	t.Run("ロックファイルのスキルのファイルが変更されたとき、検証は変更ありと報告し verify_failed で失敗する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		dir := filepath.Join(t.TempDir(), ".agents", "skills")
		addSkills(t, "-d", dir, bareURL)

		if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("# Tampered\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Act
		rec, err := runVerifyJSON(t, dir)

		// Assert
		if err == nil || rec.Error.Code != "verify_failed" {
			t.Fatalf("verify error = %v, code %q, want verify_failed", err, rec.Error.Code)
		}

		if len(rec.Results) != 1 || rec.Results[0].Status != "modified" {
			t.Errorf("results = %+v, want alpha modified", rec.Results)
		}
	})
}

func TestVerifyGhMetadata(t *testing.T) {
	bareURL := initBareRepoWithTwoTags(t)
	stubGitHubTransport(t, "h3y6e/fixture", bareURL)

	t.Run("gh 形式のスキルが未変更のとき、検証は検証済みと報告する", func(t *testing.T) {
		// Arrange
		dir := filepath.Join(t.TempDir(), "skills")
		addSkills(t, "-s", "alpha", "-d", dir, "h3y6e/fixture")

		// Act
		rec, err := runVerifyJSON(t, dir)
		// Assert
		if err != nil {
			t.Fatalf("verify: %v", err)
		}

		if len(rec.Results) != 1 || rec.Results[0].Status != "verified" {
			t.Errorf("results = %+v, want alpha verified", rec.Results)
		}
	})

	t.Run("gh 形式のスキルでファイルが追加・変更されたとき、検証は変更ありとして列挙する", func(t *testing.T) {
		// Arrange
		dir := filepath.Join(t.TempDir(), "skills")
		addSkills(t, "-s", "alpha", "-d", dir, "h3y6e/fixture")
		skillMD := filepath.Join(dir, "alpha", "SKILL.md")

		data, err := os.ReadFile(skillMD)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(skillMD, []byte(strings.Replace(string(data), "Alpha v2", "Alpha v9", 1)), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, "alpha", "extra.md"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Act
		rec, err := runVerifyJSON(t, dir)

		// Assert
		if err == nil {
			t.Fatal("verify should fail")
		}

		if len(rec.Results) != 1 || rec.Results[0].Status != "modified" || !slices.Equal(rec.Results[0].Files, []string{"SKILL.md", "extra.md"}) {
			t.Errorf("results = %+v, want alpha modified in SKILL.md and extra.md", rec.Results)
		}
	})

	t.Run("記録されたツリーが ref と一致しなくなったとき、検証はスキルを検証不能と報告する", func(t *testing.T) {
		// Arrange
		dir := filepath.Join(t.TempDir(), "skills")
		addSkills(t, "-s", "alpha", "-d", dir, "h3y6e/fixture")
		skillMD := filepath.Join(dir, "alpha", "SKILL.md")

		data, err := os.ReadFile(skillMD)
		if err != nil {
			t.Fatal(err)
		}

		tree := metadataLine(t, string(data), "github-tree-sha")
		if err := os.WriteFile(skillMD, []byte(strings.Replace(string(data), tree, "0000000000000000000000000000000000000000", 1)), 0o644); err != nil {
			t.Fatal(err)
		}

		// Act
		rec, err := runVerifyJSON(t, dir)

		// Assert
		if err == nil {
			t.Fatal("verify should fail")
		}

		if len(rec.Results) != 1 || rec.Results[0].Status != "unverifiable" {
			t.Errorf("results = %+v, want alpha unverifiable", rec.Results)
		}
	})
}

func metadataLine(t *testing.T, skillMD, key string) string {
	t.Helper()

	for line := range strings.Lines(skillMD) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+":"); ok {
			return strings.TrimSpace(v)
		}
	}

	t.Fatalf("no %s in:\n%s", key, skillMD)

	return ""
}

func TestVerifyCoverage(t *testing.T) {
	t.Parallel()

	t.Run("スキルのディレクトリのフロントマターが不正でロックファイルにもエントリがないとき、検証は検証不能と報告して失敗する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		dir := filepath.Join(t.TempDir(), "skills")
		if err := os.MkdirAll(filepath.Join(dir, "broken"), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(dir, "broken", "SKILL.md"), []byte("---\nmetadata: [\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Act
		rec, err := runVerifyJSON(t, dir)

		// Assert
		if err == nil || rec.Error.Code != "verify_failed" {
			t.Fatalf("verify error = %v, code %q, want verify_failed", err, rec.Error.Code)
		}

		if len(rec.Results) != 1 || rec.Results[0].SkillName != "broken" || rec.Results[0].Status != "unverifiable" {
			t.Errorf("results = %+v, want broken unverifiable", rec.Results)
		}
	})
}

func TestVerifySkillFilter(t *testing.T) {
	t.Run("-s でロックファイルのスキルを選んだとき、検証は gh 形式のスキルを取得しない", func(t *testing.T) {
		// Arrange
		bareURL := initBareRepoWithTwoTags(t)
		stubGitHubTransport(t, "h3y6e/fixture", bareURL)
		dir := filepath.Join(t.TempDir(), ".agents", "skills")
		addSkills(t, "-s", "beta", "-d", dir, "h3y6e/fixture")

		if err := lock.WriteFile(lock.FilePath(dir), lock.File{Version: 1}); err != nil {
			t.Fatal(err)
		}

		addSkills(t, "-s", "alpha", "-d", dir, bareURL)

		orig := cmd.NewUpstreamFuncs
		cmd.NewUpstreamFuncs = func(ctx context.Context, c *git.Cache) skill.UpstreamFuncs {
			fns := orig(ctx, c)
			fns.FetchTree = func(string, string, string) (fs.FS, error) {
				t.Error("verify fetched a skill that -s did not select")
				return nil, errors.New("unexpected fetch")
			}

			return fns
		}

		t.Cleanup(func() { cmd.NewUpstreamFuncs = orig })

		// Act
		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"verify", "-s", "alpha", "-d", dir})
		err := root.Execute()

		// Assert
		if err != nil || out.String() != "alpha: 検証済み\n" {
			t.Errorf("verify = %v, %q, want alpha verified", err, out.String())
		}
	})
}

func TestVerifyFix(t *testing.T) {
	t.Run("固定された gh 形式のスキルが変更されたとき、fix は固定したバージョンで再インストールする", func(t *testing.T) {
		// Arrange
		bareURL := initBareRepoWithTwoTags(t)
		stubGitHubTransport(t, "h3y6e/fixture", bareURL)
		dir := filepath.Join(t.TempDir(), "skills")
		addSkills(t, "-s", "alpha", "-d", dir, "h3y6e/fixture#v1.0.0")

		if err := os.WriteFile(filepath.Join(dir, "alpha", "extra.md"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"verify", "--json", "-d", dir})

		// Act
		_ = root.Execute()

		// Assert
		var rec struct {
			Error struct {
				Fix string `json:"fix"`
			} `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &rec); err != nil {
			t.Fatal(err)
		}

		if want := "skolar add h3y6e/fixture#v1.0.0 -s alpha --dir " + dir + " -y"; rec.Error.Fix != want {
			t.Errorf("fix = %q, want %q", rec.Error.Fix, want)
		}
	})
}

func TestVerifyLockfileExcludedFiles(t *testing.T) {
	t.Parallel()
	bareURL := testutil.BareRepo(t, map[string]string{
		"skills/alpha/SKILL.md":      "# Alpha\n",
		"skills/alpha/metadata.json": "{}\n",
	})

	t.Run("上流に vercel-labs/skills が除外するファイルがあるとき、未変更のインストールは検証に通る", func(t *testing.T) {
		t.Parallel()
		// Arrange
		dir := filepath.Join(t.TempDir(), ".agents", "skills")
		addSkills(t, "-d", dir, bareURL)

		// Act
		rec, err := runVerifyJSON(t, dir)

		// Assert
		if err != nil || len(rec.Results) != 1 || rec.Results[0].Status != "verified" {
			t.Errorf("verify = %v, %+v, want alpha verified", err, rec.Results)
		}
	})

	t.Run("そのようなインストールが変更されたとき、検証は変更されたファイルを列挙する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		dir := filepath.Join(t.TempDir(), ".agents", "skills")
		addSkills(t, "-d", dir, bareURL)

		if err := os.WriteFile(filepath.Join(dir, "alpha", "SKILL.md"), []byte("# Tampered\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// Act
		rec, err := runVerifyJSON(t, dir)

		// Assert
		if err == nil || len(rec.Results) != 1 || rec.Results[0].Status != "modified" || !slices.Equal(rec.Results[0].Files, []string{"SKILL.md"}) {
			t.Errorf("verify = %v, %+v, want alpha modified in SKILL.md", err, rec.Results)
		}
	})
}
