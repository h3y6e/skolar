//go:build interop

package interop

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type file struct{ Mode, Link, Data string }

func snapshot(t *testing.T, dir string) map[string]file {
	t.Helper()

	files := map[string]file{}

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(dir, path)
		e := file{Mode: info.Mode().String()}

		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}

			e.Link = target
		case !d.IsDir():
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}

			e.Data = string(b)
		}

		files[filepath.ToSlash(rel)] = e

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return files
}

func assertSameFiles(t *testing.T, want, got map[string]file) {
	t.Helper()

	for _, k := range slices.Sorted(maps.Keys(want)) {
		if g, ok := got[k]; !ok {
			t.Errorf("%s がない", k)
		} else if g != want[k] {
			t.Errorf("%s が異なる\nwant:\n%+v\ngot:\n%+v", k, want[k], g)
		}
	}

	for _, k := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[k]; !ok {
			t.Errorf("想定外の %s", k)
		}
	}
}

func subtree(files map[string]file, prefix string) map[string]file {
	out := map[string]file{}

	for k, v := range files {
		if rest, ok := strings.CutPrefix(k, prefix+"/"); ok {
			out[rest] = v
		}
	}

	return out
}

type updateResult struct {
	SkillName    string `json:"skillName"`
	Status       string `json:"status"`
	Path         string `json:"path"`
	PreviousPath string `json:"previousPath"`
	Reason       string `json:"reason"`
}

func planUpdate(t *testing.T, dest string) []updateResult {
	t.Helper()

	var record struct {
		Results []updateResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(run(t, filepath.Dir(filepath.Dir(dest)), skolar, "update", "--dry-run", "-d", dest, "--json")), &record); err != nil {
		t.Fatal(err)
	}

	installed := slices.DeleteFunc(record.Results, func(r updateResult) bool { return r.Status == "new" })

	var planned []string
	for _, r := range installed {
		planned = append(planned, r.SkillName)
	}

	if names := skillNames(t, dest); !slices.Equal(slices.Sorted(slices.Values(planned)), names) {
		t.Fatalf("skolar update の対象は %v だが、インストール済みの全スキル %v が対象のはず", planned, names)
	}

	return installed
}

func assertAllUpToDate(t *testing.T, dest string) {
	t.Helper()

	for _, r := range planUpdate(t, dest) {
		if r.Status != "up-to-date" {
			t.Errorf("%s: %s %s", r.SkillName, r.Status, r.Reason)
		}
	}
}

func assertMoved(t *testing.T, dest, name, from, to string) {
	t.Helper()

	for _, r := range planUpdate(t, dest) {
		switch {
		case r.SkillName == name && (r.Status != "moved" || r.PreviousPath != from || r.Path != to):
			t.Errorf("%s: %s (%q から %q)。%q から %q への moved のはず", name, r.Status, r.PreviousPath, r.Path, from, to)
		case r.SkillName != name && r.Status != "up-to-date":
			t.Errorf("%s: %s %s", r.SkillName, r.Status, r.Reason)
		}
	}
}

