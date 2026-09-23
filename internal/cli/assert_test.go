package cli_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

const (
	urgent = `{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`
	routed = `{"model":"jev-1.13.0","answers":{"answer":{"type":"choice","choice":"billing",` +
		`"confidence":0.9,"probabilities":{"billing":0.8,"technical":0.2}}}}`
	unsure = `{"model":"jev-1.13.0","answers":{"answer":{"type":"choice","choice":"billing",` +
		`"confidence":0.3,"probabilities":{"billing":0.8,"technical":0.2}}}}`
	both = `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.9},` +
		`"b":{"type":"noul","noul":0.9}}}`
)

func TestAssert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		status    int
		response  string
		wantCode  int
		wantOut   string
		contains  []string
		missing   []string
		wantErr   string
		noRequest bool
	}{
		{
			name:     "should exit zero and write no assert key when the assertion holds",
			args:     []string{"is this urgent", "-o", "json", "--assert", "answer.value > 0.5"},
			response: urgent,
			wantCode: cli.ExitOK,
			contains: []string{`"answer":{"value":0.9}`},
			missing:  []string{"assert"},
		},
		{
			name:     "should print the record and exit one when the assertion is false",
			args:     []string{"is this urgent", "-o", "json", "--assert", "answer.value > 0.95"},
			response: urgent,
			wantCode: cli.ExitRejected,
			contains: []string{`"assert":false`, `"answer":{"value":0.9}`, `"model":"jev-1.13.0"`},
		},
		{
			name:     "should carry the key in values mode",
			args:     []string{"is this urgent", "-o", "values", "--assert", "answer.value > 0.95"},
			response: urgent,
			wantCode: cli.ExitRejected,
			wantOut:  "{\"assert\":false,\"answer\":0.9}\n",
		},
		{
			name:     "should print assert false beside the model in table mode",
			args:     []string{"is this urgent", "-o", "table", "--assert", "answer.value > 0.95"},
			response: urgent,
			wantCode: cli.ExitRejected,
			contains: []string{"model jev-1.13.0\nassert false\n", "answer  0.9000"},
		},
		{
			name:     "should print the bare scalar and nothing else in raw mode",
			args:     []string{"is this urgent", "-r", "--assert", "answer.value > 0.95"},
			response: urgent,
			wantCode: cli.ExitRejected,
			wantOut:  "0.9\n",
		},
		{
			name:     "should not evaluate the assertion on a failed request",
			args:     []string{"is this urgent", "-o", "json", "--assert", "answer.value > 0.5"},
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{`"kind":"http"`},
			missing:  []string{"assert"},
			wantErr:  "boom",
		},
		{
			name: "should combine several assertions with and",
			args: []string{
				"is this urgent", "-o", "json",
				"--assert", "answer.value > 0.5", "--assert", "answer.value > 0.95",
			},
			response: urgent,
			wantCode: cli.ExitRejected,
			contains: []string{`"assert":false`},
		},
		{
			name: "should hold when every combined assertion holds",
			args: []string{
				"is this urgent", "-o", "json",
				"--assert", "answer.value > 0.5", "--assert", "answer.value < 0.95",
			},
			response: urgent,
			wantCode: cli.ExitOK,
			missing:  []string{"assert"},
		},
		{
			name: "should carry the key inside the merge container",
			args: []string{
				"is this urgent", "-o", "json", "--merge",
				"--assert", "answer.value > 0.95",
			},
			response: urgent,
			wantCode: cli.ExitRejected,
			wantOut: "{\"state\":\"a ticket\",\"answers\":{\"assert\":false," +
				"\"model\":\"jev-1.13.0\",\"answer\":{\"value\":0.9}}}\n",
		},
		{
			name:      "should reject a bad expression before any request",
			args:      []string{"is this urgent", "--assert", "answer.value >"},
			response:  urgent,
			wantCode:  cli.ExitUsage,
			wantOut:   "",
			wantErr:   "jev: --assert: parse error at column 15",
			noRequest: true,
		},
		{
			name:      "should reject an unknown question before any request",
			args:      []string{"is this urgent", "--assert", "sevrity.value > 0.5"},
			response:  urgent,
			wantCode:  cli.ExitUsage,
			wantOut:   "",
			wantErr:   "jev: --assert: unknown question 'sevrity'. Questions: answer",
			noRequest: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, requests := runAsserted(t, tc.args, tc.status, tc.response, tc.wantCode)

			if tc.wantOut != "" && out != tc.wantOut {
				t.Errorf("stdout = %q, want %q", out, tc.wantOut)
			}

			if tc.wantOut == "" && tc.noRequest && out != "" {
				t.Errorf("stdout = %q, want nothing", out)
			}

			for _, want := range tc.contains {
				if !strings.Contains(out, want) {
					t.Errorf("stdout missing %q\ngot:\n%s", want, out)
				}
			}

			for _, unwanted := range tc.missing {
				if strings.Contains(out, unwanted) {
					t.Errorf("stdout carries %q\ngot:\n%s", unwanted, out)
				}
			}

			if tc.wantErr == "" && errOut != "" {
				t.Errorf("stderr = %q, want nothing", errOut)
			}

			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}

			if tc.noRequest && requests != 0 {
				t.Errorf("requests = %d, want none", requests)
			}
		})
	}
}

