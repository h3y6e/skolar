package skill_test

import (
	"strings"
	"testing"

	"github.com/h3y6e/skolar/internal/skill"
)

func TestParseSourceMetadata(t *testing.T) {
	t.Parallel()

	t.Run("SKILL.md に github のメタデータがあるとき、解析はソースのメタデータを返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "---\n" +
			"description: Creates draft content for new issues\n" +
			"license: MIT\n" +
			"metadata:\n" +
			"    author: h3y6e\n" +
			"    github-path: skills/creating-issues\n" +
			"    github-ref: refs/tags/v2026.7.1\n" +
			"    github-repo: https://github.com/h3y6e/agent-skills\n" +
			"    github-tree-sha: 7a5507a41b20be505102f26e6b75927edd1493fd\n" +
			"    version: 2026.7.1\n" +
			"name: creating-issues\n" +
			"---\n" +
			"# Creating Issues\n"

		// Act
		meta, err := skill.ParseSourceMetadata(content)
		// Assert
		if err != nil {
			t.Fatalf("ParseSourceMetadata() error = %v", err)
		}

		if !meta.IsGitHub() {
			t.Errorf("IsGitHub() = false, want true")
		}

		if meta.RepoURL != "https://github.com/h3y6e/agent-skills" {
			t.Errorf("RepoURL = %q", meta.RepoURL)
		}

		if meta.Ref != "refs/tags/v2026.7.1" {
			t.Errorf("Ref = %q", meta.Ref)
		}

		if meta.TreeSHA != "7a5507a41b20be505102f26e6b75927edd1493fd" {
			t.Errorf("TreeSHA = %q", meta.TreeSHA)
		}

		if meta.Path != "skills/creating-issues" {
			t.Errorf("Path = %q", meta.Path)
		}

		if meta.Pinned != "" {
			t.Errorf("Pinned = %q, want empty", meta.Pinned)
		}

		if meta.IsLocal() {
			t.Errorf("IsLocal() = true, want false")
		}
	})

	t.Run("SKILL.md に固定された github のメタデータがあるとき、解析は固定されたバージョンを返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "---\n" +
			"name: git-commit\n" +
			"metadata:\n" +
			"    github-path: skills/git-commit\n" +
			"    github-pinned: 329042b24ebc70240bb382a932a94bb90747622c\n" +
			"    github-ref: 329042b24ebc70240bb382a932a94bb90747622c\n" +
			"    github-repo: https://github.com/github/awesome-copilot\n" +
			"    github-tree-sha: 883a6a0000000000000000000000000000000000\n" +
			"---\n" +
			"# Git Commit\n"

		// Act
		meta, err := skill.ParseSourceMetadata(content)
		// Assert
		if err != nil {
			t.Fatalf("ParseSourceMetadata() error = %v", err)
		}

		if meta.Pinned != "329042b24ebc70240bb382a932a94bb90747622c" {
			t.Errorf("Pinned = %q", meta.Pinned)
		}
	})

	t.Run("SKILL.md にフロントマターがないとき、解析は空のメタデータを返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "# Just a heading\n\nSome body.\n"

		// Act
		meta, err := skill.ParseSourceMetadata(content)
		// Assert
		if err != nil {
			t.Fatalf("ParseSourceMetadata() error = %v", err)
		}

		if meta.IsGitHub() || meta.IsLocal() {
			t.Errorf("expected no source metadata, got %+v", meta)
		}
	})

	t.Run("SKILL.md のフロントマターにメタデータがないとき、解析は空のメタデータを返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "---\nname: plain\ndescription: no metadata here\n---\n# Plain\n"

		// Act
		meta, err := skill.ParseSourceMetadata(content)
		// Assert
		if err != nil {
			t.Fatalf("ParseSourceMetadata() error = %v", err)
		}

		if meta.IsGitHub() || meta.IsLocal() {
			t.Errorf("expected no source metadata, got %+v", meta)
		}
	})

	t.Run("SKILL.md に local-path のメタデータがあるとき、解析はローカルのソースと報告する", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "---\n" +
			"name: my-skill\n" +
			"metadata:\n" +
			"    local-path: /home/user/my-skills/skills/my-skill\n" +
			"---\n" +
			"# My Skill\n"

		// Act
		meta, err := skill.ParseSourceMetadata(content)
		// Assert
		if err != nil {
			t.Fatalf("ParseSourceMetadata() error = %v", err)
		}

		if !meta.IsLocal() {
			t.Errorf("IsLocal() = false, want true")
		}

		if meta.LocalPath != "/home/user/my-skills/skills/my-skill" {
			t.Errorf("LocalPath = %q", meta.LocalPath)
		}

		if meta.IsGitHub() {
			t.Errorf("IsGitHub() = true, want false")
		}
	})

	t.Run("フロントマターの YAML が不正なとき、解析はエラーを返す", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "---\n: : : invalid\n---\n# Broken\n"

		// Act
		_, err := skill.ParseSourceMetadata(content)

		// Assert
		if err == nil {
			t.Fatal("ParseSourceMetadata() expected error, got nil")
		}
	})
}

