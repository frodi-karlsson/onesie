package assert

import (
	"fmt"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	unclosed := `team.p["needs review" > 0.2`

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{
			name:  "should parse a comparison",
			input: `urgent.value < 0.5`,
			want:  `(< urgent.value 0.5)`,
		},
		{
			name:  "should bind and tighter than or",
			input: `a.value < 1 or b.value < 1 and c.value < 1`,
			want:  `(or (< a.value 1) (and (< b.value 1) (< c.value 1)))`,
		},
		{
			name:  "should bind not tighter than and",
			input: `not a.value < 1 and b.value < 1`,
			want:  `(and (not (< a.value 1)) (< b.value 1))`,
		},
		{
			name:  "should parse a repeated not",
			input: `not not a.value < 1`,
			want:  `(not (not (< a.value 1)))`,
		},
		{
			name:  "should respect parentheses",
			input: `(a.value < 1 or b.value < 1) and c.value < 1`,
			want:  `(and (or (< a.value 1) (< b.value 1)) (< c.value 1))`,
		},
		{
			name:  "should parse a bracket segment",
			input: `team.p["needs review"] > 0.2`,
			want:  `(> team.p["needs review"] 0.2)`,
		},
		{
			name:  "should parse a bracketed path head",
			input: `["needs-review"].value < 0.5`,
			want:  `(< ["needs-review"].value 0.5)`,
		},
		{
			name:  "should read a dotted id as one head rather than two segments",
			input: `["a.b"].value < 0.5`,
			want:  `(< ["a.b"].value 0.5)`,
		},
		{
			name:  "should parse in over a list",
			input: `team.value in ["billing", "technical"]`,
			want:  `(in team.value ["billing" "technical"])`,
		},
		{
			name:  "should parse a bare model path",
			input: `model == "jev-1.13.0"`,
			want:  `(== model "jev-1.13.0")`,
		},
		{
			name:  "should parse a boolean literal operand",
			input: `urgent.decision == true`,
			want:  `(== urgent.decision true)`,
		},
		{
			name:  "should parse every comparison operator",
			input: `a.value != 1 and b.value <= 1 and c.value >= 1`,
			want:  `(and (and (!= a.value 1) (<= b.value 1)) (>= c.value 1))`,
		},
		{
			name:    "should report an unclosed bracket with its column",
			input:   unclosed,
			wantErr: fmt.Sprintf("parse error at column %d, expected a closing bracket before the end", len([]rune(unclosed))+1),
		},
		{
			name:    "should report an unclosed list bracket",
			input:   `team.value in ["billing"`,
			wantErr: "parse error at column 25, expected a closing bracket before the end",
		},
		{
			name:    "should report an unclosed parenthesis",
			input:   `(a.value < 1`,
			wantErr: "parse error at column 13, expected a closing parenthesis",
		},
		{
			name:    "should reject an empty expression",
			input:   ``,
			wantErr: "the expression is empty",
		},
		{
			name:    "should reject a trailing operator",
			input:   `a.value <`,
			wantErr: "parse error at column 10, expected a value",
		},
		{
			name:    "should reject an empty list",
			input:   `team.value in []`,
			wantErr: "parse error at column 16, a list needs at least one value",
		},
		{
			name:    "should reject a bare path as a whole expression",
			input:   `urgent.value`,
			wantErr: "parse error at column 13, expected a comparison",
		},
		{
			name:    "should reject an unquoted name in the brackets",
			input:   `[urgent].value < 1`,
			wantErr: "parse error at column 2, expected a quoted name in the brackets",
		},
		{
			name:    "should reject a keyword as a field name",
			input:   `urgent.in < 1`,
			wantErr: "parse error at column 8, expected a field name after the dot",
		},
		{
			name:    "should reject trailing input after a complete expression",
			input:   `a.value < 1 b.value < 1`,
			wantErr: "parse error at column 13, expected the end of the expression",
		},
		{
			name:    "should reject in over something that is not a list",
			input:   `team.value in "billing"`,
			wantErr: "parse error at column 15, expected a list after in",
		},
		{
			name:    "should report a lexer error as the lexer wrote it",
			input:   `team.value == 'billing'`,
			wantErr: "strings use double quotes, got 'billing'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Parse(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Parse(%q) = %q, want error %q", tc.input, got.root.render(), tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Errorf("Parse(%q) error = %q, want %q", tc.input, err.Error(), tc.wantErr)
				}
				if got != nil {
					t.Errorf("Parse(%q) = %q, want no tree beside the error", tc.input, got.root.render())
				}

				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error = %v, want no error", tc.input, err)
			}

			if rendered := got.root.render(); rendered != tc.want {
				t.Errorf("Parse(%q) = %q, want %q", tc.input, rendered, tc.want)
			}
			if got.source != tc.input {
				t.Errorf("Parse(%q) source = %q, want %q", tc.input, got.source, tc.input)
			}
		})
	}
}

func TestCombine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      []string
		want       string
		wantSource string
	}{
		{
			name:  "should return nil when combining none",
			input: nil,
		},
		{
			name:       "should return the one expression unchanged",
			input:      []string{`a.value < 1`},
			want:       `(< a.value 1)`,
			wantSource: `a.value < 1`,
		},
		{
			name:       "should fold several with and in the order given",
			input:      []string{`a.value < 1`, `b.value < 2`, `c.value < 3`},
			want:       `(and (and (< a.value 1) (< b.value 2)) (< c.value 3))`,
			wantSource: `(a.value < 1) and (b.value < 2) and (c.value < 3)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exprs := make([]*Expr, 0, len(tc.input))
			for _, source := range tc.input {
				expr, err := Parse(source)
				if err != nil {
					t.Fatalf("Parse(%q) error = %v, want no error", source, err)
				}

				exprs = append(exprs, expr)
			}

			got := Combine(exprs...)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("Combine(%q) = %q, want nil", tc.input, got.root.render())
				}

				return
			}
			if got == nil {
				t.Fatalf("Combine(%q) = nil, want %q", tc.input, tc.want)
			}

			if rendered := got.root.render(); rendered != tc.want {
				t.Errorf("Combine(%q) = %q, want %q", tc.input, rendered, tc.want)
			}
			if got.source != tc.wantSource {
				t.Errorf("Combine(%q) source = %q, want %q", tc.input, got.source, tc.wantSource)
			}
			if len(exprs) == 1 && got != exprs[0] {
				t.Errorf("Combine(%q) rebuilt the one expression, want it returned as it was", tc.input)
			}
		})
	}
}
