package skill_test

import (
	"path/filepath"
	"testing"

	"github.com/h3y6e/skolar/internal/lock"
	"github.com/h3y6e/skolar/internal/skill"
)

func TestParseSource(t *testing.T) {
	relativeAbs, err := filepath.Abs("../spec-skills")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		raw     string
		want    skill.SourceRef
		wantErr bool
	}{
		{
			name: "GitHub の省略記法",
			raw:  "h3y6e/spec-skills",
			want: skill.SourceRef{
				Raw:             "h3y6e/spec-skills",
				SourceType:      "github",
				CanonicalSource: "h3y6e/spec-skills",
				CloneURL:        "https://github.com/h3y6e/spec-skills.git",
			},
		},
		{
			name: "ref 付きの GitHub の省略記法",
			raw:  "h3y6e/spec-skills#feature/install",
			want: skill.SourceRef{
				Raw:             "h3y6e/spec-skills#feature/install",
				SourceType:      "github",
				CanonicalSource: "h3y6e/spec-skills",
				CloneURL:        "https://github.com/h3y6e/spec-skills.git",
				Ref:             "feature/install",
			},
		},
		{
			name: "GitHub の https URL",
			raw:  "https://github.com/h3y6e/spec-skills",
			want: skill.SourceRef{
				Raw:             "https://github.com/h3y6e/spec-skills",
				SourceType:      "github",
				CanonicalSource: "h3y6e/spec-skills",
				CloneURL:        "https://github.com/h3y6e/spec-skills.git",
			},
		},
		{
			name: "ref 付きの GitHub の https URL",
			raw:  "https://github.com/h3y6e/spec-skills.git#release-2026",
			want: skill.SourceRef{
				Raw:             "https://github.com/h3y6e/spec-skills.git#release-2026",
				SourceType:      "github",
				CanonicalSource: "h3y6e/spec-skills",
				CloneURL:        "https://github.com/h3y6e/spec-skills.git",
				Ref:             "release-2026",
			},
		},
		{
			name: "GitLab の https URL",
			raw:  "https://gitlab.com/h3y6e/repo",
			want: skill.SourceRef{
				Raw:             "https://gitlab.com/h3y6e/repo",
				SourceType:      "gitlab",
				CanonicalSource: "h3y6e/repo",
				CloneURL:        "https://gitlab.com/h3y6e/repo.git",
			},
		},
		{
			name: "ref 付きの GitLab の https URL",
			raw:  "https://gitlab.com/h3y6e/repo#feature/install",
			want: skill.SourceRef{
				Raw:             "https://gitlab.com/h3y6e/repo#feature/install",
				SourceType:      "gitlab",
				CanonicalSource: "h3y6e/repo",
				CloneURL:        "https://gitlab.com/h3y6e/repo.git",
				Ref:             "feature/install",
			},
		},
		{
			name: "GitHub の ssh URL",
			raw:  "git@github.com:h3y6e/spec-skills.git",
			want: skill.SourceRef{
				Raw:             "git@github.com:h3y6e/spec-skills.git",
				SourceType:      "github",
				CanonicalSource: "h3y6e/spec-skills",
				CloneURL:        "git@github.com:h3y6e/spec-skills.git",
			},
		},
		{
			name: "ref 付きの GitHub の ssh URL",
			raw:  "git@github.com:h3y6e/spec-skills.git#feature/install",
			want: skill.SourceRef{
				Raw:             "git@github.com:h3y6e/spec-skills.git#feature/install",
				SourceType:      "github",
				CanonicalSource: "h3y6e/spec-skills",
				CloneURL:        "git@github.com:h3y6e/spec-skills.git",
				Ref:             "feature/install",
			},
		},
		{
			name: "一般的な git の URL",
			raw:  "ssh://git@example.com/team/skills.git",
			want: skill.SourceRef{
				Raw:             "ssh://git@example.com/team/skills.git",
				SourceType:      "git",
				CanonicalSource: "ssh://git@example.com/team/skills.git",
				CloneURL:        "ssh://git@example.com/team/skills.git",
			},
		},
		{
			name: "ref 付きの一般的な git の URL",
			raw:  "ssh://git@example.com/team/skills.git#release-2026",
			want: skill.SourceRef{
				Raw:             "ssh://git@example.com/team/skills.git#release-2026",
				SourceType:      "git",
				CanonicalSource: "ssh://git@example.com/team/skills.git",
				CloneURL:        "ssh://git@example.com/team/skills.git",
				Ref:             "release-2026",
			},
		},
		{
			name: "セルフホストの https URL",
			raw:  "https://gitea.example.com/org/repo",
			want: skill.SourceRef{
				Raw:             "https://gitea.example.com/org/repo",
				SourceType:      "git",
				CanonicalSource: "https://gitea.example.com/org/repo",
				CloneURL:        "https://gitea.example.com/org/repo",
			},
		},
		{
			name:    "不正なソース",
			raw:     "not a source",
			wantErr: true,
		},
		{
			name:    "GitHub の blob URL は未対応",
			raw:     "https://github.com/h3y6e/spec-skills/blob/main/README.md#L10",
			wantErr: true,
		},
		{
			name: "ローカルテスト用の file URL",
			raw:  "file:///tmp/bare-repo",
			want: skill.SourceRef{
				Raw:             "file:///tmp/bare-repo",
				SourceType:      "local",
				CanonicalSource: "file:///tmp/bare-repo",
				CloneURL:        "file:///tmp/bare-repo",
			},
		},
		{
			name: "ref 付きの file URL",
			raw:  "file:///tmp/bare-repo#topic",
			want: skill.SourceRef{
				Raw:             "file:///tmp/bare-repo#topic",
				SourceType:      "local",
				CanonicalSource: "file:///tmp/bare-repo",
				CloneURL:        "file:///tmp/bare-repo",
				Ref:             "topic",
			},
		},
		{
			name: "ローカルの絶対パス",
			raw:  "/tmp/spec-skills",
			want: skill.SourceRef{
				Raw:             "/tmp/spec-skills",
				SourceType:      "local",
				CanonicalSource: "/tmp/spec-skills",
				CloneURL:        "/tmp/spec-skills",
			},
		},
		{
			name: "ローカルの相対パス (作業ディレクトリから clone する)",
			raw:  "../spec-skills#feature/install",
			want: skill.SourceRef{
				Raw:             "../spec-skills#feature/install",
				SourceType:      "local",
				CanonicalSource: "../spec-skills",
				CloneURL:        relativeAbs,
				Ref:             "feature/install",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := skill.ParseSource(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ParseSource() error = nil, want non-nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseSource() error = %v", err)
			}

			if got != tt.want {
				t.Fatalf("ParseSource() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSourceInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		entry lock.Entry
		want  string
	}{
		{"エントリが GitLab のソースのとき、入力は gitlab.com を指す", lock.Entry{Source: "team/repo", SourceType: "gitlab", Ref: "v1"}, "https://gitlab.com/team/repo#v1"},
		{"エントリが GitHub のソースのとき、入力はその省略記法になる", lock.Entry{Source: "o/r", SourceType: "github"}, "o/r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Act
			got := skill.SourceInput(tc.entry)

			// Assert
			if got != tc.want {
				t.Errorf("SourceInput() = %q, want %q", got, tc.want)
			}

			if ref, err := skill.ParseSource(got); err != nil || ref.SourceType != tc.entry.SourceType {
				t.Errorf("ParseSource(%q) = %+v, %v; want source type %s", got, ref, err, tc.entry.SourceType)
			}
		})
	}
}
