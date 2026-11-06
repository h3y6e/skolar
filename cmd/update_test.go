package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/h3y6e/skolar/cmd"
	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/testutil"
)

func updatedNames(t *testing.T, args ...string) []string {
	t.Helper()

	var out bytes.Buffer

	root := cmd.NewRootCmd("test")
	root.SetOut(&out)
	root.SetArgs(args)

	if err := root.Execute(); err != nil {
		t.Fatalf("%v: %v", args, err)
	}

	var names []string

	for _, r := range decodeRecord(t, out.Bytes(), "results") {
		name, _ := r["skillName"].(string)
		names = append(names, name)
	}

	return names
}

func TestUpdateFlags(t *testing.T) {
	t.Parallel()

	t.Run("--skill フラグが登録されている", func(t *testing.T) {
		t.Parallel()

		root := cmd.NewRootCmd("test")
		updateCmd, _, _ := root.Find([]string{"update"})

		flag := updateCmd.Flags().Lookup("skill")
		if flag == nil {
			t.Fatal("expected --skill flag on update command")
		}
	})

	t.Run("-s の短縮形が使える", func(t *testing.T) {
		t.Parallel()

		root := cmd.NewRootCmd("test")
		updateCmd, _, _ := root.Find([]string{"update"})

		flag := updateCmd.Flags().ShorthandLookup("s")
		if flag == nil {
			t.Fatal("expected -s shorthand on update command")
		}
	})

	t.Run("--dir フラグが登録されている", func(t *testing.T) {
		t.Parallel()

		root := cmd.NewRootCmd("test")
		updateCmd, _, _ := root.Find([]string{"update"})

		flag := updateCmd.Flags().Lookup("dir")
		if flag == nil {
			t.Fatal("expected --dir flag on update command")
		}
	})

	t.Run("--yes フラグが登録されている", func(t *testing.T) {
		t.Parallel()

		root := cmd.NewRootCmd("test")
		updateCmd, _, _ := root.Find([]string{"update"})

		flag := updateCmd.Flags().Lookup("yes")
		if flag == nil {
			t.Fatal("expected --yes flag on update command")
		}
	})
}