func TestVercelFormat(t *testing.T) {
	t.Run("検出: vercel がスキルをインストールした状態で、skolar は最新と報告して update は何も変更しない", func(t *testing.T) {
		// Arrange
		project := t.TempDir()
		vercelAdd(t, project)
		before := snapshot(t, project)

		// Act
		assertAllUpToDate(t, filepath.Join(project, ".agents", "skills"))
		run(t, project, skolar, "update", "-y")

		// Assert
		assertSameFiles(t, before, snapshot(t, project))
	})

	t.Run("skillPath なしの検出: ロックエントリに skillPath がなくても、skolar は vercel がインストールした全スキルを見つける", func(t *testing.T) {
		// Arrange
		project := t.TempDir()
		vercelAdd(t, project)
		editLock(t, project, func(entries map[string]map[string]any) {
			for _, e := range entries {
				delete(e, "skillPath")
			}
		})

		// Act & Assert
		assertAllUpToDate(t, filepath.Join(project, ".agents", "skills"))
	})

	t.Run("移動の追跡: skillPath が上流になくなったとき、skolar は移動を報告して update は vercel が書くロックエントリに戻す", func(t *testing.T) {
		// Arrange
		project := t.TempDir()
		vercelAdd(t, project)
		before := snapshot(t, project)
		editLock(t, project, func(entries map[string]map[string]any) {
			entries["alpha"]["skillPath"] = "moved/alpha/SKILL.md"
		})
		dest := filepath.Join(project, ".agents", "skills")

		// Act
		assertMoved(t, dest, "alpha", "moved/alpha", "skills/alpha")
		run(t, project, skolar, "update", "-y")

		// Assert
		assertSameFiles(t, before, snapshot(t, project))
	})

	t.Run("インストール: ロックファイルのあるプロジェクトに vercel と同じスキルを skolar がインストールすると、ファイルとロックエントリが一致して vercel update は何も変更しない", func(t *testing.T) {
		// Arrange
		vercel, own := t.TempDir(), t.TempDir()
		vercelAdd(t, vercel)

		if err := os.WriteFile(filepath.Join(own, "skills-lock.json"), []byte(`{"version":1,"skills":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}

		args := []string{"add", fixtureURL, "-y"}
		for _, name := range skillNames(t, filepath.Join(vercel, ".agents", "skills")) {
			args = append(args, "-s", name)
		}

		// Act
		run(t, own, skolar, args...)
		installed := snapshot(t, own)
		run(t, own, "skills", "update", "-p", "-y")

		// Assert
		want := snapshot(t, vercel)
		for _, name := range skillNames(t, filepath.Join(vercel, ".agents", "skills")) {
			dir := ".agents/skills/" + name
			assertSameFiles(t, subtree(want, dir), subtree(installed, dir))
		}

		var wantLock, gotLock struct {
			Skills map[string]json.RawMessage `json:"skills"`
		}
		if err := json.Unmarshal([]byte(want["skills-lock.json"].Data), &wantLock); err != nil {
			t.Fatal(err)
		}

		if err := json.Unmarshal([]byte(installed["skills-lock.json"].Data), &gotLock); err != nil {
			t.Fatal(err)
		}

		for name, entry := range wantLock.Skills {
			got, ok := gotLock.Skills[name]
			if !ok {
				t.Errorf("ロックエントリ %s がない", name)
				continue
			}

			if !bytes.Equal(compactJSON(t, entry), compactJSON(t, got)) {
				t.Errorf("ロックエントリ %s\nwant: %s\ngot:  %s", name, entry, got)
			}
		}

		assertSameFiles(t, installed, snapshot(t, own))
	})
}

func editLock(t *testing.T, project string, edit func(entries map[string]map[string]any)) {
	t.Helper()

	path := filepath.Join(project, "skills-lock.json")

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var lf struct {
		Version int                       `json:"version"`
		Skills  map[string]map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(b, &lf); err != nil {
		t.Fatal(err)
	}

	edit(lf.Skills)

	b, err = json.MarshalIndent(lf, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func compactJSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()

	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatal(err)
	}

	return b.Bytes()
}

func TestGhFormat(t *testing.T) {
	t.Run("検出: gh がスキルをインストールした状態で、skolar は最新と報告して update は何も変更しない", func(t *testing.T) {
		// Arrange
		dest := filepath.Join(t.TempDir(), "skills")
		ghInstall(t, dest, "--all")
		before := snapshot(t, dest)

		// Act
		assertAllUpToDate(t, dest)
		run(t, filepath.Dir(dest), skolar, "update", "-d", dest, "-y")

		// Assert
		assertSameFiles(t, before, snapshot(t, dest))
	})

	t.Run("移動の追跡: github-path が上流になくなったとき、skolar は移動を報告して update は gh がインストールするファイルに戻す", func(t *testing.T) {
		// Arrange
		dest := filepath.Join(t.TempDir(), "skills")
		ghInstall(t, dest, "alpha")
		before := snapshot(t, dest)
		skillMD := filepath.Join(dest, "alpha", "SKILL.md")

		data, err := os.ReadFile(skillMD)
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(skillMD, []byte(strings.Replace(string(data), "github-path: skills/alpha", "github-path: moved/alpha", 1)), 0o644); err != nil {
			t.Fatal(err)
		}

		// Act
		assertMoved(t, dest, "alpha", "moved/alpha", "skills/alpha")
		run(t, filepath.Dir(dest), skolar, "update", "-d", dest, "-y")

		// Assert
		assertSameFiles(t, before, snapshot(t, dest))
	})

	t.Run("インストール: ロックファイルなしで skolar がインストールすると、ファイルは gh のインストールと一致して gh update は何も変更しない", func(t *testing.T) {
		// Arrange
		gh, own := filepath.Join(t.TempDir(), "skills"), filepath.Join(t.TempDir(), "skills")
		ghInstall(t, gh, "--all")

		// Act
		run(t, filepath.Dir(own), skolar, "add", fixtureRepo, "-d", own, "-y")
		installed := snapshot(t, own)
		ghUpdate(t, homeOf(t), own, "--all")

		// Assert
		want := snapshot(t, gh)
		for _, name := range skillNames(t, gh) {
			assertSameFiles(t, subtree(want, name), subtree(installed, name))
		}

		assertSameFiles(t, installed, snapshot(t, own))
	})

	t.Run("更新: 固定なしのインストールが最新リリースより古いとき、skolar update は gh update と同じファイルになる", func(t *testing.T) {
		// Arrange
		gh, own := filepath.Join(t.TempDir(), "skills"), filepath.Join(t.TempDir(), "skills")
		for _, dest := range []string{gh, own} {
			ghInstall(t, dest, "alpha", "--pin", "v1.0.0")
			unpin(t, filepath.Join(dest, "alpha", "SKILL.md"))
		}

		// Act
		ghUpdate(t, homeOf(t), gh, "--all")
		run(t, filepath.Dir(own), skolar, "update", "-d", own, "-y")

		// Assert
		assertSameFiles(t, snapshot(t, gh), snapshot(t, own))
	})

	t.Run("引き渡し: skolar が gh のインストールを更新し上流が変わらないとき、gh update は何も変更しない", func(t *testing.T) {
		// Arrange
		dest := filepath.Join(t.TempDir(), "skills")
		ghInstall(t, dest, "alpha", "--pin", "v1.0.0")
		unpin(t, filepath.Join(dest, "alpha", "SKILL.md"))
		run(t, filepath.Dir(dest), skolar, "update", "-d", dest, "-y")
		before := snapshot(t, dest)

		// Act
		ghUpdate(t, homeOf(t), dest, "--all")

		// Assert
		assertSameFiles(t, before, snapshot(t, dest))
	})

	t.Run("再インストール: skolar が更新したスキルを gh が再インストールすると、ファイルは新規の gh インストールと一致する", func(t *testing.T) {
		// Arrange
		gh, own := filepath.Join(t.TempDir(), "skills"), filepath.Join(t.TempDir(), "skills")
		ghInstall(t, gh, "alpha")
		ghInstall(t, own, "alpha", "--pin", "v1.0.0")
		unpin(t, filepath.Join(own, "alpha", "SKILL.md"))
		run(t, filepath.Dir(own), skolar, "update", "-d", own, "-y")

		// Act
		ghUpdate(t, homeOf(t), own, "--all", "--force")

		// Assert
		assertSameFiles(t, snapshot(t, gh), snapshot(t, own))
	})
}

func unpin(t *testing.T, skillMD string) {
	t.Helper()

	b, err := os.ReadFile(skillMD)
	if err != nil {
		t.Fatal(err)
	}

	var kept []string

	for line := range strings.Lines(string(b)) {
		if !strings.Contains(line, "github-pinned:") {
			kept = append(kept, line)
		}
	}

	if err := os.WriteFile(skillMD, []byte(strings.Join(kept, "")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGhUndiscoverableLayouts(t *testing.T) {
	for _, tc := range []struct{ skill, path string }{
		{"root-skill", "."},
		{"theta", "extras/theta"},
	} {
		t.Run("検出不能なパス: skolar が "+tc.skill+" を gh 形式で記録すると、メタデータは実際のパスを保持して gh が "+tc.path+" を検出できるようになるまで gh update は古いインストールに触れない", func(t *testing.T) {
			// Arrange
			dir := filepath.Join(t.TempDir(), "skills")
			run(t, filepath.Dir(dir), skolar, "add", fixtureRepo+"#v1.0.0", "-s", tc.skill, "-d", dir, "-y")
			skillMD := filepath.Join(dir, tc.skill, "SKILL.md")
			unpin(t, skillMD)
			before := snapshot(t, dir)

			// Act
			ghUpdate(t, homeOf(t), dir, "--all")

			// Assert
			if got := metadataValue(before[tc.skill+"/SKILL.md"].Data, "github-path"); got != tc.path {
				t.Errorf("github-path = %q、want %q", got, tc.path)
			}

			assertSameFiles(t, before, snapshot(t, dir))
		})
	}
}

func metadataValue(skillMD, key string) string {
	for line := range strings.Lines(skillMD) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}

	return ""
}

func TestRecordChoice(t *testing.T) {
	t.Run("ロックファイルのあるプロジェクトで独自ディレクトリにスキルを追加すると、skolar は gh メタデータに記録してロックファイルは変更しない", func(t *testing.T) {
		// Arrange
		project := t.TempDir()
		vercelAdd(t, project)
		lockBefore := snapshot(t, project)["skills-lock.json"]
		dir := filepath.Join(project, "dot_agents", "exact_skills")

		// Act
		run(t, project, skolar, "add", fixtureRepo, "-s", "alpha", "-d", dir, "-y")

		// Assert
		if got := metadataValue(snapshot(t, dir)["alpha/SKILL.md"].Data, "github-repo"); got != fixtureURL {
			t.Errorf("github-repo = %q、want %q", got, fixtureURL)
		}

		if got := snapshot(t, project)["skills-lock.json"]; got != lockBefore {
			t.Errorf("skills-lock.json が変化した:\n%s", got)
		}
	})

	t.Run("独自ディレクトリへ gh メタデータで記録できないソースを追加すると、失敗し何も書き込まない", func(t *testing.T) {
		// Arrange
		src := t.TempDir()
		run(t, src, "git", "init", "-q")

		if err := os.MkdirAll(filepath.Join(src, "skills", "local"), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(src, "skills", "local", "SKILL.md"), []byte("---\nname: local\ndescription: d\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		run(t, src, "git", "add", "-A")
		run(t, src, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init")
		project := t.TempDir()
		dir := filepath.Join(project, "custom", "skills")

		// Act
		cmd := exec.CommandContext(t.Context(), skolar, "add", "file://"+src, "-d", dir, "-y")
		cmd.Dir = project
		out, err := cmd.CombinedOutput()

		// Assert
		if err == nil {
			t.Fatalf("skolar add が成功した:\n%s", out)
		}

		if files := snapshot(t, project); len(files) != 0 {
			t.Errorf("skolar add がファイルを書き込んだ: %v", slices.Sorted(maps.Keys(files)))
		}
	})
}
