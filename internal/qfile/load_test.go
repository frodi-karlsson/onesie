package qfile_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	const schemaURL = "https://raw.githubusercontent.com/frodi-karlsson/onesie/main/schema/questions.json"

	tests := []struct {
		name    string
		doc     string
		same    string
		wantErr string
	}{
		{
			name: "should ignore a $schema string in a yaml file",
			doc:  "$schema: " + schemaURL + "\nurgent: is this urgent\nassert: 'urgent.value < 0.5'\n",
			same: "urgent: is this urgent\nassert: 'urgent.value < 0.5'\n",
		},
		{
			name: "should ignore a $schema string in a json file",
			doc:  `{"$schema": "` + schemaURL + `", "team": {"ask": "which team", "pick": ["billing", "sales"]}}`,
			same: `{"team": {"ask": "which team", "pick": ["billing", "sales"]}}`,
		},
		{
			name: "should ignore a $schema string written below the questions",
			doc:  "urgent: is this urgent\n$schema: ./questions.json\n",
			same: "urgent: is this urgent\n",
		},
		{
			name:    "should reject a $schema that is a number",
			doc:     "$schema: 3\nurgent: is this urgent\n",
			wantErr: "'$schema' must be a string, got '3'",
		},
		{
			name:    "should reject a $schema that is a mapping",
			doc:     `{"$schema": {"url": "x"}, "urgent": "is this urgent"}`,
			wantErr: "'$schema' must be a string, got a mapping",
		},
		{
			name: "should leave a request body as it is beside a $schema",
			doc: `{"$schema": 3, "questions": {"urgent": {"type": "noul", ` +
				`"instructions": "is this urgent"}}}`,
			same: `{"questions": {"urgent": {"type": "noul", "instructions": "is this urgent"}}}`,
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

				if err.Error() != tc.wantErr {
					t.Errorf("error = %q, want %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			want, err := qfile.Load([]byte(tc.same))
			if err != nil {
				t.Fatalf("loading the file without $schema: %v", err)
			}

			if !reflect.DeepEqual(got, want) {
				t.Errorf("loaded %#v\nwant %#v", got, want)
			}
		})
	}

	t.Run("should load a null document as a file with no questions", func(t *testing.T) {
		t.Parallel()

		for _, doc := range []string{"", " \n", "# a note\n", "null\n"} {
			file, err := qfile.Load([]byte(doc))
			if err != nil || len(file.Questions) != 0 {
				t.Errorf("Load(%q) = %+v, %v, want an empty file", doc, file, err)
			}
		}
	})

	t.Run("should load and refuse box questions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			file    string
			wantErr string
		}{
			{name: "should load a box question", file: "mug:\n  ask: the mug in photo\n  box: true\n"},
			{name: "should load a box question from a request body", file: `{"questions":{"mug":{"type":"box","instructions":"x"}}}`},
			{name: "should refuse box false", file: "mug:\n  ask: x\n  box: false\n", wantErr: "'box' in question 'mug' takes true"},
			{name: "should refuse a box with pick", file: "mug:\n  ask: x\n  box: true\n  pick: [a, b]\n", wantErr: "A question is one of pick, rate or box"},
			{name: "should refuse a box with yes_means", file: "mug:\n  ask: x\n  box: true\n  yes_means: y\n", wantErr: "has both 'yes_means' and 'box'"},
			{name: "should refuse a body box with criteria", file: `{"questions":{"mug":{"type":"box","instructions":"x","criteria":{}}}}`, wantErr: "box question 'mug' in a request body takes no criteria"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				file, err := qfile.Load([]byte(tc.file))
				if tc.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
						t.Fatalf("error = %v, want %q", err, tc.wantErr)
					}

					return
				}

				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if got := file.Questions[0]; got.Shape != plan.Box || !got.Boxed {
					t.Errorf("question = %+v, want a box", got)
				}
			})
		}
	})
}

