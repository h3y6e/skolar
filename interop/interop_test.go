//go:build interop

// Package interop は公開フィクスチャリポジトリを使い、flake.lock で固定した
// 上流の CLI (`skills` としての vercel-labs/skills と `gh skill`) に対して skolar を実行する
package interop

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const (
	fixtureRepo = "h3y6e/skolar-fixtures"
	fixtureURL  = "https://github.com/" + fixtureRepo
)

var skolar string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "skolar-interop-*")
	if err != nil {
		panic(err)
	}
	// サンドボックス化した HOME では gh の保存済みログインが見えないので GH_TOKEN として渡す
	if os.Getenv("GH_TOKEN") == "" {
		if token, err := exec.CommandContext(context.Background(), "gh", "auth", "token").Output(); err == nil {
			if err := os.Setenv("GH_TOKEN", strings.TrimSpace(string(token))); err != nil {
				panic(err)
			}
		}
	}

	skolar = filepath.Join(dir, "skolar")
	build := exec.CommandContext(context.Background(), "go", "build", "-o", skolar, "github.com/h3y6e/skolar")

	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		panic(err)
	}

	// macOS は新しいバイナリを初回実行時に検査する。その時間が最初のベンチマークに入らないよう先に一度実行しておく
	if err := exec.CommandContext(context.Background(), skolar, "--version").Run(); err != nil {
		panic(err)
	}

	for _, v := range []struct {
		tool  string
		field int
		argv  []string
	}{
		{"skolar", 0, []string{"git", "rev-parse", "--short", "HEAD"}},
		{"skills", 0, []string{"skills", "--version"}},
		{"gh", 2, []string{"gh", "--version"}},
	} {
		out, err := exec.CommandContext(context.Background(), v.argv[0], v.argv[1:]...).Output()
		if err != nil {
			panic(err)
		}

		fmt.Println("version:", v.tool, strings.Fields(string(out))[v.field])
	}

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}

var homes sync.Map

func homeOf(tb testing.TB) string {
	tb.Helper()

	if home, ok := homes.Load(tb.Name()); ok {
		h, _ := home.(string)
		return h
	}

	h := tb.TempDir()
	homes.Store(tb.Name(), h)
	tb.Cleanup(func() { homes.Delete(tb.Name()) })

	return h
}

func runIn(tb testing.TB, home, dir, name string, args ...string) (string, string) {
	tb.Helper()

	cmd := exec.CommandContext(tb.Context(), name, args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(),
		"HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"DO_NOT_TRACK=1", "DISABLE_TELEMETRY=1", "NO_COLOR=1")

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		tb.Fatalf("%s %s: %v\nstdout:\n%s\nstderr:\n%s", name, strings.Join(args, " "), err, out, stderr.String())
	}

	return string(out), stderr.String()
}

func runStderr(tb testing.TB, dir, name string, args ...string) (string, string) {
	tb.Helper()

	return runIn(tb, homeOf(tb), dir, name, args...)
}

// ghUpdate は gh skill update を実行し、gh がスキルを飛ばすときに出す警告を失敗として扱う。
// 警告が出ても終了コードは 0 になるためである
func ghUpdate(tb testing.TB, home, dir string, args ...string) {
	tb.Helper()

	_, stderr := runIn(tb, home, filepath.Dir(dir), "gh", append([]string{"skill", "update", "--dir", dir}, args...)...)
	for line := range strings.Lines(stderr) {
		if strings.HasPrefix(line, "!") {
			tb.Errorf("gh skill update が警告した: %s", strings.TrimSpace(line))
		}
	}
}

func run(tb testing.TB, dir, name string, args ...string) string {
	tb.Helper()
	out, _ := runStderr(tb, dir, name, args...)

	return out
}

func vercelAdd(tb testing.TB, project string) {
	tb.Helper()
	run(tb, project, "skills", "add", fixtureURL, "--skill", "*", "--agent", "universal", "--copy", "--full-depth", "-y")
}

func ghInstall(tb testing.TB, dir string, args ...string) {
	tb.Helper()
	run(tb, filepath.Dir(dir), "gh", append([]string{"skill", "install", fixtureRepo, "--dir", dir}, args...)...)
}

func skillNames(tb testing.TB, dir string) []string {
	tb.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		tb.Fatal(err)
	}

	var names []string

	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}

	return names
}
