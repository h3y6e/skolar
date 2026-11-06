package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/h3y6e/skolar/internal/testutil"
)

func TestLock(t *testing.T) {
	t.Parallel()

	t.Run("他の保持者がロックを保持しているとき、ロックを待つとコンテキストの終了で打ち切られる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		path := filepath.Join(t.TempDir(), "x.lock")

		unlock, err := lock(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(unlock)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		// Act
		_, err = lock(ctx, path)

		// Assert
		if !errors.Is(err, context.Canceled) {
			t.Errorf("lock() error = %v, want context.Canceled", err)
		}
	})
}

func TestCacheRepo(t *testing.T) {
	t.Parallel()

	t.Run("リポジトリを開いたとき、キャッシュは所有者だけが入れる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		c := NewCache()

		t.Cleanup(func() { _ = c.Close() })

		// Act
		r, err := c.repo(t.Context(), testutil.BareRepo(t, map[string]string{"a": "a\n"}), "sha1")
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if info, err := os.Stat(filepath.Dir(filepath.Dir(r.gitDir))); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("cache root = %v, %v, want drwx------", info, err)
		}
	})

	t.Run("ブランチが進んだとき、gc は以前に取得したツリーを残す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		work := testutil.InitWorkRepo(t)
		testutil.Commit(t, work, "v1", map[string]string{"SKILL.md": "v1\n"})
		url := testutil.BareClone(t, work)
		bare := strings.TrimPrefix(url, "file://")

		c := NewCache()

		t.Cleanup(func() { _ = c.Close() })

		first, err := ResolveDefault(t.Context(), url, "")
		if err != nil {
			t.Fatal(err)
		}

		old, err := c.Fetch(t.Context(), url, first)
		if err != nil {
			t.Fatal(err)
		}

		oldTree, err := old.TreeSHA(".")
		if err != nil {
			t.Fatal(err)
		}

		testutil.Commit(t, work, "v2", map[string]string{"SKILL.md": "v2\n"})
		testutil.Commit(t, work, "v3", map[string]string{"SKILL.md": "v3\n"})
		testutil.Git(t, work, "push", "-q", bare, "main")

		second, err := ResolveDefault(t.Context(), url, "")
		if err != nil {
			t.Fatal(err)
		}

		if _, err := c.Fetch(t.Context(), url, second); err != nil {
			t.Fatal(err)
		}

		r, err := c.repo(t.Context(), url, "sha1")
		if err != nil {
			t.Fatal(err)
		}

		// Act
		testutil.Git(t, r.gitDir, "gc", "-q", "--prune=now")

		// Assert
		fresh := NewCache()

		t.Cleanup(func() { _ = fresh.Close() })

		if _, err := fresh.ReadTree(t.Context(), url, oldTree); err != nil {
			t.Errorf("ReadTree(old) error = %v, want the tree fetched before", err)
		}
	})
}

func TestCacheRoot(t *testing.T) {
	t.Run("XDG_CACHE_HOME が絶対パスのとき、キャッシュはそこに置かれる", func(t *testing.T) {
		// Arrange
		want := t.TempDir()
		t.Setenv("XDG_CACHE_HOME", want)

		// Act
		got, err := cacheRoot()

		// Assert
		if err != nil || got != want {
			t.Errorf("cacheRoot() = %q, %v, want %q", got, err, want)
		}
	})

	t.Run("XDG_CACHE_HOME が相対パスのとき、無視される", func(t *testing.T) {
		// Arrange
		t.Setenv("XDG_CACHE_HOME", "relative")

		// Act
		got, err := cacheRoot()

		// Assert
		if err != nil || !filepath.IsAbs(got) {
			t.Errorf("cacheRoot() = %q, %v, want the absolute platform cache directory", got, err)
		}
	})
}