func TestUpdateSkillFilter(t *testing.T) {
	t.Parallel()

	bareURL := testutil.BareRepo(t, map[string]string{
		"skills/alpha/SKILL.md": "# Alpha\n",
		"skills/beta/SKILL.md":  "# Beta\n",
	})

	t.Run("--skill を指定したとき、指定したスキルだけが出力される", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		root1 := cmd.NewRootCmd("test")
		root1.SetArgs([]string{"add", "-d", skillsDir, bareURL})

		if err := root1.Execute(); err != nil {
			t.Fatalf("add error: %v", err)
		}

		// Act
		got := updatedNames(t, "update", "-s", "alpha", "--dry-run", "--json", "-d", skillsDir)

		// Assert
		if !slices.Equal(got, []string{"alpha"}) {
			t.Errorf("results = %v, want only alpha", got)
		}
	})

	t.Run("ディレクトリを選択したとき、そのディレクトリのスキルだけを更新する", func(t *testing.T) {
		t.Parallel()

		rootDir := t.TempDir()
		dirA := filepath.Join(rootDir, ".agents", "skills")
		bareURL := testutil.BareRepo(t, map[string]string{
			"skills/alpha/SKILL.md": "# Alpha v2\n",
			"skills/beta/SKILL.md":  "# Beta v2\n",
		})

		if err := os.MkdirAll(filepath.Join(dirA, "alpha"), 0o755); err != nil {
			t.Fatalf("mkdir alpha: %v", err)
		}

		if err := os.WriteFile(filepath.Join(dirA, "alpha", "SKILL.md"), []byte("# Alpha v1\n"), 0o644); err != nil {
			t.Fatalf("write alpha: %v", err)
		}

		lockData := []byte(`{
		  "version": 1,
		  "skills": {
		    "alpha": {
		      "source": ` + strconv.Quote(bareURL) + `,
		      "sourceType": "git",
		      "computedHash": "stale-alpha"
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
		root.SetArgs([]string{"update", "-y", "-d", dirA})

		if err := root.Execute(); err != nil {
			t.Fatalf("update error: %v", err)
		}

		got := out.String()
		if !strings.Contains(got, "  M  alpha  +1 -1 (1 ファイル)\n  A  1 件が未インストール\n1 件を更新\n") {
			t.Errorf("expected alpha updated and beta listed as not installed, got: %q", got)
		}

		if _, err := os.Stat(filepath.Join(dirA, "beta")); !os.IsNotExist(err) {
			t.Errorf("beta was installed into %q: %v", dirA, err)
		}

		data, err := os.ReadFile(filepath.Join(dirA, "alpha", "SKILL.md"))
		if err != nil {
			t.Fatalf("read alpha after update: %v", err)
		}

		if string(data) != "# Alpha v2\n" {
			t.Errorf("alpha content = %q, want %q", string(data), "# Alpha v2\n")
		}
	})

	t.Run("--skill を指定しないとき、すべてのスキルを表示する", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		root1 := cmd.NewRootCmd("test")
		root1.SetArgs([]string{"add", "-d", skillsDir, bareURL})

		if err := root1.Execute(); err != nil {
			t.Fatalf("add error: %v", err)
		}

		// Act
		got := updatedNames(t, "update", "--dry-run", "--json", "-d", skillsDir)

		// Assert
		if !slices.Equal(got, []string{"alpha", "beta"}) {
			t.Errorf("results = %v, want alpha and beta", got)
		}
	})

	t.Run("ロックファイルがないとき、更新対象なしと報告する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		// Act
		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetErr(&out)
		root.SetArgs([]string{"update", "-y", "-d", skillsDir})
		err := root.Execute()
		// Assert
		if err != nil {
			t.Fatalf("update: %v", err)
		}

		if !strings.Contains(out.String(), "更新するインストール済みのスキルがありません") {
			t.Errorf("output = %q, want %q", out.String(), "更新するインストール済みのスキルがありません")
		}
	})

	t.Run("ロックファイルのハッシュが上流と異なるとき、更新ありと判定する", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		root1 := cmd.NewRootCmd("test")
		root1.SetArgs([]string{"add", "-d", skillsDir, bareURL})

		if err := root1.Execute(); err != nil {
			t.Fatalf("add error: %v", err)
		}

		lockPath := lock.FilePath(skillsDir)

		lf, err := lock.ReadFile(lockPath)
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}

		entry := lf.Skills["alpha"]
		entry.ComputedHash = "stale-hash"

		lf.Skills["alpha"] = entry
		if err := lock.WriteFile(lockPath, lf); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		var out bytes.Buffer

		root2 := cmd.NewRootCmd("test")
		root2.SetOut(&out)
		root2.SetArgs([]string{"update", "-s", "alpha", "-y", "-d", skillsDir})

		if err := root2.Execute(); err != nil {
			t.Fatalf("update error: %v", err)
		}

		got := out.String()
		if !strings.Contains(got, "件を更新") {
			t.Errorf("expected 'updated' in output (stale hash should trigger update), got: %q", got)
		}
	})

	t.Run("ソース引数を指定したとき、そのソースのエントリだけに絞り込まれる", func(t *testing.T) {
		t.Parallel()

		bareURL2 := testutil.BareRepo(t, map[string]string{
			"skills/gamma/SKILL.md": "# Gamma\n",
		})

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		root1 := cmd.NewRootCmd("test")
		root1.SetArgs([]string{"add", "-d", skillsDir, bareURL})

		if err := root1.Execute(); err != nil {
			t.Fatalf("add 1 error: %v", err)
		}

		root2 := cmd.NewRootCmd("test")
		root2.SetArgs([]string{"add", "-d", skillsDir, bareURL2})

		if err := root2.Execute(); err != nil {
			t.Fatalf("add 2 error: %v", err)
		}

		var out bytes.Buffer

		root3 := cmd.NewRootCmd("test")
		root3.SetOut(&out)
		root3.SetArgs([]string{"update", "-y", "-d", skillsDir, bareURL2})

		if err := root3.Execute(); err != nil {
			t.Fatalf("update error: %v", err)
		}

		got := out.String()
		if strings.Contains(got, "alpha") {
			t.Errorf("output should not mention alpha when source is second repo, got: %q", got)
		}

		if strings.Contains(got, "beta") {
			t.Errorf("output should not mention beta when source is second repo, got: %q", got)
		}
	})

	t.Run("ref 付きのソース引数を指定したとき、一致する ref だけに絞り込まれる", func(t *testing.T) {
		t.Parallel()

		bareURLWithRefs := initBareRepoWithSplitRefSkills(t)
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		testutil.WriteFiles(t, skillsDir, map[string]string{"alpha/SKILL.md": "# Alpha old\n"})
		testutil.WriteFiles(t, skillsDir, map[string]string{"beta/SKILL.md": "# Beta old\n"})

		lockPath := lock.FilePath(skillsDir)

		lf := lock.File{
			Version: 1,
			Skills: map[string]lock.Entry{
				"alpha": {
					Source:       bareURLWithRefs,
					SourceType:   "git",
					Ref:          "main",
					ComputedHash: "stale-alpha",
				},
				"beta": {
					Source:       bareURLWithRefs,
					SourceType:   "git",
					Ref:          "feature/install",
					ComputedHash: "stale-beta",
				},
			},
		}
		if err := lock.WriteFile(lockPath, lf); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"update", "-y", "-d", skillsDir, bareURLWithRefs + "#feature/install"})

		if err := root.Execute(); err != nil {
			t.Fatalf("update error: %v", err)
		}

		got := out.String()
		if strings.Contains(got, "alpha") {
			t.Errorf("output should not mention alpha when ref is feature/install, got: %q", got)
		}

		if !strings.Contains(got, "M  beta") || !strings.HasSuffix(got, "1 件を更新\n") {
			t.Errorf("expected beta to be updated, got: %q", got)
		}

		alphaData, err := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md"))
		if err != nil {
			t.Fatalf("read alpha: %v", err)
		}

		if string(alphaData) != "# Alpha old\n" {
			t.Errorf("alpha = %q, want %q", string(alphaData), "# Alpha old\n")
		}

		betaData, err := os.ReadFile(filepath.Join(skillsDir, "beta", "SKILL.md"))
		if err != nil {
			t.Fatalf("read beta: %v", err)
		}

		if string(betaData) != "# Beta feature\n" {
			t.Errorf("beta = %q, want %q", string(betaData), "# Beta feature\n")
		}

		updatedLF, err := lock.ReadFile(lockPath)
		if err != nil {
			t.Fatalf("read updated lockfile: %v", err)
		}

		if updatedLF.Skills["alpha"].ComputedHash != "stale-alpha" {
			t.Errorf("alpha hash = %q, want %q", updatedLF.Skills["alpha"].ComputedHash, "stale-alpha")
		}

		if updatedLF.Skills["beta"].ComputedHash == "stale-beta" {
			t.Error("beta hash should be refreshed")
		}
	})
}

func TestUpdateNoChanges(t *testing.T) {
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
	root2.SetArgs([]string{"update", "-y", "-d", skillsDir})

	if err := root2.Execute(); err != nil {
		t.Fatalf("update error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "最新") {
		t.Errorf("expected '最新' in output, got: %q", got)
	}

	if _, err := os.Stat(filepath.Join(skillsDir, "alpha", "SKILL.md")); err != nil {
		t.Errorf("alpha/SKILL.md should still exist: %v", err)
	}
}

func TestUpdateApply(t *testing.T) {
	bareURL := testutil.BareRepo(t, map[string]string{
		"skills/alpha/SKILL.md": "# Alpha v2\n",
		"skills/beta/SKILL.md":  "# Beta\n",
	})

	t.Run("--yes を指定したとき、更新を適用してロックファイルを更新する", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		testutil.WriteFiles(t, skillsDir, map[string]string{"alpha/SKILL.md": "# Alpha v1\n"})
		testutil.WriteFiles(t, skillsDir, map[string]string{"beta/SKILL.md": "# Beta\n"})

		lf := lock.File{
			Version: 1,
			Skills: map[string]lock.Entry{
				"alpha": {Source: bareURL, SourceType: "git", ComputedHash: "stale-hash-alpha"},
				"beta":  {Source: bareURL, SourceType: "git", ComputedHash: "stale-hash-beta"},
			},
		}

		lockPath := lock.FilePath(skillsDir)
		if err := lock.WriteFile(lockPath, lf); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"update", "-y", "-d", skillsDir})

		if err := root.Execute(); err != nil {
			t.Fatalf("update error: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md"))
		if err != nil {
			t.Fatalf("read alpha SKILL.md: %v", err)
		}

		if string(data) != "# Alpha v2\n" {
			t.Errorf("alpha content = %q, want %q", string(data), "# Alpha v2\n")
		}

		updatedLF, err := lock.ReadFile(lockPath)
		if err != nil {
			t.Fatalf("read updated lockfile: %v", err)
		}

		alphaEntry := updatedLF.Skills["alpha"]
		if alphaEntry.ComputedHash == "stale-hash-alpha" {
			t.Error("alpha hash should have been updated from stale value")
		}

		if alphaEntry.ComputedHash == "" {
			t.Error("alpha hash should not be empty after update")
		}
	})

	t.Run("非対話モードのとき、適用せずにプレビューを表示する", func(t *testing.T) {
		origIsTTY := cmd.IsTTY
		cmd.IsTTY = func() bool { return false }

		t.Cleanup(func() { cmd.IsTTY = origIsTTY })

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		testutil.WriteFiles(t, skillsDir, map[string]string{"alpha/SKILL.md": "# Alpha v1\n"})

		lf := lock.File{
			Version: 1,
			Skills: map[string]lock.Entry{
				"alpha": {Source: bareURL, SourceType: "git", ComputedHash: "stale-hash"},
			},
		}

		lockPath := lock.FilePath(skillsDir)
		if err := lock.WriteFile(lockPath, lf); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"update", "-d", skillsDir})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error in non-interactive mode without -y")
		}

		data, err2 := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md"))
		if err2 != nil {
			t.Fatalf("read alpha: %v", err2)
		}

		if string(data) != "# Alpha v1\n" {
			t.Errorf("alpha should not have been updated, got: %q", string(data))
		}

		updatedLF, err2 := lock.ReadFile(lockPath)
		if err2 != nil {
			t.Fatalf("read lockfile: %v", err2)
		}

		if updatedLF.Skills["alpha"].ComputedHash != "stale-hash" {
			t.Error("lockfile should not have been modified in non-interactive mode")
		}
	})

	t.Run("--yes とスキルの絞り込みを指定したとき、絞り込んだスキルだけを更新する", func(t *testing.T) {
		t.Parallel()

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		testutil.WriteFiles(t, skillsDir, map[string]string{"alpha/SKILL.md": "# Alpha v1\n"})
		testutil.WriteFiles(t, skillsDir, map[string]string{"beta/SKILL.md": "# Beta old\n"})

		lf := lock.File{
			Version: 1,
			Skills: map[string]lock.Entry{
				"alpha": {Source: bareURL, SourceType: "git", ComputedHash: "stale-alpha"},
				"beta":  {Source: bareURL, SourceType: "git", ComputedHash: "stale-beta"},
			},
		}

		lockPath := lock.FilePath(skillsDir)
		if err := lock.WriteFile(lockPath, lf); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		root := cmd.NewRootCmd("test")
		root.SetArgs([]string{"update", "-s", "alpha", "-y", "-d", skillsDir})

		if err := root.Execute(); err != nil {
			t.Fatalf("update error: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md"))
		if err != nil {
			t.Fatalf("read alpha: %v", err)
		}

		if string(data) != "# Alpha v2\n" {
			t.Errorf("alpha = %q, want %q", string(data), "# Alpha v2\n")
		}

		data, err = os.ReadFile(filepath.Join(skillsDir, "beta", "SKILL.md"))
		if err != nil {
			t.Fatalf("read beta: %v", err)
		}

		if string(data) != "# Beta old\n" {
			t.Errorf("beta = %q, want %q (should not be updated)", string(data), "# Beta old\n")
		}

		updatedLF, err := lock.ReadFile(lockPath)
		if err != nil {
			t.Fatalf("read lockfile: %v", err)
		}

		if updatedLF.Skills["alpha"].ComputedHash == "stale-alpha" {
			t.Error("alpha hash should be refreshed")
		}

		if updatedLF.Skills["beta"].ComputedHash != "stale-beta" {
			t.Error("beta hash should still be stale")
		}
	})
}

func TestUpdateIntegration(t *testing.T) {
	t.Run("最新のスキルがあるとき、更新しても変更されない", func(t *testing.T) {
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

		alphaPath := filepath.Join(skillsDir, "alpha", "SKILL.md")

		infoBefore, err := os.Stat(alphaPath)
		if err != nil {
			t.Fatalf("stat alpha: %v", err)
		}

		var out bytes.Buffer

		root2 := cmd.NewRootCmd("test")
		root2.SetOut(&out)
		root2.SetArgs([]string{"update", "-y", "-d", skillsDir})

		if err := root2.Execute(); err != nil {
			t.Fatalf("update error: %v", err)
		}

		got := out.String()
		if !strings.Contains(got, "最新") {
			t.Errorf("expected '最新' in output, got: %q", got)
		}

		if strings.Contains(got, "件を更新") {
			t.Errorf("no skill should have been updated, got: %q", got)
		}

		infoAfter, err := os.Stat(alphaPath)
		if err != nil {
			t.Fatalf("stat alpha after: %v", err)
		}

		if infoBefore.ModTime() != infoAfter.ModTime() {
			t.Error("alpha should not have been modified when up-to-date")
		}
	})

	t.Run("スキルが上流で削除されたとき、更新は残して remove を案内する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		bareURL := testutil.BareRepo(t, map[string]string{"skills/beta/SKILL.md": "# Beta v2\n"})
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		testutil.WriteFiles(t, skillsDir, map[string]string{"alpha/SKILL.md": "# Alpha\n", "beta/SKILL.md": "# Beta\n"})

		lf := lock.File{
			Version: 1,
			Skills: map[string]lock.Entry{
				"alpha": {Source: bareURL, SourceType: "git", SkillPath: "skills/alpha/SKILL.md", ComputedHash: "some-hash"},
				"beta":  {Source: bareURL, SourceType: "git", SkillPath: "skills/beta/SKILL.md", ComputedHash: "some-hash"},
			},
		}
		if err := lock.WriteFile(lock.FilePath(skillsDir), lf); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		var out, errOut bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{"update", "-y", "-d", skillsDir})

		// Act
		err := root.Execute()
		// Assert
		if err != nil {
			t.Fatalf("update: %v", err)
		}

		if want := "  D  alpha  上流で削除\n  M  beta   +1 -1 (1 ファイル)\n1 件を更新\n"; !strings.HasSuffix(out.String(), want) {
			t.Errorf("stdout = %q, want suffix %q", out.String(), want)
		}

		if !strings.Contains(errOut.String(), "skolar remove alpha --dir "+skillsDir) {
			t.Errorf("stderr = %q, want a skolar remove hint for alpha", errOut.String())
		}

		if _, err := os.Stat(filepath.Join(skillsDir, "alpha", "SKILL.md")); err != nil {
			t.Errorf("alpha was not kept: %v", err)
		}
	})

	t.Run("ソースに未インストールのスキルがあるとき、ディレクトリが既にあるものは数えずヒントは ref を保つ", func(t *testing.T) {
		t.Parallel()

		// Arrange
		bareURL := testutil.BareRepo(t, map[string]string{
			"skills/alpha/SKILL.md": "# Alpha\n",
			"skills/beta/SKILL.md":  "# Beta\n",
			"skills/gamma/SKILL.md": "# Gamma\n",
		})
		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
		testutil.WriteFiles(t, skillsDir, map[string]string{"alpha/SKILL.md": "# Alpha\n", "beta/SKILL.md": "# Copied\n"})

		lf := lock.File{Version: 1, Skills: map[string]lock.Entry{
			"alpha": {Source: bareURL, Ref: "main", SourceType: "git", SkillPath: "skills/alpha/SKILL.md", ComputedHash: "stale"},
		}}
		if err := lock.WriteFile(lock.FilePath(skillsDir), lf); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		var out, errOut bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{"update", "--dry-run", "-d", skillsDir})

		// Act
		err := root.Execute()
		// Assert
		if err != nil {
			t.Fatalf("update: %v", err)
		}

		if !strings.HasSuffix(out.String(), "  A  1 件が未インストール\n") {
			t.Errorf("stdout = %q, want only gamma counted as not installed", out.String())
		}

		if !strings.Contains(errOut.String(), "skolar add "+bareURL+"#main -s <skill>") {
			t.Errorf("stderr = %q, want an add hint with the ref", errOut.String())
		}
	})

	t.Run("非対話モードのとき、ファイルを変更せずにすべての候補を一覧表示する", func(t *testing.T) {
		origIsTTY := cmd.IsTTY
		cmd.IsTTY = func() bool { return false }

		t.Cleanup(func() { cmd.IsTTY = origIsTTY })

		bareURL := testutil.BareRepo(t, map[string]string{
			"skills/alpha/SKILL.md": "# Alpha v2\n",
			"skills/beta/SKILL.md":  "# Beta v2\n",
		})

		skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")

		testutil.WriteFiles(t, skillsDir, map[string]string{"alpha/SKILL.md": "# Alpha v1\n"})
		testutil.WriteFiles(t, skillsDir, map[string]string{"beta/SKILL.md": "# Beta v1\n"})

		lf := lock.File{
			Version: 1,
			Skills: map[string]lock.Entry{
				"alpha": {Source: bareURL, SourceType: "git", ComputedHash: "stale-a"},
				"beta":  {Source: bareURL, SourceType: "git", ComputedHash: "stale-b"},
			},
		}

		lockPath := lock.FilePath(skillsDir)
		if err := lock.WriteFile(lockPath, lf); err != nil {
			t.Fatalf("write lockfile: %v", err)
		}

		var out bytes.Buffer

		root := cmd.NewRootCmd("test")
		root.SetOut(&out)
		root.SetArgs([]string{"update", "-d", skillsDir})

		err := root.Execute()
		if err == nil {
			t.Fatal("expected error in non-interactive mode")
		}

		got := out.String()
		if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
			t.Errorf("preview should list all candidates, got: %q", got)
		}

		data, _ := os.ReadFile(filepath.Join(skillsDir, "alpha", "SKILL.md"))
		if string(data) != "# Alpha v1\n" {
			t.Error("alpha should not have been modified in preview mode")
		}

		data, _ = os.ReadFile(filepath.Join(skillsDir, "beta", "SKILL.md"))
		if string(data) != "# Beta v1\n" {
			t.Error("beta should not have been modified in preview mode")
		}

		updatedLF, err := lock.ReadFile(lockPath)
		if err != nil {
			t.Fatal(err)
		}

		if updatedLF.Skills["alpha"].ComputedHash != "stale-a" {
			t.Error("alpha hash should be unchanged in preview mode")
		}
	})
}