func TestInjectSourceMetadata(t *testing.T) {
	t.Parallel()

	githubMeta := skill.SourceMetadata{
		RepoURL: "https://github.com/h3y6e/agent-skills",
		Ref:     "refs/heads/main",
		TreeSHA: "7a5507a41b20be505102f26e6b75927edd1493fd",
		Path:    "skills/creating-issues",
	}

	t.Run("フロントマターのない SKILL.md に注入するとき、フロントマターのブロックが作られる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "# No Frontmatter\n\nBody text.\n"

		// Act
		out, err := skill.InjectSourceMetadata(content, githubMeta)
		// Assert
		if err != nil {
			t.Fatalf("InjectSourceMetadata() error = %v", err)
		}

		if !strings.HasPrefix(out, "---\n") {
			t.Errorf("output does not start with frontmatter delimiter:\n%s", out)
		}

		if !strings.Contains(out, "github-repo: https://github.com/h3y6e/agent-skills") {
			t.Errorf("output missing github-repo:\n%s", out)
		}

		if !strings.HasSuffix(out, "# No Frontmatter\n\nBody text.\n") {
			t.Errorf("output lost body:\n%s", out)
		}
	})

	t.Run("github のメタデータを注入するとき、既存のフロントマターのキーと本文は保たれる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "---\n" +
			"description: Use when creating a git commit.\n" +
			"license: MIT\n" +
			"metadata:\n" +
			"    author: h3y6e\n" +
			"    version: 2026.7.0\n" +
			"name: cxg\n" +
			"---\n" +
			"# cxg\n"

		// Act
		out, err := skill.InjectSourceMetadata(content, githubMeta)
		// Assert
		if err != nil {
			t.Fatalf("InjectSourceMetadata() error = %v", err)
		}

		for _, want := range []string{
			"description: Use when creating a git commit.",
			"license: MIT",
			"name: cxg",
			"author: h3y6e",
			"version: 2026.7.0",
			"github-path: skills/creating-issues",
			"github-ref: refs/heads/main",
			"github-repo: https://github.com/h3y6e/agent-skills",
			"github-tree-sha: 7a5507a41b20be505102f26e6b75927edd1493fd",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}

		if !strings.HasSuffix(out, "# cxg\n") {
			t.Errorf("output lost body:\n%s", out)
		}
	})

	t.Run("固定バージョン付きで注入するとき、github-pinned が設定される", func(t *testing.T) {
		t.Parallel()

		// Arrange
		meta := githubMeta
		meta.Pinned = "v1.2.0"

		// Act
		out, err := skill.InjectSourceMetadata("# Skill\n", meta)
		// Assert
		if err != nil {
			t.Fatalf("InjectSourceMetadata() error = %v", err)
		}

		if !strings.Contains(out, "github-pinned: v1.2.0") {
			t.Errorf("output missing github-pinned:\n%s", out)
		}
	})

	t.Run("固定のメタデータに固定なしで注入するとき、github-pinned は削除される", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "---\n" +
			"metadata:\n" +
			"    github-pinned: v1.0.0\n" +
			"    github-repo: https://github.com/h3y6e/agent-skills\n" +
			"name: cxg\n" +
			"---\n" +
			"# cxg\n"

		// Act
		out, err := skill.InjectSourceMetadata(content, githubMeta)
		// Assert
		if err != nil {
			t.Fatalf("InjectSourceMetadata() error = %v", err)
		}

		if strings.Contains(out, "github-pinned") {
			t.Errorf("output still has github-pinned:\n%s", out)
		}
	})

	t.Run("ローカルのメタデータを注入するとき、github のキーは削除され local-path が設定される", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "---\n" +
			"metadata:\n" +
			"    github-path: skills/cxg\n" +
			"    github-ref: refs/heads/main\n" +
			"    github-repo: https://github.com/h3y6e/cxg\n" +
			"    github-tree-sha: 6590912c0383c31cea5566053f61e471a084d722\n" +
			"name: cxg\n" +
			"---\n" +
			"# cxg\n"
		localMeta := skill.SourceMetadata{LocalPath: "/home/user/cxg/skills/cxg"}

		// Act
		out, err := skill.InjectSourceMetadata(content, localMeta)
		// Assert
		if err != nil {
			t.Fatalf("InjectSourceMetadata() error = %v", err)
		}

		if strings.Contains(out, "github-") {
			t.Errorf("output still has github keys:\n%s", out)
		}

		if !strings.Contains(out, "local-path: /home/user/cxg/skills/cxg") {
			t.Errorf("output missing local-path:\n%s", out)
		}

		if !strings.Contains(out, "name: cxg") {
			t.Errorf("output lost name:\n%s", out)
		}
	})

	t.Run("注入した内容を解析するとき、同じメタデータが返る", func(t *testing.T) {
		t.Parallel()

		// Arrange
		content := "---\nname: cxg\n---\n# cxg\n"

		// Act
		out, err := skill.InjectSourceMetadata(content, githubMeta)
		if err != nil {
			t.Fatalf("InjectSourceMetadata() error = %v", err)
		}

		got, err := skill.ParseSourceMetadata(out)
		// Assert
		if err != nil {
			t.Fatalf("ParseSourceMetadata() error = %v", err)
		}

		if got != githubMeta {
			t.Errorf("round-trip = %+v, want %+v", got, githubMeta)
		}
	})
}
