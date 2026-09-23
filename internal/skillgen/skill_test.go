package skillgen_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/skillgen"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		skill     skillgen.Skill
		wantFiles []string
		wantDirs  []string
		wantErr   string // the message after "jev: <path>: "
	}{
		{
			name:  "should accept a minimal valid skill",
			skill: skillgen.Skill{Name: "my-skill", Description: "does a thing"},
		},
		{
			name:    "should reject a name that does not match the directory",
			skill:   skillgen.Skill{Name: "other-name", Description: "does a thing"},
			wantErr: "name 'other-name' must equal the directory name 'my-skill'",
		},
		{
			name:  "should reject a name over 64 characters",
			skill: skillgen.Skill{Name: strings.Repeat("a", 65), Description: "does a thing"},
			wantErr: "name '" + strings.Repeat("a", 65) +
				"' must be at most 64 characters",
		},
		{
			name:  "should reject a name with an uppercase letter",
			skill: skillgen.Skill{Name: "My-Skill", Description: "does a thing"},
			wantErr: "name 'My-Skill' must be lowercase letters, digits and " +
				"single hyphens, with no leading, trailing or consecutive hyphen",
		},
		{
			name:  "should reject a name with a leading hyphen",
			skill: skillgen.Skill{Name: "-my-skill", Description: "does a thing"},
			wantErr: "name '-my-skill' must be lowercase letters, digits and " +
				"single hyphens, with no leading, trailing or consecutive hyphen",
		},
		{
			name:  "should reject a name with a trailing hyphen",
			skill: skillgen.Skill{Name: "my-skill-", Description: "does a thing"},
			wantErr: "name 'my-skill-' must be lowercase letters, digits and " +
				"single hyphens, with no leading, trailing or consecutive hyphen",
		},
		{
			name:  "should reject a name with consecutive hyphens",
			skill: skillgen.Skill{Name: "my--skill", Description: "does a thing"},
			wantErr: "name 'my--skill' must be lowercase letters, digits and " +
				"single hyphens, with no leading, trailing or consecutive hyphen",
		},
		{
			name:    "should reject a missing name",
			skill:   skillgen.Skill{Description: "does a thing"},
			wantErr: "name is required",
		},
		{
			name:    "should reject an empty name",
			skill:   skillgen.Skill{Name: "", Description: "does a thing"},
			wantErr: "name is required",
		},
		{
			name:    "should reject an empty description",
			skill:   skillgen.Skill{Name: "my-skill"},
			wantErr: "description is required",
		},
		{
			name:    "should reject a description over 1024 characters",
			skill:   skillgen.Skill{Name: "my-skill", Description: strings.Repeat("a", 1025)},
			wantErr: "description must be at most 1024 characters",
		},
		{
			name: "should accept a description within 1024 characters when it exceeds 1024 bytes",
			skill: skillgen.Skill{
				Name: "my-skill", Description: strings.Repeat("é", 600),
			},
		},
		{
			name:    "should reject a description containing a control character",
			skill:   skillgen.Skill{Name: "my-skill", Description: "does a thing\x01"},
			wantErr: "description has a control character",
		},
		{
			name:  "should accept a description containing a newline and a tab",
			skill: skillgen.Skill{Name: "my-skill", Description: "does a thing\nwith a\ttab"},
		},
		{
			name: "should reject a metadata value containing a control character",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Metadata: map[string]string{"version": "0.1.0\x01"},
			},
			wantErr: "metadata 'version' has a control character",
		},
		{
			name: "should reject a rule missing id",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{{Short: "do it", Why: "because"}},
			},
			wantErr: "rules[0]: id is required",
		},
		{
			name: "should reject a rule missing short",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{{ID: "r1", Why: "because"}},
			},
			wantErr: "rules[0] 'r1': short is required",
		},
		{
			name: "should reject a rule missing why",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{{ID: "r1", Short: "do it"}},
			},
			wantErr: "rules[0] 'r1': why is required",
		},
		{
			name: "should reject a rule with a whitespace only why",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{{ID: "r1", Short: "do it", Why: "   "}},
			},
			wantErr: "rules[0] 'r1': why is required",
		},
		{
			name: "should reject a rule short containing a control character",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{{ID: "r1", Short: "do it\x01", Why: "because"}},
			},
			wantErr: "rules[0] 'r1': short has a control character",
		},
		{
			name: "should reject a rule why containing a control character",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{{ID: "r1", Short: "do it", Why: "because\x01"}},
			},
			wantErr: "rules[0] 'r1': why has a control character",
		},
		{
			name: "should reject two rules sharing an id",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{
					{ID: "r1", Short: "do it", Why: "because"},
					{ID: "r1", Short: "do it too", Why: "also because"},
				},
			},
			wantErr: "rules[1] 'r1': id is already used by another rule",
		},
		{
			name: "should accept a rule with no bad and no good",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{{ID: "r1", Short: "do it", Why: "because"}},
			},
		},
		{
			name: "should accept a rule with good_fails set",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{
					{ID: "r1", Short: "do it", Why: "because", Good: "ok", GoodFails: true},
				},
			},
		},
		{
			name: "should default good_fails to false when absent",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{{ID: "r1", Short: "do it", Why: "because", Good: "ok"}},
			},
		},
		{
			name: "should accept a rule with bad_passes set",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{
					{ID: "r1", Short: "do it", Why: "because", Bad: "not so ok", BadPasses: true},
				},
			},
		},
		{
			name: "should default bad_passes to false when absent",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{{ID: "r1", Short: "do it", Why: "because", Bad: "not so ok"}},
			},
		},
		{
			name: "should accept a rule with bad_unverifiable set and no bad_passes",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{
					{
						ID: "r1", Short: "do it", Why: "because", Bad: "not so ok",
						BadUnverifiable: "the checker strips the flag this depends on",
					},
				},
			},
		},
		{
			name: "should accept a rule with good_unverifiable set and no good_fails",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{
					{
						ID: "r1", Short: "do it", Why: "because", Good: "ok",
						GoodUnverifiable: "the checker strips the flag this depends on",
					},
				},
			},
		},
		{
			name: "should reject a rule setting both bad_passes and bad_unverifiable",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{
					{
						ID: "r1", Short: "do it", Why: "because", Bad: "not so ok",
						BadPasses: true, BadUnverifiable: "the checker strips the flag this depends on",
					},
				},
			},
			wantErr: "rules[0] 'r1': bad_passes and bad_unverifiable contradict, keep one",
		},
		{
			name: "should reject a rule setting both good_fails and good_unverifiable",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Rules: []skillgen.Rule{
					{
						ID: "r1", Short: "do it", Why: "because", Good: "ok",
						GoodFails: true, GoodUnverifiable: "the checker strips the flag this depends on",
					},
				},
			},
			wantErr: "rules[0] 'r1': good_fails and good_unverifiable contradict, keep one",
		},
		{
			name: "should reject an intro entry naming a file that does not exist",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing", Intro: "intro.md",
			},
			wantErr: "intro 'intro.md' does not exist",
		},
		{
			name: "should reject a sections entry naming a file that does not exist",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Sections: []string{"sections/missing.md"},
			},
			wantErr: "sections[0] 'sections/missing.md' does not exist",
		},
		{
			name: "should reject a references entry naming a file that does not exist",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				References: []string{"references/missing.md"},
			},
			wantErr: "references[0] 'references/missing.md' does not exist",
		},
		{
			name: "should reject an empty sections entry",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Sections: []string{""},
			},
			wantErr: "sections[0] '' must be a relative path inside the skill directory",
		},
		{
			name: "should reject a sections entry naming a directory",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Sections: []string{"sections"},
			},
			wantDirs: []string{"sections"},
			wantErr:  "sections[0] 'sections' must be a regular file",
		},
		{
			name: "should reject a sections entry that escapes the skill directory",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Sections: []string{"../outside.md"},
			},
			wantErr: "sections[0] '../outside.md' must be a relative path inside the skill directory",
		},
		{
			name: "should accept an intro and a sections entry that exist",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Intro: "intro.md", Sections: []string{"sections/policy.md"},
			},
			wantFiles: []string{"intro.md", "sections/policy.md"},
		},
		{
			name: "should reject a sections entry listed twice",
			skill: skillgen.Skill{
				Name: "my-skill", Description: "does a thing",
				Sections: []string{"sections/a.md", "sections/a.md"},
			},
			wantFiles: []string{"sections/a.md"},
			wantErr:   "sections[1] 'sections/a.md' is already listed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "my-skill")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			for _, rel := range tc.wantDirs {
				if err := os.MkdirAll(filepath.Join(dir, rel), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", rel, err)
				}
			}

			for _, rel := range tc.wantFiles {
				full := filepath.Join(dir, rel)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
				}

				if err := os.WriteFile(full, []byte("content"), 0o600); err != nil {
					t.Fatalf("write %s: %v", rel, err)
				}
			}

			path := filepath.Join(dir, "skill.json")

			err := skillgen.Validate(path, tc.skill)

			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("expected an error, got none")
			}

			if want := "jev: " + path + ": " + tc.wantErr; err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	t.Run("should load a minimal valid skill", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "my-skill")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		path := filepath.Join(dir, "skill.json")
		body := `{"name": "my-skill", "description": "does a thing"}`

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write skill.json: %v", err)
		}

		s, err := skillgen.Load(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if s.Name != "my-skill" {
			t.Errorf("name = %q, want %q", s.Name, "my-skill")
		}
	})

	t.Run("should reject an unknown field", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "my-skill")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		path := filepath.Join(dir, "skill.json")
		body := `{"name": "my-skill", "description": "does a thing", "to_verify": true}`

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write skill.json: %v", err)
		}

		_, err := skillgen.Load(path)
		if err == nil {
			t.Fatalf("expected an error, got none")
		}

		want := "jev: " + path + ": json: unknown field \"to_verify\""
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})

	t.Run("should reject trailing content after the skill object", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "my-skill")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		path := filepath.Join(dir, "skill.json")
		body := `{"name": "my-skill", "description": "does a thing"} {"garbage": 1}`

		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write skill.json: %v", err)
		}

		_, err := skillgen.Load(path)
		if err == nil {
			t.Fatalf("expected an error, got none")
		}

		want := "jev: " + path + ": trailing content after the skill object"
		if err.Error() != want {
			t.Errorf("error = %q, want %q", err.Error(), want)
		}
	})
}
