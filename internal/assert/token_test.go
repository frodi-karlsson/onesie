package assert

import (
	"slices"
	"testing"
)

func TestLex(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		want     []string
		wantCols []int
		wantErr  string
	}{
		{
			name:     "should lex a simple comparison",
			input:    `urgent.value < 0.5`,
			want:     []string{"ident:urgent", "dot", "ident:value", "lt", "number:0.5", "eof"},
			wantCols: []int{1, 7, 8, 14, 16, 19},
		},
		{
			name:  "should lex a bracket path",
			input: `team.p["needs review"]`,
			want: []string{
				"ident:team", "dot", "ident:p", "lbracket",
				"string:needs review", "rbracket", "eof",
			},
			wantCols: []int{1, 5, 6, 7, 8, 22, 23},
		},
		{
			name:  "should lex the keywords",
			input: `a and b or not c in d true false`,
			want: []string{
				"ident:a", "and", "ident:b", "or", "not", "ident:c",
				"in", "ident:d", "true", "false", "eof",
			},
			wantCols: []int{1, 3, 7, 9, 12, 16, 18, 21, 23, 28, 33},
		},
		{
			name:     "should lex every comparison operator",
			input:    `== != < <= > >=`,
			want:     []string{"eq", "ne", "lt", "le", "gt", "ge", "eof"},
			wantCols: []int{1, 4, 7, 9, 12, 14, 16},
		},
		{
			name:  "should lex a list, the parentheses and the comma",
			input: `(a or b) and x in [1, 2]`,
			want: []string{
				"lparen", "ident:a", "or", "ident:b", "rparen", "and", "ident:x", "in",
				"lbracket", "number:1", "comma", "number:2", "rbracket", "eof",
			},
			wantCols: []int{1, 2, 4, 7, 8, 10, 14, 16, 19, 20, 21, 23, 24, 25},
		},
		{
			name:     "should lex a negative number",
			input:    `-1.5`,
			want:     []string{"number:-1.5", "eof"},
			wantCols: []int{1, 5},
		},
		{
			name:     "should lex an escaped quote inside a string",
			input:    `"a\"b"`,
			want:     []string{`string:a"b`, "eof"},
			wantCols: []int{1, 7},
		},
		{
			name:     "should not split an identifier that starts with a keyword",
			input:    `android.value < 0.5`,
			want:     []string{"ident:android", "dot", "ident:value", "lt", "number:0.5", "eof"},
			wantCols: []int{1, 8, 9, 15, 17, 20},
		},
		{
			name:  "should count a column in runes, not bytes",
			input: `x.p["é"] == 1`,
			want: []string{
				"ident:x", "dot", "ident:p", "lbracket", "string:é", "rbracket",
				"eq", "number:1", "eof",
			},
			wantCols: []int{1, 2, 3, 4, 5, 8, 10, 13, 14},
		},
		{
			name:     "should lex an empty expression",
			input:    ``,
			want:     []string{"eof"},
			wantCols: []int{1},
		},
		{
			name:    "should reject a single quoted string",
			input:   `team.value == 'billing'`,
			wantErr: "strings use double quotes, got 'billing'",
		},
		{
			name:    "should reject an unterminated single quoted string",
			input:   `team.value == 'billing`,
			wantErr: "strings use double quotes, got 'billing'",
		},
		{
			name:    "should reject an unterminated string",
			input:   `team.value == "billing`,
			wantErr: "unterminated string",
		},
		{
			name:    "should reject an unknown escape in a string",
			input:   `team.value == "bil\qing"`,
			wantErr: `invalid string "bil\qing"`,
		},
		{
			name:    "should reject an unknown character",
			input:   `urgent.value % 2`,
			wantErr: "unexpected character '%'",
		},
		{
			name:    "should reject a single equals",
			input:   `urgent.value = 0.5`,
			wantErr: "equality is '==', got '='",
		},
		{
			name:    "should reject a number carrying two dots",
			input:   `urgent.value < 1.2.3`,
			wantErr: "invalid number '1.2.3'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := lex(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("lex(%q) = %v, want error %q", tc.input, renderTokens(got), tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Errorf("lex(%q) error = %q, want %q", tc.input, err.Error(), tc.wantErr)
				}
				if got != nil {
					t.Errorf("lex(%q) = %v, want no tokens beside the error", tc.input, renderTokens(got))
				}

				return
			}
			if err != nil {
				t.Fatalf("lex(%q) error = %v, want no error", tc.input, err)
			}

			if rendered := renderTokens(got); !slices.Equal(rendered, tc.want) {
				t.Errorf("lex(%q) = %v, want %v", tc.input, rendered, tc.want)
			}
			if cols := columns(got); !slices.Equal(cols, tc.wantCols) {
				t.Errorf("lex(%q) columns = %v, want %v", tc.input, cols, tc.wantCols)
			}
		})
	}
}

func renderTokens(tokens []token) []string {
	var out []string
	for _, tok := range tokens {
		switch tok.kind {
		case kindIdent, kindNumber, kindString:
			out = append(out, tok.kind.String()+":"+tok.text)
		default:
			out = append(out, tok.kind.String())
		}
	}

	return out
}

func columns(tokens []token) []int {
	var out []int
	for _, tok := range tokens {
		out = append(out, tok.col)
	}

	return out
}
