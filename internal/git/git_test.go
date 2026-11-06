package git_test

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/testutil"
)

func TestMain(m *testing.M) { testutil.Main(m) }

func fetch(t *testing.T, url, ref string) (git.Snapshot, error) {
	t.Helper()

	resolved, err := git.ResolveDefault(t.Context(), url, ref)
	if err != nil {
		return git.Snapshot{}, err
	}

	return newCache(t).Fetch(t.Context(), url, resolved)
}

func newCache(t *testing.T) *git.Cache {
	t.Helper()

	c := git.NewCache()

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func readSkill(t *testing.T, s git.Snapshot) string {
	t.Helper()

	data, err := fs.ReadFile(s.FS, "skills/my-skill/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

func TestFetch(t *testing.T) {
	t.Parallel()

	t.Run("ref を渡さないとき、取得はデフォルトブランチを読む", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		// Act
		s, err := fetch(t, repo, "")
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := readSkill(t, s); got != "# v2.0.0-rc.1\n" {
			t.Errorf("SKILL.md = %q, want the main branch content", got)
		}
	})

	t.Run("コミットがリンクと実行ファイルを含むとき、ファイルはモードを保ちリンクはたどられない", func(t *testing.T) {
		t.Parallel()

		// Arrange
		work := testutil.InitWorkRepo(t)
		testutil.WriteFiles(t, work, map[string]string{"skills/a/SKILL.md": "# A\n", "skills/a/run.sh": "#!/bin/sh\n"})

		if err := os.Chmod(filepath.Join(work, "skills", "a", "run.sh"), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink("../../../etc", filepath.Join(work, "skills", "a", "escape")); err != nil {
			t.Fatal(err)
		}

		testutil.Git(t, work, "add", "-A")
		testutil.Git(t, work, "commit", "-m", "init")

		// Act
		s, err := fetch(t, "file://"+work, "")
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		var walked []string

		_ = fs.WalkDir(s.FS, "skills", func(p string, d fs.DirEntry, err error) error {
			walked = append(walked, p+" "+d.Type().String())
			return err
		})

		want := []string{"skills d---------", "skills/a d---------", "skills/a/SKILL.md ----------", "skills/a/escape L---------", "skills/a/run.sh ----------"}
		if !slices.Equal(walked, want) {
			t.Errorf("walked %q, want %q", walked, want)
		}

		if info, err := fs.Stat(s.FS, "skills/a/run.sh"); err != nil || info.Mode().Perm() != 0o755 {
			t.Errorf("run.sh mode = %v, %v, want 0755", info, err)
		}

		if _, err := s.FS.Open("skills/a/escape/passwd"); err == nil {
			t.Error("opened a path through a link")
		}
	})

	t.Run("ブランチ、軽量タグ、注釈付きタグを渡したとき、取得はその内容を読む", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		for ref, want := range map[string]string{"feature/install": "# Feature\n", "v1.0.0": "# v1.0.0\n", "annotated": "# v1.9.0\n"} {
			// Act
			s, err := fetch(t, repo, ref)
			// Assert
			if err != nil {
				t.Fatalf("%s: %v", ref, err)
			}

			if got := readSkill(t, s); got != want {
				t.Errorf("%s: SKILL.md = %q, want %q", ref, got, want)
			}
		}
	})

	t.Run("名前がブランチとタグの両方に一致するとき、取得はブランチを読む", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		// Act
		s, err := fetch(t, repo, "collide")
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if s.Ref.Full != "refs/heads/collide" || readSkill(t, s) != "# Feature\n" {
			t.Errorf("ref %q, SKILL.md %q, want the collide branch", s.Ref.Full, readSkill(t, s))
		}
	})

	t.Run("ref がコミット SHA のとき、取得はそのコミットを読む", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)
		sha := strings.TrimSpace(testutil.Git(t, strings.TrimPrefix(repo, "file://"), "rev-parse", "v1.0.0"))

		// Act
		s, err := fetch(t, repo, sha)
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := readSkill(t, s); got != "# v1.0.0\n" {
			t.Errorf("SKILL.md = %q, want the v1.0.0 content", got)
		}
	})

	t.Run("コミットを以前に取得したとき、取得はリモートなしでそれを読む", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		ref, err := git.ResolveDefault(t.Context(), repo, "v1.0.0")
		if err != nil {
			t.Fatal(err)
		}

		if _, err := newCache(t).Fetch(t.Context(), repo, ref); err != nil {
			t.Fatal(err)
		}

		if err := os.RemoveAll(strings.TrimPrefix(repo, "file://")); err != nil {
			t.Fatal(err)
		}

		// Act
		s, err := newCache(t).Fetch(t.Context(), repo, ref)
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := readSkill(t, s); got != "# v1.0.0\n" {
			t.Errorf("SKILL.md = %q, want the v1.0.0 content", got)
		}
	})

	t.Run("取得済みのブランチが上流で削除されたとき、取得はキャッシュのコピーを読まず失敗する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)
		if _, err := fetch(t, repo, "feature/install"); err != nil {
			t.Fatal(err)
		}

		testutil.Git(t, strings.TrimPrefix(repo, "file://"), "branch", "-D", "feature/install")

		// Act
		_, err := fetch(t, repo, "feature/install")

		// Assert
		if err == nil {
			t.Fatal("fetched a branch the remote deleted")
		}
	})

	t.Run("同じリモートを並行して取得したとき、すべての取得が成功する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)
		refs := []string{"v1.0.0", "v1.9.0", "feature/install", "main", "collide", "v1.0.0"}
		errs := make([]error, len(refs))

		// Act
		var wg sync.WaitGroup
		for i, ref := range refs {
			wg.Go(func() { _, errs[i] = fetch(t, repo, ref) })
		}

		wg.Wait()

		// Assert
		for i, err := range errs {
			if err != nil {
				t.Errorf("%s: %v", refs[i], err)
			}
		}
	})

	t.Run("取得したとき、TreeSHA はルートとディレクトリのツリー SHA を返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)
		bare := strings.TrimPrefix(repo, "file://")

		// Act
		s, err := fetch(t, repo, "v1.0.0")
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		for path, rev := range map[string]string{".": "v1.0.0^{tree}", "skills/my-skill": "v1.0.0:skills/my-skill"} {
			if got, err := s.TreeSHA(path); got != strings.TrimSpace(testutil.Git(t, bare, "rev-parse", rev)) {
				t.Errorf("TreeSHA(%q) = %q, %v, want %s", path, got, err, rev)
			}
		}
	})

	t.Run("リモートが存在しないとき、取得は失敗する", func(t *testing.T) {
		t.Parallel()

		// Act
		_, err := fetch(t, "/nonexistent/repo", "")

		// Assert
		if err == nil {
			t.Fatal("fetch succeeded without a remote")
		}
	})

	t.Run("コンテキストがキャンセルされたとき、取得は失敗する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)
		ref := git.ResolvedRef{Full: "refs/heads/main", Object: "refs/heads/main"}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		// Act
		c := git.NewCache()

		t.Cleanup(func() { _ = c.Close() })

		_, err := c.Fetch(ctx, repo, ref)

		// Assert
		if err == nil {
			t.Fatal("fetch succeeded with a cancelled context")
		}
	})
}

