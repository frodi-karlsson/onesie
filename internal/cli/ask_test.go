package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func TestNewAskCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantErr  bool
		contains string
		wantJSON bool
	}{
		{
			name:     "should print the probability for a question",
			args:     []string{"ask", "Is this urgent?", "--state", "Help, everything is down"},
			contains: "0.95",
		},
		{
			name:     "should read the state from stdin when it is a dash",
			args:     []string{"ask", "Is this urgent?", "--state", "-"},
			stdin:    "Help, everything is down",
			contains: "0.95",
		},
		{
			name:     "should print json when asked",
			args:     []string{"ask", "Is this urgent?", "--state", "x", "--json"},
			contains: `"noul"`,
			wantJSON: true,
		},
		{
			name:    "should require a question",
			args:    []string{"ask"},
			wantErr: true,
		},
		{
			name:    "should reject more than one question",
			args:    []string{"ask", "one", "two"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{
					"model":"jev-1.13.0",
					"answers":{"answer":{"type":"noul","noul":0.95}},
					"usage":{"input_tokens":10,"output_tokens":2}
				}`)
			}))
			defer server.Close()

			newClient := func(context.Context) (*jev.Client, error) {
				return jev.New(
					jev.WithEnv(func(string) (string, bool) { return "", false }),
					jev.WithAPIKey("sk-test"),
					jev.WithBaseURL(server.URL),
				)
			}

			var out bytes.Buffer

			root := cli.NewRootCmd(cli.BuildInfo{Version: "test"}, cli.WithClientFactory(newClient))
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetIn(strings.NewReader(tc.stdin))
			root.SetArgs(tc.args)

			err := root.ExecuteContext(t.Context())

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got none. output: %s", out.String())
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v. output: %s", err, out.String())
			}

			if !strings.Contains(out.String(), tc.contains) {
				t.Errorf("output missing %q\ngot:\n%s", tc.contains, out.String())
			}

			if tc.wantJSON {
				var parsed map[string]any
				if decodeErr := json.Unmarshal(out.Bytes(), &parsed); decodeErr != nil {
					t.Errorf("output was not valid json: %v", decodeErr)
				}
			}
		})
	}
}
