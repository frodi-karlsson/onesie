package skilleval

import (
	"slices"
	"testing"
)

func TestOnesieCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "should read a bare command",
			script: "onesie 'is this urgent' --state 'the server is down'",
			want:   []string{"onesie 'is this urgent' --state 'the server is down'"},
		},
		{
			name:   "should stop at a chain and drop a redirection, replacing the variable",
			script: `onesie --ask safe='is this safe' --assert 'safe.value > 0.7' --state "$cmd" >/dev/null && eval "$cmd"`,
			want:   []string{`onesie --ask safe='is this safe' --assert 'safe.value > 0.7' --state "{}"`},
		},
		{
			name:   "should find a command after if and drop 2>&1",
			script: `if onesie 'is this safe' -q --state "${cmd}" 2>&1; then eval "$cmd"; fi`,
			want:   []string{`onesie 'is this safe' -q --state "{}"`},
		},
		{
			name:   "should find a command inside a command substitution in double quotes",
			script: `out="$(onesie --ask a='x y' -o values --state "$t")"`,
			want:   []string{`onesie --ask a='x y' -o values --state "{}"`},
		},
		{
			name:   "should read the onesie side of a pipe on both ends",
			script: "cat t.jsonl | onesie --ask u='is this urgent' -i jsonl | jq -c 'select(.error == null)'",
			want:   []string{"onesie --ask u='is this urgent' -i jsonl"},
		},
		{
			name:   "should drop an input redirection and keep the flags after it",
			script: "onesie --ask u='urgent' -i jsonl < tickets.jsonl -j 8",
			want:   []string{"onesie --ask u='urgent' -i jsonl  -j 8"},
		},
		{
			name:   "should join a line continuation",
			script: "onesie --ask u='urgent' \\\n  --state 'down'",
			want:   []string{"onesie --ask u='urgent'    --state 'down'"},
		},
		{
			name:   "should read a YAML run step",
			script: "- run: onesie 'is this risky' --rate low,high --state 'diff'",
			want:   []string{"onesie 'is this risky' --rate low,high --state 'diff'"},
		},
		{
			name:   "should ignore onesie inside quotes, as an argument and in a comment",
			script: "echo 'run onesie now'\nwhich onesie\n# onesie 'is this urgent'",
			want:   nil,
		},
		{
			name:   "should find a command after a bare variable argument",
			script: `echo $cmd; onesie 'is this safe' --state "$cmd"`,
			want:   []string{`onesie 'is this safe' --state "{}"`},
		},
		{
			name:   "should replace a bare variable with a placeholder that stays one word",
			script: `onesie 'is this safe' --state $cmd`,
			want:   []string{`onesie 'is this safe' --state '{}'`},
		},
		{
			name:   "should treat a GitHub expression as one expansion",
			script: `onesie 'does this explain why' --state "${{ github.event.pull_request.body }}"`,
			want:   []string{`onesie 'does this explain why' --state "{}"`},
		},
		{
			name:   "should drop a quoted here string target with spaces",
			script: `onesie 'is this urgent' -i text <<< "a b" -o json`,
			want:   []string{`onesie 'is this urgent' -i text  -o json`},
		},
		{
			name:   "should find a command after a brace group, exec, command and env",
			script: "{ onesie 'a'; }\nexec onesie 'b'\ncommand onesie 'c'\nenv TYPESAFE_API_KEY=k onesie 'd'",
			want:   []string{"onesie 'a'", "onesie 'b'", "onesie 'c'", "onesie 'd'"},
		},
		{
			name:   "should read a command run by path",
			script: "./bin/onesie 'is this urgent' --state 'down'",
			want:   []string{"onesie 'is this urgent' --state 'down'"},
		},
		{
			name:   "should not read command -v onesie as a run",
			script: "command -v onesie",
			want:   nil,
		},
		{
			name:   "should ignore a word that only starts with onesie",
			script: "onesieons 'x'",
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := onesieCommands(tc.script); !slices.Equal(got, tc.want) {
				t.Errorf("onesieCommands(%q) = %q, want %q", tc.script, got, tc.want)
			}
		})
	}
}