func TestFetchObjectFormat(t *testing.T) {
	t.Run("リモートが SHA-256 を使うとき、取得はそれを読む", func(t *testing.T) {
		// Arrange
		work := t.TempDir()
		testutil.Git(t, work, "init", "--object-format=sha256", "-b", "main")
		testutil.Git(t, work, "config", "user.email", "test@test.com")
		testutil.Git(t, work, "config", "user.name", "Test")
		testutil.Commit(t, work, "init", map[string]string{"skills/my-skill/SKILL.md": "# 256\n"})

		// Act
		s, err := fetch(t, "file://"+work, "")
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := readSkill(t, s); got != "# 256\n" {
			t.Errorf("SKILL.md = %q", got)
		}
	})

	t.Run("git が既定で SHA-256 のリポジトリを作るとき、SHA-1 のリモートの取得も成功する", func(t *testing.T) {
		// Arrange
		repo := initTaggedRepo(t)
		t.Setenv("GIT_DEFAULT_HASH", "sha256")

		// Act
		_, err := fetch(t, repo, "v1.0.0")
		// Assert
		if err != nil {
			t.Errorf("fetch() error = %v", err)
		}
	})
}

func TestReadTree(t *testing.T) {
	t.Parallel()

	t.Run("取得したコミットがツリーを持つとき、読み取りはツリーのファイルを一覧する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		s, err := fetch(t, repo, "v1.0.0")
		if err != nil {
			t.Fatal(err)
		}

		// Act
		sha, err := s.TreeSHA("skills/my-skill")
		if err != nil {
			t.Fatal(err)
		}

		tree, err := newCache(t).ReadTree(t.Context(), repo, sha)
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if data, err := fs.ReadFile(tree, "SKILL.md"); string(data) != "# v1.0.0\n" {
			t.Errorf("SKILL.md = %q, %v, want the v1.0.0 content", data, err)
		}
	})

	t.Run("リモートが .git 付きで指定されたとき、読み取りは .git なしの名前で取得したキャッシュを使う", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		s, err := fetch(t, repo, "v1.0.0")
		if err != nil {
			t.Fatal(err)
		}

		sha, err := s.TreeSHA("skills/my-skill")
		if err != nil {
			t.Fatal(err)
		}

		// Act
		_, err = newCache(t).ReadTree(t.Context(), repo+".git", sha)
		// Assert
		if err != nil {
			t.Errorf("ReadTree() error = %v, want the cached tree", err)
		}
	})

	t.Run("取得済みのどれもツリーを持たないとき、読み取りはキャッシュにないことを報告する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)
		if _, err := fetch(t, repo, "v1.0.0"); err != nil {
			t.Fatal(err)
		}

		bare := strings.TrimPrefix(repo, "file://")
		feature := strings.TrimSpace(testutil.Git(t, bare, "rev-parse", "feature/install:skills/my-skill"))

		// Act
		_, err := newCache(t).ReadTree(t.Context(), repo, feature)

		// Assert
		if !errors.Is(err, git.ErrNotCached) {
			t.Errorf("ReadTree() error = %v, want ErrNotCached", err)
		}
	})

	t.Run("ツリー SHA が ref 名のとき、名前を解決せずに読み取りが失敗する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)
		if _, err := fetch(t, repo, "v1.0.0"); err != nil {
			t.Fatal(err)
		}

		// Act
		_, err := newCache(t).ReadTree(t.Context(), repo, "refs/tags/v1.0.0")

		// Assert
		if err == nil || errors.Is(err, git.ErrNotCached) {
			t.Errorf("ReadTree() error = %v, want an invalid tree SHA", err)
		}
	})
}

