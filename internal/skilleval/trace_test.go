package skilleval

import (
	"slices"
	"testing"
)

func TestTraceScripts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		trace   string
		want    []string
		wantErr bool
	}{
		{
			name: "should read fenced blocks from assistant text and Bash commands, skipping prose and other entries",
			trace: `{"type":"system","subtype":"init"}
{"type":"result","message":"a plain string"}
{"type":"user","message":{"content":[{"type":"text","text":"` + "```sh\\njev 'from the user'\\n```" + `"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Run jev 'in prose'.\n` + "```sh\\njev 'a'\\n```\\nand\\n```\\njev 'b'\\n```" + `"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"jev 'c' --print-request"}}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"x"}}]}}
`,
			want: []string{"jev 'a'\n", "jev 'b'\n", "jev 'c' --print-request"},
		},
		{
			name:    "should fail on a line that is not JSON",
			trace:   "not json\n",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := traceScripts([]byte(tc.trace))
			if (err != nil) != tc.wantErr {
				t.Fatalf("traceScripts(...) error = %v, wantErr %v", err, tc.wantErr)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("traceScripts(...) = %q, want %q", got, tc.want)
			}
		})
	}
}
