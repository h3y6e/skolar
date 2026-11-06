package skill_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/h3y6e/skolar/internal/skill"
	"github.com/h3y6e/skolar/internal/testutil"
)

func TestDiscoverSkills(t *testing.T) {
	t.Parallel()

	t.Run("skills/ ディレクトリがあるとき、その下のスキルが探索される", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"skills/my-skill/SKILL.md":        "# My Skill\n",
			"skills/other-skill/SKILL.md":     "# Other\n",
			"skills/other-skill/reference.md": "ref\n",
			"README.md":                       "# Repo\n",
		})

		skills, err := skill.DiscoverSkills(os.DirFS(root))
		if err != nil {
			t.Fatalf("DiscoverSkills() error = %v", err)
		}

		if len(skills) != 2 {
			t.Fatalf("expected 2 skills, got %d", len(skills))
		}

		if skills[0].Name != "my-skill" {
			t.Errorf("skills[0].Name = %q, want %q", skills[0].Name, "my-skill")
		}

		if skills[1].Name != "other-skill" {
			t.Errorf("skills[1].Name = %q, want %q", skills[1].Name, "other-skill")
		}

		for _, s := range skills {
			if hash, err := s.Hash(); err != nil || hash == "" {
				t.Errorf("skill %q hash = %q, %v", s.Name, hash, err)
			}
		}
	})

	t.Run("skills/ ディレクトリがないとき、ルートが探索される", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"my-skill/SKILL.md": "# Root Skill\n",
			"README.md":         "# Repo\n",
		})

		skills, err := skill.DiscoverSkills(os.DirFS(root))
		if err != nil {
			t.Fatalf("DiscoverSkills() error = %v", err)
		}

		if len(skills) != 1 {
			t.Fatalf("expected 1 skill, got %d", len(skills))
		}

		if skills[0].Name != "my-skill" {
			t.Errorf("Name = %q, want %q", skills[0].Name, "my-skill")
		}
	})

	t.Run("スキルが 1 階層の名前空間に置かれているとき、探索は末端の名前とフルパスで見つける", func(t *testing.T) {
		t.Parallel()

		// Arrange
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"skills/engineering/codebase-design/SKILL.md": "# Codebase Design\n",
			"skills/writing/writing-japanese/SKILL.md":    "# Writing Japanese\n",
		})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatalf("DiscoverSkills() error = %v", err)
		}

		if len(skills) != 2 {
			t.Fatalf("expected 2 skills, got %d", len(skills))
		}

		if skills[0].Name != "codebase-design" || skills[0].Path != "skills/engineering/codebase-design" {
			t.Errorf("skills[0] = %q (%q)", skills[0].Name, skills[0].Path)
		}

		if skills[1].Name != "writing-japanese" || skills[1].Path != "skills/writing/writing-japanese" {
			t.Errorf("skills[1] = %q (%q)", skills[1].Name, skills[1].Path)
		}
	})

	t.Run("名前空間のないスキルと名前空間付きのスキルが混在するとき、両方が探索される", func(t *testing.T) {
		t.Parallel()

		// Arrange
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"skills/cxg/SKILL.md":                         "# cxg\n",
			"skills/engineering/diagnosing-bugs/SKILL.md": "# Diagnosing Bugs\n",
		})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatalf("DiscoverSkills() error = %v", err)
		}

		if len(skills) != 2 {
			t.Fatalf("expected 2 skills, got %d", len(skills))
		}

		if skills[0].Name != "cxg" || skills[0].Path != "skills/cxg" {
			t.Errorf("skills[0] = %q (%q)", skills[0].Name, skills[0].Path)
		}

		if skills[1].Name != "diagnosing-bugs" || skills[1].Path != "skills/engineering/diagnosing-bugs" {
			t.Errorf("skills[1] = %q (%q)", skills[1].Name, skills[1].Path)
		}
	})

	t.Run("2 つの名前空間に同じ末端の名前があるとき、両方が見つかって選択では最初のものが採られる", func(t *testing.T) {
		t.Parallel()

		// Arrange
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"skills/a/code-review/SKILL.md": "# A\n",
			"skills/b/code-review/SKILL.md": "# B\n",
		})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		picked := skill.FilterDiscoveredSkills(skills, nil)
		if len(skills) != 2 || len(picked) != 1 || picked[0].Path != "skills/a/code-review" {
			t.Errorf("skills = %+v, picked = %+v, want both found and skills/a/code-review picked", skills, picked)
		}
	})

	t.Run("スキルを探索したとき、Path にリポジトリからの相対ディレクトリが記録される", func(t *testing.T) {
		t.Parallel()

		// Arrange
		skillsRoot := t.TempDir()
		testutil.WriteFiles(t, skillsRoot, map[string]string{
			"skills/my-skill/SKILL.md": "# My Skill\n",
		})
		flatRoot := t.TempDir()
		testutil.WriteFiles(t, flatRoot, map[string]string{
			"my-skill/SKILL.md": "# My Skill\n",
		})

		// Act
		fromSkillsDir, err := skill.DiscoverSkills(os.DirFS(skillsRoot))
		if err != nil {
			t.Fatalf("DiscoverSkills() error = %v", err)
		}

		fromFlat, err := skill.DiscoverSkills(os.DirFS(flatRoot))
		if err != nil {
			t.Fatalf("DiscoverSkills() error = %v", err)
		}

		// Assert
		if got := fromSkillsDir[0].Path; got != "skills/my-skill" {
			t.Errorf("Path = %q, want %q", got, "skills/my-skill")
		}

		if got := fromFlat[0].Path; got != "my-skill" {
			t.Errorf("Path = %q, want %q", got, "my-skill")
		}
	})

	t.Run("skills/ とルート直下のディレクトリの両方にスキルがあるとき、両方が既定になる", func(t *testing.T) {
		t.Parallel()
		// Arrange
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"skills/preferred/SKILL.md": "# Preferred\n",
			"root-skill/SKILL.md":       "# Root\n",
		})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if len(skills) != 2 || !skills[0].Default || !skills[1].Default {
			t.Errorf("skills = %+v, want preferred and root-skill as defaults", skills)
		}
	})

	t.Run("SKILL.md のないディレクトリがあるとき、無視される", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"skills/valid/SKILL.md":    "# Valid\n",
			"skills/invalid/README.md": "# Not a skill\n",
		})

		skills, err := skill.DiscoverSkills(os.DirFS(root))
		if err != nil {
			t.Fatalf("DiscoverSkills() error = %v", err)
		}

		if len(skills) != 1 {
			t.Fatalf("expected 1 skill, got %d", len(skills))
		}

		if skills[0].Name != "valid" {
			t.Errorf("Name = %q, want %q", skills[0].Name, "valid")
		}
	})

	t.Run("スキルのないリポジトリのとき、空が返る", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"README.md": "# Empty Repo\n",
		})

		skills, err := skill.DiscoverSkills(os.DirFS(root))
		if err != nil {
			t.Fatalf("DiscoverSkills() error = %v", err)
		}

		if len(skills) != 0 {
			t.Errorf("expected 0 skills, got %d", len(skills))
		}
	})

	t.Run("スキルの 1 階層下に別の SKILL.md があるとき、gh skill は名前空間付きとして読むので両方が既定になる", func(t *testing.T) {
		t.Parallel()
		// Arrange
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"skills/top/SKILL.md":        "# Top\n",
			"skills/top/nested/SKILL.md": "# Nested\n",
		})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if len(skills) != 2 || skills[0].Name != "top" || !skills[0].Default || skills[1].Name != "nested" || !skills[1].Default {
			t.Errorf("skills = %+v, want nested and top as defaults", skills)
		}
	})

	t.Run("スキルのディレクトリのハッシュは全ファイルを含めて計算される", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		testutil.WriteFiles(t, root, map[string]string{
			"skills/s1/SKILL.md": "# S1\n",
		})

		skills1, err := skill.DiscoverSkills(os.DirFS(root))
		if err != nil {
			t.Fatal(err)
		}

		hash1 := mustHash(t, skills1[0])

		testutil.WriteFiles(t, root, map[string]string{
			"skills/s1/extra.md": "extra content\n",
		})

		skills2, err := skill.DiscoverSkills(os.DirFS(root))
		if err != nil {
			t.Fatal(err)
		}

		hash2 := mustHash(t, skills2[0])

		if hash1 == hash2 {
			t.Error("expected hash to change when file added")
		}
	})
}

