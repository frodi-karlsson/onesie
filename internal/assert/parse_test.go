package assert

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	t.Parallel()

	unclosed := `team.p["needs review" > 0.2`
	tooDeep := strings.Repeat("(", maxDepth) + `a.value < 1` + strings.Repeat(")", maxDepth)
	deepEnough := strings.Repeat("(", maxDepth-1) + `a.value < 1` + strings.Repeat(")", maxDepth-1)
	callTooDeep := strings.Repeat("max(", maxDepth) + `a.value` + strings.Repeat(")", maxDepth) + ` < 1`
	callDeepEnough := strings.Repeat("max(", maxDepth-1) + `a.value` + strings.Repeat(")", maxDepth-1) + ` < 1`

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
			name:  "should parse a bracket path as a list member",
			input: `team.value in [["needs review"].value, "billing"]`,
			want:  `(in team.value [["needs review"].value "billing"])`,
		},
		{
			name:  "should parse an expression nesting just inside the bound",
			input: deepEnough,
			want:  `(< a.value 1)`,
		},
		{
			name:  "should parse a bare model path",
			input: `model == "onesie-1.13.0"`,
			want:  `(== model "onesie-1.13.0")`,
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
			name:  "should parse min with one argument",
			input: `min(a.value) < 0.5`,
			want:  `(< (min a.value) 0.5)`,
		},
		{
			name:  "should parse min with two arguments",
			input: `min(a.value, b.value) < 0.5`,
			want:  `(< (min a.value b.value) 0.5)`,
		},
		{
			name:  "should parse min with three arguments",
			input: `min(a.value, b.confidence, 0.2) < 0.5`,
			want:  `(< (min a.value b.confidence 0.2) 0.5)`,
		},
		{
			name:  "should parse max with one argument",
			input: `max(a.value) < 0.5`,
			want:  `(< (max a.value) 0.5)`,
		},
		{
			name:  "should parse max with two arguments",
			input: `max(a.value, b.value) < 0.5`,
			want:  `(< (max a.value b.value) 0.5)`,
		},
		{
			name:  "should parse max with three arguments",
			input: `max(a.value, b.confidence, 0.2) < 0.5`,
			want:  `(< (max a.value b.confidence 0.2) 0.5)`,
		},
		{
			name:  "should parse sum with one argument",
			input: `sum(a.value) < 0.5`,
			want:  `(< (sum a.value) 0.5)`,
		},
		{
			name:  "should parse sum with two arguments",
			input: `sum(a.value, b.value) < 0.5`,
			want:  `(< (sum a.value b.value) 0.5)`,
		},
		{
			name:  "should parse sum with three arguments",
			input: `sum(a.value, b.confidence, 0.2) < 0.5`,
			want:  `(< (sum a.value b.confidence 0.2) 0.5)`,
		},
		{
			name:  "should parse avg with one argument",
			input: `avg(a.value) < 0.5`,
			want:  `(< (avg a.value) 0.5)`,
		},
		{
			name:  "should parse avg with two arguments",
			input: `avg(a.value, b.value) < 0.5`,
			want:  `(< (avg a.value b.value) 0.5)`,
		},
		{
			name:  "should parse avg with three arguments",
			input: `avg(a.value, b.confidence, 0.2) < 0.5`,
			want:  `(< (avg a.value b.confidence 0.2) 0.5)`,
		},
		{
			name:  "should parse a nested call",
			input: `max(min(a.value, b.value), c.value) < 0.5`,
			want:  `(< (max (min a.value b.value) c.value) 0.5)`,
		},
		{
			name:  "should parse a call on the right of a comparison",
			input: `a.value >= avg(b.norm, c.p["needs review"])`,
			want:  `(>= a.value (avg b.norm c.p["needs review"]))`,
		},
		{
			name:  "should parse a call in an in list",
			input: `a.value in [max(b.value, c.value), 1]`,
			want:  `(in a.value [(max b.value c.value) 1])`,
		},
		{
			name:  "should read a question named like a function as a path",
			input: `max.value < 0.5`,
			want:  `(< max.value 0.5)`,
		},
		{
			name:    "should read a function name followed by a space as a path",
			input:   `max (a.value) < 0.5`,
			wantErr: "parse error at column 5, expected a comparison",
		},
		{
			name:  "should accept any name followed by a parenthesis and leave it to the checker",
			input: `mx(a.value) < 0.5`,
			want:  `(< (mx a.value) 0.5)`,
		},
		{
			name:  "should parse a call nesting just inside the bound",
			input: callDeepEnough,
			want:  `(< ` + strings.Repeat("(max ", maxDepth-1) + `a.value` + strings.Repeat(")", maxDepth-1) + ` 1)`,
		},
		{
			name:    "should reject an empty argument list",
			input:   `max() < 0.5`,
			wantErr: "parse error at column 5, 'max' needs at least one number",
		},
		{
			name:    "should reject a call missing its closing parenthesis",
			input:   `max(a.value, b.value < 0.5`,
			wantErr: "parse error at column 22, expected a closing parenthesis",
		},
		{
			name:    "should reject a trailing comma in a call",
			input:   `max(a.value,) < 0.5`,
			wantErr: "parse error at column 13, expected a value",
		},
		{
			name:    "should reject a call that nests too deeply",
			input:   callTooDeep,
			wantErr: fmt.Sprintf("parse error at column %d, the expression nests too deeply", 4*maxDepth+1),
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
		{
			name:    "should reject a list inside a list",
			input:   `team.value in [["billing"]]`,
			wantErr: "parse error at column 16, a list holds values, not another list",
		},
		{
			name:    "should reject an expression that nests too deeply",
			input:   tooDeep,
			wantErr: fmt.Sprintf("parse error at column %d, the expression nests too deeply", maxDepth+1),
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

	t.Run("should place every node at its source column", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			input string
			want  []int
		}{
			{
				name:  "should point a comparison at its operator and its operands at themselves",
				input: `urgent.value < 0.5`,
				want:  []int{14, 1, 16},
			},
			{
				name:  "should point and at its keyword and not at its own",
				input: `not a.value < 1 and b.value < 1`,
				want:  []int{17, 1, 13, 5, 15, 29, 21, 31},
			},
			{
				name:  "should point or at its keyword",
				input: `a.value < 1 or b.value < 1`,
				want:  []int{13, 9, 1, 11, 24, 16, 26},
			},
			{
				name:  "should point in at its keyword and every list member at itself",
				input: `team.value in ["billing", true]`,
				want:  []int{12, 1, 16, 27},
			},
			{
				name:  "should point a call at its name and every argument at itself",
				input: `max(a.value, 0.2) < 1`,
				want:  []int{19, 1, 5, 14, 21},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				got, err := Parse(tc.input)
				if err != nil {
					t.Fatalf("Parse(%q) error = %v, want no error", tc.input, err)
				}

				if columns := positions(got.root); !slices.Equal(columns, tc.want) {
					t.Errorf("Parse(%q) columns = %v, want %v", tc.input, columns, tc.want)
				}
			})
		}
	})
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
		{
			name:       "should bracket a folded expression so and cannot reach inside its or",
			input:      []string{`a.value < 1 or b.value < 1`, `c.value < 1`},
			want:       `(and (or (< a.value 1) (< b.value 1)) (< c.value 1))`,
			wantSource: `(a.value < 1 or b.value < 1) and (c.value < 1)`,
		},
		{
			name:  "should return nil when every expression is nil",
			input: []string{``},
		},
		{
			name:       "should skip a nil expression",
			input:      []string{``, `a.value < 1`},
			want:       `(< a.value 1)`,
			wantSource: `a.value < 1`,
		},
		{
			name:       "should skip a nil expression before folding the rest",
			input:      []string{``, `a.value < 1`, ``, `b.value < 2`},
			want:       `(and (< a.value 1) (< b.value 2))`,
			wantSource: `(a.value < 1) and (b.value < 2)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			exprs := make([]*Expr, 0, len(tc.input))
			present := 0

			for _, source := range tc.input {
				if source == "" {
					exprs = append(exprs, nil)

					continue
				}

				expr, err := Parse(source)
				if err != nil {
					t.Fatalf("Parse(%q) error = %v, want no error", source, err)
				}

				exprs = append(exprs, expr)
				present++
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

			reparsed, err := Parse(got.source)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v, want the combined source to parse", got.source, err)
			}
			if rendered := reparsed.root.render(); rendered != tc.want {
				t.Errorf("Parse(%q) = %q, want %q, so the source says what the tree says", got.source, rendered, tc.want)
			}
			if present == 1 && got != exprs[len(exprs)-1] {
				t.Errorf("Combine(%q) rebuilt the one expression, want it returned as it was", tc.input)
			}
		})
	}
}

func positions(n node) []int {
	out := []int{n.pos()}

	switch typed := n.(type) {
	case *binaryNode:
		out = append(out, positions(typed.left)...)
		out = append(out, positions(typed.right)...)
	case *notNode:
		out = append(out, positions(typed.operand)...)
	case *comparisonNode:
		out = append(out, positions(typed.left)...)
		out = append(out, positions(typed.right)...)
	case *inNode:
		out = append(out, positions(typed.operand)...)

		for _, member := range typed.list {
			out = append(out, positions(member)...)
		}
	case *callNode:
		for _, arg := range typed.args {
			out = append(out, positions(arg)...)
		}
	}

	return out
}

func TestExprSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{
			name: "should report nothing for a nil expression",
		},
		{
			name:  "should report one expression as it was written",
			input: []string{`a.value < 1`},
			want:  `a.value < 1`,
		},
		{
			name:  "should report a combined expression as its parenthesised parts",
			input: []string{`a.value < 1`, `b.value < 2`},
			want:  `(a.value < 1) and (b.value < 2)`,
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

			if got := Combine(exprs...).Source(); got != tc.want {
				t.Errorf("Source() = %q, want %q", got, tc.want)
			}
		})
	}
}