func TestFetchUntrustedInput(t *testing.T) {
	t.Parallel()

	t.Run("ref がオプションに見えるとき、git はそれをオプションとして実行しない", func(t *testing.T) {
		t.Parallel()
		// Arrange
		bare := testutil.BareRepo(t, map[string]string{"SKILL.md": "# S\n"})
		marker := filepath.Join(t.TempDir(), "pwned")

		// Act
		_, _ = fetch(t, bare, "--upload-pack=touch "+marker)

		// Assert
		if _, err := os.Stat(marker); !os.IsNotExist(err) {
			t.Errorf("--upload-pack ran: stat %s = %v", marker, err)
		}
	})

	t.Run("リモートが認証情報を求めるとき、解決はプロンプトを出さず失敗する", func(t *testing.T) {
		t.Parallel()
		// Arrange
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		t.Cleanup(srv.Close)

		// Act
		_, err := fetch(t, srv.URL+"/o/r.git", "")

		// Assert
		if err == nil || !strings.Contains(err.Error(), "terminal prompts disabled") {
			t.Errorf("fetch error = %v, want terminal prompts disabled", err)
		}
	})
}

func TestFetchIgnoresAskpass(t *testing.T) {
	t.Run("askpass プログラムが設定されているとき、解決はそれを実行せず失敗する", func(t *testing.T) {
		// Arrange
		marker := filepath.Join(t.TempDir(), "asked")

		askpass := filepath.Join(t.TempDir(), "askpass")
		if err := os.WriteFile(askpass, []byte("#!/bin/sh\ntouch "+marker+"\necho x\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		t.Setenv("GIT_ASKPASS", askpass)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("WWW-Authenticate", `Basic realm="x"`)
			w.WriteHeader(http.StatusUnauthorized)
		}))
		t.Cleanup(srv.Close)

		// Act
		_, err := fetch(t, srv.URL+"/o/r.git", "")

		// Assert
		if err == nil {
			t.Fatal("fetch succeeded against a remote that rejects every request")
		}

		if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
			t.Errorf("askpass ran: stat %s = %v", marker, statErr)
		}
	})
}