func TestComputeHashIsDeterministicAndContentSensitive(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFiles(t, root, map[string]string{
		"spec-plan/SKILL.md": "# My Skill\n",
	})
	skillPath := filepath.Join(root, "spec-plan")

	first, err := skill.ComputeHash(os.DirFS(skillPath))
	if err != nil {
		t.Fatalf("ComputeHash() error = %v", err)
	}

	second, err := skill.ComputeHash(os.DirFS(skillPath))
	if err != nil {
		t.Fatalf("ComputeHash() second call error = %v", err)
	}

	if first != second {
		t.Fatalf("ComputeHash() first = %q, second = %q", first, second)
	}

	filePath := filepath.Join(skillPath, "SKILL.md")
	if err := os.WriteFile(filePath, []byte("changed\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	third, err := skill.ComputeHash(os.DirFS(skillPath))
	if err != nil {
		t.Fatalf("ComputeHash() third call error = %v", err)
	}

	if third == first {
		t.Fatalf("ComputeHash() after change = %q, want different from %q", third, first)
	}
}

func TestComputeHashMatchesVercel(t *testing.T) {
	t.Run("スキルが大文字小文字混在の名前、.git、node_modules、シンボリックリンクを含むとき、ハッシュは vercel-labs/skills の computedHash と一致する", func(t *testing.T) {
		// Arrange
		dir := t.TempDir()
		for name, content := range map[string]string{
			"a.md":           "lower\n",
			"B.md":           "upper\n",
			"sub/c.md":       "c\n",
			".git/x":         "x\n",
			"node_modules/y": "y\n",
			"SKILL.md":       "---\nname: s\n---\n",
		} {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		if err := os.Symlink("a.md", filepath.Join(dir, "link.md")); err != nil {
			t.Fatal(err)
		}

		// Act
		got, err := skill.ComputeHash(os.DirFS(dir))
		// Assert
		if err != nil {
			t.Fatal(err)
		}
		// 同じツリーに対する vercel-labs/skills 1.7.1 の computeSkillFolderHash
		if want := "1e24b5bad76e4407e826d1f77a4fc083e12624207ac199d5a94efe24218e6959"; got != want {
			t.Errorf("ComputeHash() = %s, want %s", got, want)
		}
	})
}
