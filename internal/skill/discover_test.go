package skill_test

import (
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/h3y6e/skolar/internal/skill"
)

func writeRepo(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func fm(name string) string { return "---\nname: " + name + "\ndescription: d\n---\n" }

var mixedLayouts = map[string]string{
	"skills/alpha/SKILL.md":           fm("alpha"),
	"skills/team/beta/SKILL.md":       fm("beta"),
	"skills/.curated/gamma/SKILL.md":  fm("gamma"),
	".claude/skills/delta/SKILL.md":   fm("delta"),
	"zeta/SKILL.md":                   fm("zeta"),
	"plugins/p/skills/eta/SKILL.md":   fm("eta"),
	"extras/theta/SKILL.md":           fm("theta"),
	".claude-plugin/marketplace.json": `{"plugins": [{"name": "p", "source": "./plugins/p", "skills": ["./skills/eta"]}]}`,
}

func discoveredPaths(t *testing.T, skills []skill.DiscoveredSkill, defaultOnly bool) map[string]string {
	t.Helper()

	got := map[string]string{}

	for _, s := range skills {
		if !defaultOnly || s.Default {
			got[s.Name] = s.Path
		}
	}

	return got
}

func TestDiscoverSkillsLayouts(t *testing.T) {
	t.Parallel()

	t.Run("リポジトリがどちらのツールが対応するレイアウトも含むとき、すべてのスキルがリポジトリ内のパスで見つかる", func(t *testing.T) {
		t.Parallel()
		// Arrange
		files := map[string]string{"SKILL.md": fm("root-skill")}
		maps.Copy(files, mixedLayouts)
		root := writeRepo(t, files)

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		want := map[string]string{
			"root-skill": ".", "alpha": "skills/alpha", "beta": "skills/team/beta", "gamma": "skills/.curated/gamma",
			"delta": ".claude/skills/delta", "zeta": "zeta", "eta": "plugins/p/skills/eta", "theta": "extras/theta",
		}
		if got := discoveredPaths(t, skills, false); !maps.Equal(got, want) {
			t.Errorf("DiscoverSkills() = %v, want %v", got, want)
		}
	})

	t.Run("ルートに SKILL.md があるとき、既定はルートのスキルと gh skill のレイアウトになる", func(t *testing.T) {
		t.Parallel()
		// Arrange
		files := map[string]string{"SKILL.md": fm("root-skill")}
		maps.Copy(files, mixedLayouts)
		root := writeRepo(t, files)

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		want := map[string]string{"root-skill": ".", "alpha": "skills/alpha", "beta": "skills/team/beta", "zeta": "zeta", "eta": "plugins/p/skills/eta"}
		if got := discoveredPaths(t, skills, true); !maps.Equal(got, want) {
			t.Errorf("defaults = %v, want %v", got, want)
		}
	})

	t.Run("ルートに SKILL.md がないとき、既定は vercel-labs/skills の探索ディレクトリに従う", func(t *testing.T) {
		t.Parallel()
		// Arrange
		root := writeRepo(t, mixedLayouts)

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		want := map[string]string{
			"alpha": "skills/alpha", "beta": "skills/team/beta", "gamma": "skills/.curated/gamma",
			"delta": ".claude/skills/delta", "zeta": "zeta", "eta": "plugins/p/skills/eta",
		}
		if got := discoveredPaths(t, skills, true); !maps.Equal(got, want) {
			t.Errorf("defaults = %v, want %v", got, want)
		}
	})

	t.Run("2 つのディレクトリが同じ名前を宣言するとき、vercel-labs/skills が先に探索するほうが採られる", func(t *testing.T) {
		t.Parallel()
		// Arrange
		root := writeRepo(t, map[string]string{
			"skills/dup/SKILL.md":         fm("dup"),
			".claude/skills/dup/SKILL.md": fm("dup"),
		})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := discoveredPaths(t, skill.FilterDiscoveredSkills(skills, []string{"dup"}), false); !maps.Equal(got, map[string]string{"dup": "skills/dup"}) {
			t.Errorf("FilterDiscoveredSkills() = %v", got)
		}
	})

	t.Run("SKILL.md のフロントマターに name がないとき、スキルはディレクトリ名で呼ばれる", func(t *testing.T) {
		t.Parallel()
		// Arrange
		root := writeRepo(t, map[string]string{"skills/plain/SKILL.md": "# Plain\n"})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := discoveredPaths(t, skills, true); !maps.Equal(got, map[string]string{"plain": "skills/plain"}) {
			t.Errorf("DiscoverSkills() = %v", got)
		}
	})
}

func TestDiscoverSkillsEdges(t *testing.T) {
	t.Run("スキルのディレクトリがスキップ対象のディレクトリと同名のとき、それでも見つかる", func(t *testing.T) {
		t.Parallel()
		// Arrange
		root := writeRepo(t, map[string]string{"skills/build/SKILL.md": fm("build"), "skills/build/dist/SKILL.md": fm("hidden")})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := discoveredPaths(t, skills, false); !maps.Equal(got, map[string]string{"build": "skills/build"}) {
			t.Errorf("DiscoverSkills() = %v", got)
		}
	})

	t.Run("1 つの SKILL.md のフロントマターが不正なとき、ほかのスキルは見つかる", func(t *testing.T) {
		t.Parallel()
		// Arrange
		root := writeRepo(t, map[string]string{"skills/good/SKILL.md": fm("good"), "extras/bad/SKILL.md": "---\nname: [\n---\n"})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := discoveredPaths(t, skills, false); !maps.Equal(got, map[string]string{"good": "skills/good"}) {
			t.Errorf("DiscoverSkills() = %v", got)
		}
	})

	t.Run("SKILL.md の name がスキル名でなくパスのとき、スキップされる", func(t *testing.T) {
		t.Parallel()
		// Arrange
		root := writeRepo(t, map[string]string{
			"skills/good/SKILL.md": fm("good"),
			"skills/up/SKILL.md":   fm("../../escape"),
			"skills/abs/SKILL.md":  fm("/tmp/escape"),
			"skills/deep/SKILL.md": fm("a/b"),
			"skills/dot/SKILL.md":  fm("."),
		})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := discoveredPaths(t, skills, false); !maps.Equal(got, map[string]string{"good": "skills/good"}) {
			t.Errorf("DiscoverSkills() = %v", got)
		}
	})

	t.Run("SKILL.md がシンボリックリンクのとき、そのディレクトリはスキルにならない", func(t *testing.T) {
		t.Parallel()
		// Arrange
		root := writeRepo(t, map[string]string{"skills/good/SKILL.md": fm("good"), "elsewhere.md": fm("link")})
		if err := os.MkdirAll(filepath.Join(root, "skills", "link"), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(filepath.Join(root, "elsewhere.md"), filepath.Join(root, "skills", "link", "SKILL.md")); err != nil {
			t.Fatal(err)
		}

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if got := discoveredPaths(t, skills, false); !maps.Equal(got, map[string]string{"good": "skills/good"}) {
			t.Errorf("DiscoverSkills() = %v", got)
		}
	})

	t.Run("vercel-labs/skills だけが見つけるスキルが internal と記されているとき、見つかるが既定にはならない", func(t *testing.T) {
		// Arrange
		t.Setenv("INSTALL_INTERNAL_SKILLS", "")
		root := writeRepo(t, map[string]string{".claude/skills/secret/SKILL.md": "---\nname: secret\ndescription: d\nmetadata:\n  internal: true\n---\n"})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if len(skills) != 1 || skills[0].Default {
			t.Errorf("skills = %+v, want secret found but not a default", skills)
		}
	})

	t.Run("INSTALL_INTERNAL_SKILLS で有効にしたとき、internal のスキルが既定になる", func(t *testing.T) {
		// Arrange
		t.Setenv("INSTALL_INTERNAL_SKILLS", "1")
		root := writeRepo(t, map[string]string{".claude/skills/secret/SKILL.md": "---\nname: secret\ndescription: d\nmetadata:\n  internal: true\n---\n"})

		// Act
		skills, err := skill.DiscoverSkills(os.DirFS(root))
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		if len(skills) != 1 || !skills[0].Default {
			t.Errorf("skills = %+v, want secret as a default", skills)
		}
	})
}
