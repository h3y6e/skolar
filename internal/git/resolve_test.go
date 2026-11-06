package git_test

import (
	"testing"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/testutil"
)

func initTaggedRepo(t *testing.T) string {
	t.Helper()

	work := testutil.InitWorkRepo(t)
	skillFile := "skills/my-skill/SKILL.md"

	testutil.Commit(t, work, "v1.0.0", map[string]string{skillFile: "# v1.0.0\n"})
	testutil.Git(t, work, "tag", "v1.0.0")

	testutil.Commit(t, work, "v1.9.0", map[string]string{skillFile: "# v1.9.0\n"})
	testutil.Git(t, work, "tag", "v1.9.0")
	testutil.Git(t, work, "tag", "note")
	testutil.Git(t, work, "tag", "-a", "annotated", "-m", "annotated")

	testutil.Commit(t, work, "v2.0.0-rc.1", map[string]string{skillFile: "# v2.0.0-rc.1\n"})
	testutil.Git(t, work, "tag", "v2.0.0-rc.1")
	testutil.Git(t, work, "tag", "collide")

	testutil.Git(t, work, "checkout", "-b", "feature/install")
	testutil.Commit(t, work, "feature", map[string]string{skillFile: "# Feature\n"})

	testutil.Git(t, work, "checkout", "-b", "collide")
	testutil.Git(t, work, "checkout", "main")

	return testutil.BareClone(t, work)
}

func TestResolveLatest(t *testing.T) {
	t.Parallel()

	t.Run("明示的にブランチを渡したとき、解決はそのブランチの ref を返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		// Act
		got, err := git.ResolveLatest(t.Context(), repo, "feature/install")
		// Assert
		if err != nil {
			t.Fatalf("ResolveLatest() error = %v", err)
		}

		if got.Full != "refs/heads/feature/install" {
			t.Errorf("Full = %q, want %q", got.Full, "refs/heads/feature/install")
		}
	})

	t.Run("明示的にタグを渡したとき、解決はそのタグの ref を返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		// Act
		got, err := git.ResolveLatest(t.Context(), repo, "v1.0.0")
		// Assert
		if err != nil {
			t.Fatalf("ResolveLatest() error = %v", err)
		}

		if got.Full != "refs/tags/v1.0.0" {
			t.Errorf("Full = %q, want %q", got.Full, "refs/tags/v1.0.0")
		}
	})

	t.Run("明示的な ref がどれにも一致しないとき、解決はそれをコミット SHA として扱う", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)
		sha := "329042b24ebc70240bb382a932a94bb90747622c"

		// Act
		got, err := git.ResolveLatest(t.Context(), repo, sha)
		// Assert
		if err != nil {
			t.Fatalf("ResolveLatest() error = %v", err)
		}

		if got.Full != sha {
			t.Errorf("got = %+v, want Full = %q", got, sha)
		}
	})

	t.Run("ref を渡さないとき、解決は最新の semver タグを返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		// Act
		got, err := git.ResolveLatest(t.Context(), repo, "")
		// Assert
		if err != nil {
			t.Fatalf("ResolveLatest() error = %v", err)
		}

		if got.Full != "refs/tags/v1.9.0" {
			t.Errorf("Full = %q, want %q", got.Full, "refs/tags/v1.9.0")
		}
	})

	t.Run("ref を渡さずリポジトリに semver タグがないとき、解決はデフォルトブランチを返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := testutil.BareRepo(t, map[string]string{"README.md": "hello\n"})

		// Act
		got, err := git.ResolveLatest(t.Context(), repo, "")
		// Assert
		if err != nil {
			t.Fatalf("ResolveLatest() error = %v", err)
		}

		if got.Full != "refs/heads/main" {
			t.Errorf("Full = %q, want %q", got.Full, "refs/heads/main")
		}
	})
}

func TestResolveLatestTie(t *testing.T) {
	t.Parallel()

	t.Run("二つのタグが同じバージョンとして順位づけられるとき、解決は常に同じものを選ぶ", func(t *testing.T) {
		t.Parallel()

		// Arrange
		work := testutil.InitWorkRepo(t)
		testutil.Commit(t, work, "one", map[string]string{"a": "1\n"})
		testutil.Git(t, work, "tag", "1.0.0")
		testutil.Commit(t, work, "two", map[string]string{"a": "2\n"})
		testutil.Git(t, work, "tag", "v1.0.0")
		repo := testutil.BareClone(t, work)

		for range 10 {
			// Act
			got, err := git.ResolveLatest(t.Context(), repo, "")

			// Assert
			if err != nil || got.Full != "refs/tags/v1.0.0" {
				t.Fatalf("ResolveLatest() = %+v, %v, want refs/tags/v1.0.0", got, err)
			}
		}
	})
}

func TestResolveDefault(t *testing.T) {
	t.Parallel()

	t.Run("ref を渡さないとき、semver タグがあっても解決はデフォルトブランチを返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		repo := initTaggedRepo(t)

		// Act
		got, err := git.ResolveDefault(t.Context(), repo, "")
		// Assert
		if err != nil || got.Full != "refs/heads/main" {
			t.Errorf("ResolveDefault() = %+v, %v, want refs/heads/main", got, err)
		}
	})
}
