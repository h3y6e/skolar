package cmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/h3y6e/skolar/cmd"
)

func decodeRecord(t *testing.T, data []byte, key string) []map[string]any {
	t.Helper()

	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("invalid JSON record: %v\n%s", err, data)
	}

	var values []map[string]any
	if err := json.Unmarshal(record[key], &values); err != nil {
		t.Fatalf("invalid %q in record: %v\n%s", key, err, data)
	}

	return values
}

func runMain(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()

	c := exec.CommandContext(t.Context(), os.Args[0], args...)

	c.Env = append(append(os.Environ(), "RUN_SKOLAR_MAIN=1"), env...)

	var stdout, stderr bytes.Buffer

	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	if _, ok := errors.AsType[*exec.ExitError](err); err != nil && !ok {
		t.Fatalf("run: %v", err)
	}

	return stdout.String(), stderr.String(), c.ProcessState.ExitCode()
}

func TestExitStatus(t *testing.T) {
	t.Parallel()

	t.Run("不明なコマンドを指定したとき、実行すると stderr に候補を出力して 2 で終了する", func(t *testing.T) {
		t.Parallel()

		// Act
		stdout, stderr, code := runMain(t, nil, "ad")

		// Assert
		if code != 2 {
			t.Errorf("exit = %d, want 2", code)
		}

		if stdout != "" {
			t.Errorf("stdout = %q, want empty", stdout)
		}

		if !strings.Contains(stderr, "もしかして:\n\tadd") || !strings.Contains(stderr, "--help") {
			t.Errorf("stderr = %q, want a suggestion and a --help hint", stderr)
		}
	})

	t.Run("不明なフラグを指定したとき、実行すると 2 で終了する", func(t *testing.T) {
		t.Parallel()

		// Act
		_, stderr, code := runMain(t, nil, "list", "--nope")

		// Assert
		if code != 2 || !strings.Contains(stderr, "unknown flag: --nope") {
			t.Errorf("exit = %d, stderr = %q, want 2 and the unknown flag", code, stderr)
		}
	})

	t.Run("コマンドが失敗したとき、実行すると stderr にエラーを出力して 1 で終了する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skillsDir := filepath.Join(t.TempDir(), "skills")

		// Act
		_, stderr, code := runMain(t, nil, "remove", "-y", "-d", skillsDir, "missing")

		// Assert
		if code != 1 || !strings.Contains(stderr, `error: スキル "missing" が`) || !strings.Contains(stderr, "fix: skolar list --dir "+skillsDir) {
			t.Errorf("exit = %d, stderr = %q, want 1, the cause, and a fix", code, stderr)
		}
	})

	t.Run("TTY も --yes もなく確認が必要なとき、実行すると fix を表示して 2 で終了する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skillsDir := filepath.Join(t.TempDir(), "skills")
		writeTrackedSkill(t, skillsDir, "alpha")

		// Act
		_, stderr, code := runMain(t, nil, "remove", "-d", skillsDir, "alpha")

		// Assert
		if code != 2 || !strings.Contains(stderr, "fix: --yes を付けて再実行してください") {
			t.Errorf("exit = %d, stderr = %q, want 2 and the --yes fix", code, stderr)
		}
	})
}

func TestUsageErrorRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		env  []string
		args []string
		key  string
	}{
		{"--json の後に不明なフラグを指定したとき、一覧表示はエラーレコードを出力する", nil, []string{"list", "--json", "--nope"}, "skills"},
		{"SKOLAR_JSON を設定し引数が余分なとき、更新はエラーレコードを出力する", []string{"SKOLAR_JSON=true"}, []string{"update", "a", "b"}, "results"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Act
			stdout, _, code := runMain(t, tc.env, tc.args...)

			// Assert
			var record map[string]json.RawMessage
			if err := json.Unmarshal([]byte(stdout), &record); err != nil {
				t.Fatalf("stdout is not a record: %v\n%s", err, stdout)
			}

			if string(record[tc.key]) != "[]" || !strings.Contains(string(record["error"]), `"code": "usage"`) {
				t.Errorf("record = %s, want empty %q and a usage error", stdout, tc.key)
			}

			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
		})
	}
}

func TestFlagsFromEnv(t *testing.T) {
	t.Parallel()

	// Arrange
	skillsDir := filepath.Join(t.TempDir(), "skills")

	// Act
	stdout, stderr, code := runMain(t, []string{"SKOLAR_DIR=" + skillsDir, "SKOLAR_JSON=true"}, "update")

	// Assert
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q, want 0", code, stderr)
	}

	if got := decodeRecord(t, []byte(stdout), "results"); len(got) != 0 {
		t.Errorf("results = %v, want empty from SKOLAR_JSON", got)
	}
}

func TestNewRootCmdRegistersCoreCommands(t *testing.T) {
	root := cmd.NewRootCmd("test")

	if got, want := root.Use, "skolar"; got != want {
		t.Fatalf("Use = %q, want %q", got, want)
	}

	if !root.SilenceUsage {
		t.Fatal("SilenceUsage = false, want true")
	}

	checks := []struct {
		name  string
		alias string
	}{
		{name: "add"},
		{name: "update"},
		{name: "list", alias: "ls"},
		{name: "remove", alias: "rm"},
	}

	for _, check := range checks {
		sub, _, err := root.Find([]string{check.name})
		if err != nil {
			t.Fatalf("Find(%q) error = %v", check.name, err)
		}

		if sub == nil || sub.Name() != check.name {
			t.Fatalf("Find(%q) returned %v", check.name, sub)
		}

		if check.alias == "" {
			continue
		}

		aliasSub, _, err := root.Find([]string{check.alias})
		if err != nil {
			t.Fatalf("Find(%q) error = %v", check.alias, err)
		}

		if aliasSub == nil || aliasSub.Name() != check.name {
			t.Fatalf("Find(%q) returned %v, want command %q", check.alias, aliasSub, check.name)
		}
	}
}
