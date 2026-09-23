package skillcheck

import (
	"reflect"
	"testing"
)

func TestTokenize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    []string
		wantOK  bool
	}{
		{
			name:    "should split a simple command into argv tokens",
			command: "jev --ask a=x --model jev-latest",
			want:    []string{"jev", "--ask", "a=x", "--model", "jev-latest"},
			wantOK:  true,
		},
		{
			name:    "should keep a single quoted question as one token",
			command: "jev 'is this urgent'",
			want:    []string{"jev", "is this urgent"},
			wantOK:  true,
		},
		{
			name:    "should keep a double quoted value as one token",
			command: `jev --assert "a.value < 0.5"`,
			want:    []string{"jev", "--assert", "a.value < 0.5"},
			wantOK:  true,
		},
		{
			name:    "should join a joined flag and a quoted value into one token",
			command: "jev --ask a='x'",
			want:    []string{"jev", "--ask", "a=x"},
			wantOK:  true,
		},
		{
			name:    "should strip a trailing redirection to dev null",
			command: "jev --ask a=x >/dev/null",
			want:    []string{"jev", "--ask", "a=x"},
			wantOK:  true,
		},
		{
			name:    "should strip a trailing stderr redirection combined with dev null",
			command: "jev --ask a=x >/dev/null 2>&1",
			want:    []string{"jev", "--ask", "a=x"},
			wantOK:  true,
		},
		{
			name:    "should reject an unterminated single quote",
			command: "jev --ask a='x",
			wantOK:  false,
		},
		{
			name:    "should reject an unterminated double quote",
			command: `jev --ask a="x`,
			wantOK:  false,
		},
		{
			name:    "should reject a command carrying a pipe",
			command: "jev --ask a=x | jq '.value'",
			wantOK:  false,
		},
		{
			name:    "should reject a command carrying a chained command",
			command: "jev --ask a=x && jev --ask b=y",
			wantOK:  false,
		},
		{
			name:    "should reject a command carrying a shell variable",
			command: `jev --state "$cmd"`,
			wantOK:  false,
		},
		{
			name:    "should reject a command carrying command substitution",
			command: "jev --state \"`cat file`\"",
			wantOK:  false,
		},
		{
			name:    "should reject a command redirected into a file",
			command: "jev --ask a=x > out.json",
			wantOK:  false,
		},
		{
			name:    "should reject an empty command",
			command: "",
			wantOK:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tokenize(tc.command)
			if ok != tc.wantOK {
				t.Fatalf("tokenize(%q) ok = %v, want %v", tc.command, ok, tc.wantOK)
			}

			if !tc.wantOK {
				return
			}

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("tokenize(%q) = %#v, want %#v", tc.command, got, tc.want)
			}
		})
	}
}
