package qfile_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
	"github.com/frodi-karlsson/jev-cli/internal/qfile"
)

func TestLoadBody(t *testing.T) {
	t.Parallel()

	const full = `{
	  "state": "a ticket body",
	  "model": "jev-1.13.0",
	  "questions": {
	    "urgent": {"type": "noul", "instructions": "is this urgent",
	               "criteria": {"true": "shouting", "false": "calm"}},
	    "team": {"type": "choice", "instructions": "which team",
	             "criteria": {"billing": "money", "technical": null}},
	    "mood": {"type": "score", "instructions": "how cross",
	             "criteria": ["calm", "annoyed", "furious"]}
	  }
	}`

	tests := []struct {
		name    string
		doc     string
		wantErr string
		check   func(t *testing.T, f *qfile.File)
	}{
		{
			name: "should report itself as a body",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if !f.IsBody {
					t.Error("a file with a top level questions key is a body")
				}

				if f.Model != "jev-1.13.0" {
					t.Errorf("model = %q", f.Model)
				}

				if !f.HasState || f.State != "a ticket body" {
					t.Errorf("state = %v, hasState = %v", f.State, f.HasState)
				}
			},
		},
		{
			name: "should keep body questions in file order",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				want := []string{"urgent", "team", "mood"}
				for i, q := range f.Questions {
					if q.ID != want[i] {
						t.Errorf("question %d = %s, want %s", i, q.ID, want[i])
					}
				}
			},
		},
		{
			name: "should map the three wire types onto shapes",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				shapes := []plan.Shape{plan.Noul, plan.Pick, plan.Rate}
				for i, want := range shapes {
					if f.Questions[i].Shape != want {
						t.Errorf("question %d shape = %s, want %s",
							i, f.Questions[i].Shape, want)
					}
				}
			},
		},
		{
			name: "should leave a body score question unlabelled",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				mood := f.Questions[2]
				if mood.Labelled {
					t.Error("a body score question has no labels and must not be Labelled")
				}

				if len(mood.Levels) != 3 {
					t.Fatalf("levels = %d, want 3", len(mood.Levels))
				}

				for _, level := range mood.Levels {
					if level.Label != "" {
						t.Errorf("a body level must carry no label, got %q", level.Label)
					}
				}

				if mood.Levels[0].Desc != "calm" {
					t.Errorf("level 0 desc = %v, want calm", mood.Levels[0].Desc)
				}
			},
		},
		{
			name: "should read choice criteria as ordered options",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				team := f.Questions[1]
				if team.Options[0].Name != "billing" || team.Options[1].Name != "technical" {
					t.Errorf("options = %+v, want billing then technical", team.Options)
				}

				if team.Options[1].Desc != nil {
					t.Errorf("a null criteria must stay nil, got %v", team.Options[1].Desc)
				}
			},
		},
		{
			name: "should read noul criteria into the yes and no rubric",
			doc:  full,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				urgent := f.Questions[0]
				if urgent.Criteria == nil || urgent.Criteria.Yes != "shouting" {
					t.Errorf("criteria = %+v", urgent.Criteria)
				}
			},
		},
		{
			name: "should carry no state when the body omits one",
			doc:  `{"questions":{"a":{"type":"noul","instructions":"q"}}}`,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if f.HasState {
					t.Error("a body with no state key must not report one")
				}
			},
		},
		{
			name: "should carry an explicit null state as present",
			doc:  `{"state":null,"questions":{"a":{"type":"noul","instructions":"q"}}}`,
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if !f.HasState {
					t.Error("an explicit null state is present, not absent")
				}
			},
		},
		{
			name: "should load a body written as yaml",
			doc: "state: a ticket\nquestions:\n  a:\n    type: noul\n" +
				"    instructions: is this urgent\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if !f.IsBody || f.Questions[0].ID != "a" {
					t.Errorf("a body is a body whether written as json or yaml, got %+v", f)
				}
			},
		},
		{
			name:    "should reject an unknown question type",
			doc:     `{"questions":{"a":{"type":"vibes","instructions":"q"}}}`,
			wantErr: "vibes",
		},
		{
			name:    "should reject a question with no type",
			doc:     `{"questions":{"a":{"instructions":"q"}}}`,
			wantErr: "type",
		},
		{
			name:    "should reject a questions key that is not a mapping",
			doc:     `{"questions":[1,2]}`,
			wantErr: "questions",
		},
		{
			name:    "should reject a structured model",
			doc:     `{"model":{"name":"jev"},"questions":{"a":{"type":"noul"}}}`,
			wantErr: "'model' in a request body must be a string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q, want it to mention %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.check(t, got)
		})
	}
}

func TestLoadBodyStructured(t *testing.T) {
	t.Parallel()

	t.Run("should carry a structured body instruction through as an object", func(t *testing.T) {
		t.Parallel()

		const doc = `{"questions":{"a":{"type":"noul","instructions":` +
			`{"what":"is this urgent","examples":["call me now"]}}}}`

		f, err := qfile.Load([]byte(doc))
		if err != nil {
			t.Fatalf("loading: %v", err)
		}

		encoded, err := json.Marshal(f.Questions[0].Instructions)
		if err != nil {
			t.Fatalf("marshalling: %v", err)
		}

		want := `{"examples":["call me now"],"what":"is this urgent"}`
		if string(encoded) != want {
			t.Errorf("got  %s\nwant %s", encoded, want)
		}
	})
}
