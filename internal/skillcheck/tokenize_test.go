package skillcheck

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		command    string
		want       []string
		wantReason string
	}{
		{
			name:    "should split a simple command into argv tokens",
			command: "onesie --ask a=x --model jev-latest",
			want:    []string{"onesie", "--ask", "a=x", "--model", "jev-latest"},
		},
		{
			name:    "should keep a single quoted question as one token",
			command: "onesie 'is this urgent'",
			want:    []string{"onesie", "is this urgent"},
		},
		{
			name:    "should keep a double quoted value as one token",
			command: `onesie --assert "a.value < 0.5"`,
			want:    []string{"onesie", "--assert", "a.value < 0.5"},
		},
		{
			name:    "should join a joined flag and a quoted value into one token",
			command: "onesie --ask a='x'",
			want:    []string{"onesie", "--ask", "a=x"},
		},
		{
			name:    "should strip a trailing redirection to dev null",
			command: "onesie --ask a=x >/dev/null",
			want:    []string{"onesie", "--ask", "a=x"},
		},
		{
			name:    "should strip a trailing stderr redirection combined with dev null",
			command: "onesie --ask a=x >/dev/null 2>&1",
			want:    []string{"onesie", "--ask", "a=x"},
		},
		{
			name:       "should reject an unterminated single quote",
			command:    "onesie --ask a='x",
			wantReason: "an unterminated single quote",
		},
		{
			name:       "should reject an unterminated double quote",
			command:    `onesie --ask a="x`,
			wantReason: "an unterminated double quote",
		},
		{
			name:       "should reject a command carrying a pipe",
			command:    "onesie --ask a=x | jq '.value'",
			wantReason: "contains a pipe",
		},
		{
			name:       "should reject a command carrying a chained command",
			command:    "onesie --ask a=x && onesie --ask b=y",
			wantReason: "contains a background operator",
		},
		{
			name:       "should reject a command carrying a command separator",
			command:    "onesie --ask a=x ; onesie --ask b=y",
			wantReason: "contains a command separator",
		},
		{
			name:       "should reject a command carrying a shell variable",
			command:    `onesie --state "$cmd"`,
			wantReason: "a variable or a command substitution",
		},
		{
			name:       "should reject a command carrying an unquoted shell variable",
			command:    "onesie --state $cmd",
			wantReason: "contains a variable or a command substitution",
		},
		{
			name:       "should reject a command carrying command substitution",
			command:    "onesie --state \"`cat file`\"",
			wantReason: "a variable or a command substitution",
		},
		{
			name:       "should reject a command redirected into a file",
			command:    "onesie --ask a=x > out.json",
			wantReason: "contains a redirection",
		},
		{
			name:       "should reject a command carrying a subshell",
			command:    "onesie --ask a=x (echo hi)",
			wantReason: "contains a subshell",
		},
		{
			name:       "should reject a command carrying a glob",
			command:    "onesie --state-file data/*.json",
			wantReason: "contains a glob",
		},
		{
			name:       "should reject a command carrying a bare question mark glob",
			command:    "onesie --state-file data?.json",
			wantReason: "contains a glob",
		},
		{
			name:       "should reject a command carrying a brace expansion",
			command:    "onesie --state-file data/{a,b}.json",
			wantReason: "contains a brace expansion",
		},
		{
			name:       "should reject a command carrying a home directory expansion",
			command:    "onesie --state-file ~/data.json",
			wantReason: "contains a home directory expansion",
		},
		{
			name:       "should reject a command carrying a comment marker",
			command:    "onesie --ask a=x # trailing note",
			wantReason: "contains a comment marker",
		},
		{
			name:       "should reject a trailing backslash",
			command:    `onesie --ask a=x \`,
			wantReason: "a trailing backslash",
		},
		{
			name:       "should reject an empty command",
			command:    "",
			wantReason: "an empty command",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, reason := Tokenize(tc.command)
			if reason != tc.wantReason {
				t.Fatalf("Tokenize(%q) reason = %q, want %q", tc.command, reason, tc.wantReason)
			}

			if tc.wantReason != "" {
				return
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Tokenize(%q) = %#v, want %#v", tc.command, got, tc.want)
			}
		})
	}
}
