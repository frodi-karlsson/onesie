package qfile_test

import (
	"encoding/json"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
	"github.com/frodi-karlsson/jev-cli/internal/qfile"
)

func TestLoadYesNo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		wantErr bool
		check   func(t *testing.T, f *qfile.File)
	}{
		{
			name: "should read a bare string as a yes/no question",
			doc:  "urgent: Does this convey urgency?\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if len(f.Questions) != 1 {
					t.Fatalf("questions = %d, want 1", len(f.Questions))
				}

				q := f.Questions[0]
				if q.ID != "urgent" || q.Shape != plan.Noul {
					t.Errorf("got %s/%s, want urgent/noul", q.ID, q.Shape)
				}

				if q.Instructions != "Does this convey urgency?" {
					t.Errorf("instructions = %v", q.Instructions)
				}

				if !q.Named {
					t.Error("a file question must be Named, so reserved ids are rejected")
				}
			},
		},
		{
			name: "should keep questions in file order",
			doc:  "zebra: one\napple: two\nmiddle: three\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				want := []string{"zebra", "apple", "middle"}
				for i, q := range f.Questions {
					if q.ID != want[i] {
						t.Errorf("question %d = %s, want %s", i, q.ID, want[i])
					}
				}
			},
		},
		{
			name: "should read the long form with rubrics",
			doc: "spam:\n  ask: Is this automated spam?\n" +
				"  yes_means: Bulk, templated or bot sent\n" +
				"  no_means: Written by a person\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				q := f.Questions[0]
				if q.Criteria == nil {
					t.Fatal("criteria should be set")
				}

				if q.Criteria.Yes != "Bulk, templated or bot sent" {
					t.Errorf("yes = %v", q.Criteria.Yes)
				}

				if q.Criteria.No != "Written by a person" {
					t.Errorf("no = %v", q.Criteria.No)
				}
			},
		},
		{
			name: "should accept true and false as aliases",
			doc:  "spam:\n  ask: Is this spam?\n  true: bulk\n  false: personal\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				q := f.Questions[0]
				if q.Criteria == nil || q.Criteria.Yes != "bulk" || q.Criteria.No != "personal" {
					t.Errorf("criteria = %+v, want the true and false keys mapped", q.Criteria)
				}
			},
		},
		{
			name: "should prefer the _means spelling when both are present",
			doc:  "spam:\n  ask: q\n  yes_means: documented\n  true: alias\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				if f.Questions[0].Criteria.Yes != "documented" {
					t.Errorf("yes = %v, want the _means spelling to win",
						f.Questions[0].Criteria.Yes)
				}
			},
		},
		{
			name: "should carry a structured instruction through as an object",
			doc: "spam:\n  ask:\n    what: Is this spam?\n" +
				"    examples: [\"buy now\"]\n",
			check: func(t *testing.T, f *qfile.File) {
				t.Helper()

				encoded, err := json.Marshal(f.Questions[0].Instructions)
				if err != nil {
					t.Fatalf("marshalling: %v", err)
				}

				want := `{"examples":["buy now"],"what":"Is this spam?"}`
				if string(encoded) != want {
					t.Errorf("got  %s\nwant %s", encoded, want)
				}
			},
		},
		{
			name:    "should reject a question that is neither a string nor a mapping",
			doc:     "urgent: [1, 2]\n",
			wantErr: true,
		},
		{
			name:    "should reject a mapping with no ask",
			doc:     "urgent:\n  yes_means: something\n",
			wantErr: true,
		},
		{
			name:    "should reject a file that is not a mapping",
			doc:     "- just\n- a list\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
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

func TestLoadPick(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		wantErr bool
		check   func(t *testing.T, q plan.Question)
	}{
		{
			name: "should read a mapping of option to description in file order",
			doc: "team:\n  ask: Which team?\n  pick:\n" +
				"    zebra: Z\n    apple: A\n    middle: M\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Shape != plan.Pick {
					t.Fatalf("shape = %s, want pick", q.Shape)
				}

				want := []string{"zebra", "apple", "middle"}
				for i, option := range q.Options {
					if option.Name != want[i] {
						t.Errorf("option %d = %s, want %s", i, option.Name, want[i])
					}
				}

				if q.Options[0].Desc != "Z" {
					t.Errorf("desc = %v, want Z", q.Options[0].Desc)
				}
			},
		},
		{
			name: "should read an empty description as a null criteria",
			doc:  "team:\n  ask: Which team?\n  pick:\n    billing: Payments\n    sales:\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if q.Options[1].Desc != nil {
					t.Errorf("desc = %v, want nil for an empty value", q.Options[1].Desc)
				}
			},
		},
		{
			name: "should read a sequence of strings as undescribed options",
			doc:  "team:\n  ask: Which team?\n  pick: [billing, technical, sales]\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				if len(q.Options) != 3 {
					t.Fatalf("options = %d, want 3", len(q.Options))
				}

				for _, option := range q.Options {
					if option.Desc != nil {
						t.Errorf("option %s should have no description", option.Name)
					}
				}
			},
		},
		{
			name: "should carry a structured description through as an object",
			doc: "team:\n  ask: Which team?\n  pick:\n    billing:\n" +
				"      what: money\n      examples: [\"refund\"]\n    sales: deals\n",
			check: func(t *testing.T, q plan.Question) {
				t.Helper()

				encoded, err := json.Marshal(q.Options[0].Desc)
				if err != nil {
					t.Fatalf("marshalling: %v", err)
				}

				want := `{"examples":["refund"],"what":"money"}`
				if string(encoded) != want {
					t.Errorf("got  %s\nwant %s", encoded, want)
				}
			},
		},
		{
			name:    "should reject a pick that is neither a mapping nor a sequence",
			doc:     "team:\n  ask: Which team?\n  pick: billing\n",
			wantErr: true,
		},
		{
			name:    "should reject a non string entry in a pick sequence",
			doc:     "team:\n  ask: Which team?\n  pick: [billing, 7]\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Load([]byte(tc.doc))

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %#v", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			tc.check(t, got.Questions[0])
		})
	}
}
