package lock_test

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/h3y6e/skolar/internal/lock"
)

//go:embed testdata/skills-lock.json
var lockFixture []byte

func TestLockPathRelativeAlwaysAtCWD(t *testing.T) {
	cases := []string{
		filepath.Join(".agents", "skills"),
		filepath.Join("a", "b"),
		filepath.Join("a", "b", "c"),
		filepath.Join("a", "b", "c", "d"),
	}
	for _, skillsDir := range cases {
		l := lock.NewLayout(skillsDir)
		if got := l.LockPath(); got != lock.FileName {
			t.Errorf("LockPath(%q) = %q, want %q", skillsDir, got, lock.FileName)
		}
	}
}

func TestReadWriteFileRoundTrip(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
	path := lock.FilePath(skillsDir)

	want := lock.File{
		Version: 1,
		Skills: map[string]lock.Entry{
			"spec-plan": {
				Source:       "h3y6e/spec-skills",
				SourceType:   "github",
				ComputedHash: "abc123",
			},
		},
	}

	if err := lock.WriteFile(path, want); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := lock.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if got.Version != want.Version {
		t.Fatalf("Version = %d, want %d", got.Version, want.Version)
	}

	if got.Skills["spec-plan"] != want.Skills["spec-plan"] {
		t.Fatalf("entry = %#v, want %#v", got.Skills["spec-plan"], want.Skills["spec-plan"])
	}
}

func TestReadFileFixture(t *testing.T) {
	skillsDir := filepath.Join(t.TempDir(), ".agents", "skills")
	path := lock.FilePath(skillsDir)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if err := os.WriteFile(path, lockFixture, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := lock.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if got.Version != 1 {
		t.Fatalf("Version = %d, want 1", got.Version)
	}

	tests := []struct {
		name  string
		entry lock.Entry
	}{
		{
			name: "skill-github",
			entry: lock.Entry{
				Source:       "h3y6e/spec-skills",
				SourceType:   "github",
				ComputedHash: "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
			},
		},
		{
			name: "skill-gitlab",
			entry: lock.Entry{
				Source:       "h3y6e/repo",
				SourceType:   "gitlab",
				ComputedHash: "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3",
			},
		},
		{
			name: "skill-git",
			entry: lock.Entry{
				Source:       "https://git.example.com/team/skills.git",
				SourceType:   "git",
				ComputedHash: "c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4",
			},
		},
		{
			name: "skill-local",
			entry: lock.Entry{
				Source:       "/home/user/my-skills",
				SourceType:   "local",
				ComputedHash: "d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5",
			},
		},
		{
			name: "skill-well-known",
			entry: lock.Entry{
				Source:       "mintlify/docs",
				SourceType:   "well-known",
				ComputedHash: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
		},
		{
			name: "skill-node-modules",
			entry: lock.Entry{
				Source:       "my-npm-pkg",
				SourceType:   "node_modules",
				ComputedHash: "7f83b1657ff1fc53b92dc18148a1d65dfc2d4b1fa3d677284addd200126d9069",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := got.Skills[tt.name]
			if !ok {
				t.Fatalf("entry %q missing", tt.name)
			}

			if got != tt.entry {
				t.Fatalf("entry = %#v, want %#v", got, tt.entry)
			}
		})
	}

	raw, _ := json.Marshal(got)

	var roundTrip lock.File
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("JSON round-trip failed: %v", err)
	}
}

func TestLayoutUsesLockfile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, dir string
		want      bool
	}{
		{"ディレクトリがデフォルトのとき、ロックファイルはそれを記録する", lock.DefaultDir, true},
		{"ディレクトリが絶対パスでデフォルトで終わるとき、隣のロックファイルはそれを記録する", "/home/u/.agents/skills", true},
		{"ディレクトリがカスタムのとき、ロックファイルはそれを記録しない", "dot_agents/exact_skills", false},
		{"絶対パスのデフォルトディレクトリが末尾スラッシュで終わるとき、隣のロックファイルはそれを記録する", "/home/u/.agents/skills/", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Arrange
			layout := lock.NewLayout(tc.dir)

			// Act
			got := layout.UsesLockfile()

			// Assert
			if got != tc.want {
				t.Errorf("UsesLockfile() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWriteFileMode(t *testing.T) {
	t.Parallel()

	t.Run("ロックファイルを書き込んだとき、コミット済みファイルのように他者が読める", func(t *testing.T) {
		t.Parallel()
		// Arrange
		path := filepath.Join(t.TempDir(), lock.FileName)

		// Act
		err := lock.WriteFile(path, lock.File{Version: 1})
		// Assert
		if err != nil {
			t.Fatal(err)
		}

		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		if got := info.Mode().Perm(); got != 0o644 {
			t.Errorf("mode = %v, want 0644", got)
		}
	})
}
