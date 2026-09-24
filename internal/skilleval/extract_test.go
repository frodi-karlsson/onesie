package skilleval

import (
	"slices"
	"testing"
)

func TestJevCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "should read a bare command",
			script: "jev 'is this urgent' --state 'the server is down'",
			want:   []string{"jev 'is this urgent' --state 'the server is down'"},
		},
		{
			name:   "should stop at a chain and drop a redirection, replacing the variable",
			script: `jev --ask safe='is this safe' --assert 'safe.value > 0.7' --state "$cmd" >/dev/null && eval "$cmd"`,
			want:   []string{`jev --ask safe='is this safe' --assert 'safe.value > 0.7' --state "{}"`},
		},
		{
			name:   "should find a command after if and drop 2>&1",
			script: `if jev 'is this safe' -q --state "${cmd}" 2>&1; then eval "$cmd"; fi`,
			want:   []string{`jev 'is this safe' -q --state "{}"`},
		},
		{
			name:   "should find a command inside a command substitution in double quotes",
			script: `out="$(jev --ask a='x y' -o values --state "$t")"`,
			want:   []string{`jev --ask a='x y' -o values --state "{}"`},
		},
		{
			name:   "should read the jev side of a pipe on both ends",
			script: "cat t.jsonl | jev --ask u='is this urgent' -i jsonl | jq -c 'select(.error == null)'",
			want:   []string{"jev --ask u='is this urgent' -i jsonl"},
		},
		{
			name:   "should drop an input redirection and keep the flags after it",
			script: "jev --ask u='urgent' -i jsonl < tickets.jsonl -j 8",
			want:   []string{"jev --ask u='urgent' -i jsonl  -j 8"},
		},
		{
			name:   "should join a line continuation",
			script: "jev --ask u='urgent' \\\n  --state 'down'",
			want:   []string{"jev --ask u='urgent'    --state 'down'"},
		},
		{
			name:   "should read a YAML run step",
			script: "- run: jev 'is this risky' --rate low,high --state 'diff'",
			want:   []string{"jev 'is this risky' --rate low,high --state 'diff'"},
		},
		{
			name:   "should ignore jev inside quotes, as an argument and in a comment",
			script: "echo 'run jev now'\nwhich jev\n# jev 'is this urgent'",
			want:   nil,
		},
		{
			name:   "should find a command after a bare variable argument",
			script: `echo $cmd; jev 'is this safe' --state "$cmd"`,
			want:   []string{`jev 'is this safe' --state "{}"`},
		},
		{
			name:   "should replace a bare variable with a placeholder that stays one word",
			script: `jev 'is this safe' --state $cmd`,
			want:   []string{`jev 'is this safe' --state '{}'`},
		},
		{
			name:   "should treat a GitHub expression as one expansion",
			script: `jev 'does this explain why' --state "${{ github.event.pull_request.body }}"`,
			want:   []string{`jev 'does this explain why' --state "{}"`},
		},
		{
			name:   "should drop a quoted here string target with spaces",
			script: `jev 'is this urgent' -i text <<< "a b" -o json`,
			want:   []string{`jev 'is this urgent' -i text  -o json`},
		},
		{
			name:   "should find a command after a brace group, exec, command and env",
			script: "{ jev 'a'; }\nexec jev 'b'\ncommand jev 'c'\nenv TYPESAFE_API_KEY=k jev 'd'",
			want:   []string{"jev 'a'", "jev 'b'", "jev 'c'", "jev 'd'"},
		},
		{
			name:   "should read a command run by path",
			script: "./bin/jev 'is this urgent' --state 'down'",
			want:   []string{"jev 'is this urgent' --state 'down'"},
		},
		{
			name:   "should not read command -v jev as a run",
			script: "command -v jev",
			want:   nil,
		},
		{
			name:   "should ignore a word that only starts with jev",
			script: "jevons 'x'",
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := jevCommands(tc.script); !slices.Equal(got, tc.want) {
				t.Errorf("jevCommands(%q) = %q, want %q", tc.script, got, tc.want)
			}
		})
	}
}
