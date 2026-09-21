package qfile_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/qfile"
)

func TestDecodeOrdered(t *testing.T) {
	t.Parallel()

	const separated = "jev: a question file is one document, " +
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

func TestLoadSeparatorInText(t *testing.T) {
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
