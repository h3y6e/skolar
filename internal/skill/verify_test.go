package skill_test

import (
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/h3y6e/skolar/internal/git"
	"github.com/h3y6e/skolar/internal/skill"
)

func TestVerifyTrackedRootSkill(t *testing.T) {
	t.Parallel()

	t.Run("ルートのスキルとその内側のスキルが 1 回の clone に由来するとき、両方が検証される", func(t *testing.T) {
		t.Parallel()
		// Arrange
		repo := initGitRepo(t, map[string]string{
			"SKILL.md":              "---\nname: whole\ndescription: d\n---\n",
			"skills/alpha/SKILL.md": "# Alpha\n",
		})
		skillsDir := filepath.Join(t.TempDir(), "skills")
		ref := skill.SourceRef{CanonicalSource: "o/r", SourceType: "github"}
		installTracked(t, repo, ref, git.ResolvedRef{Full: "refs/heads/main"}, skillsDir)

		installed, err := skill.ScanInstalledSkills(skillsDir)
		if err != nil {
			t.Fatal(err)
		}

		fns := skill.UpstreamFuncs{
			FetchTree: func(_, _, tree string) (fs.FS, error) {
				return newCache(t).ReadTree(t.Context(), repo, tree)
			},
		}

		// Act
		results := skill.VerifyTracked(installed, fns)

		// Assert
		if len(results) != 2 {
			t.Fatalf("results = %+v", results)
		}

		for _, r := range results {
			if r.Status != skill.VerifyVerified {
				t.Errorf("%s: %s %v %s", r.SkillName, r.Status, r.Files, r.Reason)
			}
		}
	})
}
