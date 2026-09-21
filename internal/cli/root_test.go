package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func TestNewRootCmd(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.92}},` +
		`"usage":{"input_tokens":10,"output_tokens":2}}`

	const picked = `{"model":"jev-1.13.0","answers":{"answer":{"type":"choice",` +
		`"choice":"billing","confidence":0.91,` +
		`"probabilities":{"billing":0.91,"technical":0.09}}},` +
		`"usage":{"input_tokens":10,"output_tokens":2}}`

	tests := []struct {
		name     string
		args     []string
		stdin    string
		response string
		wantCode int
		contains []string
		absent   []string
	}{
		{
			name:     "should print help when given no arguments",
			args:     []string{},
			wantCode: cli.ExitOK,
			contains: []string{"Usage:", "jev [question]"},
		},
		{
			name:     "should print the limits for the version flag",
			args:     []string{"-V"},
			wantCode: cli.ExitOK,
			contains: []string{"jev 1.2.3", "max-choice-options 255", "max-retry-after 1m0s"},
		},
		{
			name:     "should print full provenance for the version subcommand",
			args:     []string{"version"},
			wantCode: cli.ExitOK,
			contains: []string{"version   1.2.3", "commit    abc1234", "min-score-levels 2"},
		},
		{
			name:     "should answer a positional question from stdin",
			args:     []string{"is this urgent", "-o", "json"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"answer":{"value":0.92}`, `"model":"jev-1.13.0"`},
			absent:   []string{`"usage"`},
		},
		{
			name:     "should add usage when asked",
			args:     []string{"is this urgent", "-o", "json", "--usage"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"usage":{"input_tokens":10,"output_tokens":2}`},
		},
		{
			name:     "should print the bare value with -r",
			args:     []string{"is this urgent", "-r"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{"0.92"},
		},
		{
			name:     "should answer a pick question",
			args:     []string{"which team", "--pick", "billing,technical", "-r"},
			stdin:    "i was charged twice",
			response: picked,
			wantCode: cli.ExitOK,
			contains: []string{"billing"},
		},
		{
			name:     "should exit ok with -q above the threshold",
			args:     []string{"is this urgent", "-q", "--threshold", "0.9"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			absent:   []string{"0.92"},
		},
		{
			name:     "should exit rejected with -q below the threshold",
			args:     []string{"is this urgent", "-q", "--threshold", "0.95"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitRejected,
		},
		{
			name:     "should reject a state-less invocation",
			args:     []string{"is this urgent"},
			wantCode: cli.ExitUsage,
			contains: []string{"no state given"},
		},
		{
			name:     "should reject -r with two questions",
			args:     []string{"--ask", "a=one", "--ask", "b=two", "-r"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"-r needs a single question"},
		},
		{
			name:     "should reject a reserved question id",
			args:     []string{"--ask", "model=one"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"'model' is reserved"},
		},
		{
			name: "should warn about a partly described option set",
			args: []string{
				"which team", "--pick", "billing,technical", "--desc", "billing=payments", "-r",
			},
			stdin:    "i was charged twice",
			response: picked,
			wantCode: cli.ExitOK,
			contains: []string{"warning:", "but not technical"},
		},
		{
			name:     "should reject an unknown output mode",
			args:     []string{"is this urgent", "-o", "yaml"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"-o takes"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if _, err := w.Write([]byte(tc.response)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			var out bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3", Commit: "abc1234", Date: "2026-01-01"},
				cli.WithClientFactory(func(context.Context) (*jev.Client, error) {
					return jev.New(jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL))
				}),
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tc.args)

			code := cli.Execute(t.Context(), root)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\noutput:\n%s", code, tc.wantCode, out.String())
			}

			for _, want := range tc.contains {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q\ngot:\n%s", want, out.String())
				}
			}

			for _, unwanted := range tc.absent {
				if strings.Contains(out.String(), unwanted) {
					t.Errorf("output should not contain %q\ngot:\n%s", unwanted, out.String())
				}
			}
		})
	}
}