func TestAssertQuiet(t *testing.T) {
	t.Parallel()

	const holds = "answer.p.billing > 0.5"

	const fails = "answer.p.billing > 0.95"

	policed := []string{
		"which team", "--pick", "billing,technical",
		"--min-confidence", "0.7", "--fallback", "human", "-q",
	}

	tests := []struct {
		name      string
		assertion string
		response  string
		wantCode  int
	}{
		{
			name:      "should exit zero when the policy accepts and the assertion holds",
			assertion: holds,
			response:  routed,
			wantCode:  cli.ExitOK,
		},
		{
			name:      "should exit one when the policy accepts and the assertion is false",
			assertion: fails,
			response:  routed,
			wantCode:  cli.ExitRejected,
		},
		{
			name:      "should exit one when the policy rejects and the assertion holds",
			assertion: holds,
			response:  unsure,
			wantCode:  cli.ExitRejected,
		},
		{
			name:      "should exit one when the policy rejects and the assertion is false",
			assertion: fails,
			response:  unsure,
			wantCode:  cli.ExitRejected,
		},
		{
			name:      "should exit per section 12 when the request failed",
			assertion: fails,
			wantCode:  cli.ExitUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := append(append([]string{}, policed...), "--assert", tc.assertion)

			status := 0
			response := tc.response

			if response == "" {
				status = http.StatusInternalServerError
				response = `{"error":{"message":"boom"}}`
			}

			out, _, _ := runAsserted(t, args, status, response, tc.wantCode)

			if out != "" {
				t.Errorf("stdout = %q, want nothing under -q", out)
			}
		})
	}
}

func TestAssertQuietWithoutPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{
			name:     "should accept a pick under quiet when an assertion stands in for the policy",
			args:     []string{"which team", "--pick", "billing,technical", "-q", "--assert", "answer.p.billing > 0.5"},
			wantCode: cli.ExitOK,
		},
		{
			name:     "should reject a pick under quiet with neither a policy nor an assertion",
			args:     []string{"which team", "--pick", "billing,technical", "-q"},
			wantCode: cli.ExitUsage,
			wantErr: "jev: -q on 'answer' needs --min-confidence and --fallback, or --assert. " +
				"Without one the exit code is always 0",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, errOut, _ := runAsserted(t, tc.args, 0, routed, tc.wantCode)

			if tc.wantErr == "" && errOut != "" {
				t.Errorf("stderr = %q, want nothing", errOut)
			}

			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}
		})
	}
}

func TestAssertIsGlobal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{
			name: "should not bind an assertion to the group it sits between",
			args: []string{
				"-o", "json", "--ask", "a=first", "--assert", "a.value > 0.5",
				"--ask", "b=second",
			},
		},
		{
			name: "should accept an assertion over a group opened after it",
			args: []string{
				"-o", "json", "--ask", "a=first", "--assert", "b.value > 0.5",
				"--ask", "b=second",
			},
		},
		{
			name: "should accept an assertion before the first group",
			args: []string{
				"-o", "json", "--assert", "a.value > 0.5", "--ask", "a=first",
				"--ask", "b=second",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, _ := runAsserted(t, tc.args, 0, both, cli.ExitOK)

			if errOut != "" {
				t.Errorf("stderr = %q, want nothing", errOut)
			}

			for _, want := range []string{`"a":{"value":0.9}`, `"b":{"value":0.9}`} {
				if !strings.Contains(out, want) {
					t.Errorf("stdout missing %q\ngot:\n%s", want, out)
				}
			}
		})
	}
}

func runAsserted(
	t *testing.T,
	args []string,
	status int,
	response string,
	wantCode int,
) (string, string, int64) {
	t.Helper()

	var requests atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)

		if status != 0 {
			w.WriteHeader(status)
		}

		if _, err := w.Write([]byte(response)); err != nil {
			t.Errorf("writing stub response: %v", err)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer

	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "1.2.3"},
		cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
			// No retries. A 500 case would otherwise spend the client's backoff for no coverage.
			policy := jev.DefaultRetryPolicy()
			policy.MaxRetries = 0

			return jev.New(append([]jev.Option{
				jev.WithAPIKey("k"),
				jev.WithBaseURL(srv.URL),
				jev.WithRetry(policy),
			}, opts...)...)
		}),
		cli.WithStdin(strings.NewReader("a ticket")),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	if code := cli.Execute(t.Context(), root); code != wantCode {
		t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			code, wantCode, out.String(), errOut.String())
	}

	return out.String(), errOut.String(), requests.Load()
}