func TestDecodeOrdered(t *testing.T) {
	t.Parallel()

	const separated = "a question file is one document, " +
		"found a second after a --- separator"

	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{
			name: "should accept a single document",
			doc:  "a: q1\n",
		},
		{
			name: "should accept a leading document separator",
			doc:  "---\na: q1\n",
		},
		{
			name: "should accept a trailing document separator",
			doc:  "a: q1\n---\n",
		},
		{
			name: "should accept a separator inside a block scalar",
			doc:  "a:\n  ask: |\n    line one\n    ---\n    line two\n",
		},
		{
			name: "should accept a separator inside a quoted string",
			doc:  "a:\n  ask: \"line one\\n---\\nline two\"\n",
		},
		{
			name: "should accept a value that is only dashes",
			doc:  "a: \"---\"\n",
		},
		{
			name: "should accept JSON carrying dashes in a value",
			doc:  `{"a": "--- not a separator ---"}`,
		},
		{
			name:    "should reject a second document",
			doc:     "a: q1\n---\nb: q2\n",
			wantErr: separated,
		},
		{
			name:    "should reject a second document after a leading separator",
			doc:     "---\na: q1\n---\nb: q2\n",
			wantErr: separated,
		},
		{
			name:    "should reject a third document",
			doc:     "a: q1\n---\nb: q2\n---\nc: q3\n",
			wantErr: separated,
		},
		{name: "should reject malformed yaml", doc: "a:\n  - b\n c: broken\n", wantErr: "value is not allowed in this context"},
		{name: "should reject a key json repeats", doc: `{"a": "x", "a": "y"}`, wantErr: `mapping key "a" already defined`},
		{name: "should reject a key yaml repeats", doc: "a: x\na: y\n", wantErr: `mapping key "a" already defined`},
		{
			name: "should reject an alias before it expands",
			doc: "a: &a [x, x, x, x, x, x, x, x, x, x]\n" +
				"b: &b [*a, *a, *a, *a, *a, *a, *a, *a, *a, *a]\n" +
				"c: &c [*b, *b, *b, *b, *b, *b, *b, *b, *b, *b]\n" +
				"d: &d [*c, *c, *c, *c, *c, *c, *c, *c, *c, *c]\n" +
				"e: &e [*d, *d, *d, *d, *d, *d, *d, *d, *d, *d]\n" +
				"f: &f [*e, *e, *e, *e, *e, *e, *e, *e, *e, *e]\n" +
				"g: [*f, *f, *f, *f, *f, *f, *f, *f, *f, *f]\n",
			wantErr: "a question file cannot use a YAML alias, since onesie does not expand one. " +
				"Write the value out in full",
		},
		{
			name:    "should reject a merge key, which is an alias too",
			doc:     "base: &base {ask: is this urgent}\nurgent:\n  <<: *base\n",
			wantErr: "cannot use a YAML alias",
		},
		{name: "should accept an anchor nothing refers to", doc: "a: &a q1\n"},
		{
			name:    "should reject a file over the size cap",
			doc:     "a: \"" + strings.Repeat("x", limits.MaxQuestionFileBytes) + "\"\n",
			wantErr: "a question file is at most 33554432 bytes",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.DecodeOrdered([]byte(tc.doc))

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got %+v", tc.wantErr, got)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q\nwant it to contain %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCheckSingleDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		doc  string
		want string
	}{
		{
			name: "should keep a separator inside a block scalar in the instructions",
			doc:  "a:\n  ask: |-\n    line one\n    ---\n    line two\n",
			want: "line one\n---\nline two",
		},
		{
			name: "should keep a separator inside a quoted string in the instructions",
			doc:  "a:\n  ask: \"line one\\n---\\nline two\"\n",
			want: "line one\n---\nline two",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			file, err := qfile.Load([]byte(tc.doc))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(file.Questions) != 1 {
				t.Fatalf("questions = %d, want 1", len(file.Questions))
			}

			if file.Questions[0].Instructions != tc.want {
				t.Errorf("instructions = %q, want %q", file.Questions[0].Instructions, tc.want)
			}
		})
	}
}
