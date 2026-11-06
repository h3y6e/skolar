// Package testutil はテスト用の git リポジトリのフィクスチャを作る
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Main は git の設定と skolar のキャッシュをユーザーのものから切り離して
// パッケージのテストを実行する
func Main(m *testing.M) {
	home, err := os.MkdirTemp("", "skolar-test-home-*")
	if err != nil {
		panic(err)
	}

	for name, value := range map[string]string{
		"HOME":              home,
		"XDG_CACHE_HOME":    filepath.Join(home, ".cache"),
		"GIT_CONFIG_GLOBAL": os.DevNull,
		"GIT_CONFIG_SYSTEM": os.DevNull,
	} {
		if err := os.Setenv(name, value); err != nil {
			panic(err)
		}
	}

	code := m.Run()

	_ = os.RemoveAll(home)

	os.Exit(code)
}

// Git はユーザー設定とシステム設定から分離して dir で git を実行し、
// その出力を返す
func Git(tb testing.TB, dir string, args ...string) string {
	tb.Helper()

	cmd := exec.CommandContext(tb.Context(), "git", args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		tb.Fatalf("git %v が失敗しました: %v\n%s", args, err, out)
	}

	return string(out)
}

// WriteFiles はスラッシュ区切りのパスをキーとする files を base の下に書き込む
func WriteFiles(tb testing.TB, base string, files map[string]string) {
	tb.Helper()

	for name, content := range files {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			tb.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
}

// InitWorkRepo はコミットの作者情報を設定した main ブランチのリポジトリを作る
func InitWorkRepo(tb testing.TB) string {
	tb.Helper()

	work := tb.TempDir()
	Git(tb, work, "init", "-b", "main")
	Git(tb, work, "config", "user.email", "test@test.com")
	Git(tb, work, "config", "user.name", "Test")

	return work
}

// Commit は files を work に書き込んですべてをコミットする
func Commit(tb testing.TB, work, msg string, files map[string]string) {
	tb.Helper()

	WriteFiles(tb, work, files)
	Git(tb, work, "add", "-A")
	Git(tb, work, "commit", "-m", msg)
}

// BareClone は work の bare clone の file:// URL を返す
func BareClone(tb testing.TB, work string) string {
	tb.Helper()

	bare := tb.TempDir()
	Git(tb, work, "clone", "--bare", work, bare)

	return "file://" + bare
}

// BareRepo は空のベースと files の二つのコミットを持つベアリポジトリの
// file:// URL を返す。履歴があるので shallow clone が実際のリポジトリと同じように振る舞う
func BareRepo(tb testing.TB, files map[string]string) string {
	tb.Helper()

	work := InitWorkRepo(tb)
	Commit(tb, work, "base", map[string]string{".gitkeep": ""})
	Commit(tb, work, "add files", files)

	return BareClone(tb, work)
}

// SkillRepo は大規模なスキル集のような形のベアリポジトリの file:// URL を返す。
// スキルは skills/<group>/<name> の下に置かれ、それぞれ SKILL.md と
// files 個の参照ファイルを持つ
func SkillRepo(tb testing.TB, skills, files int) string {
	tb.Helper()

	work := InitWorkRepo(tb)
	body := strings.Repeat("Explain the step, then show an example of it.\n", 80)

	all := map[string]string{}

	for i := range skills {
		dir := fmt.Sprintf("skills/group-%d/skill-%d", i%8, i)
		all[dir+"/SKILL.md"] = fmt.Sprintf("---\nname: skill-%d\ndescription: Skill %d.\n---\n%s", i, i, body)

		for j := range files {
			all[fmt.Sprintf("%s/references/ref-%d.md", dir, j)] = fmt.Sprintf("# Reference %d\n%s", j, body)
		}
	}

	Commit(tb, work, "skills", all)

	return BareClone(tb, work)
}
