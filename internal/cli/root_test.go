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
		status   int
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
			name:     "should prefix a cobra parse error",
			args:     []string{"--nope"},
			wantCode: cli.ExitUsage,
			contains: []string{"jev: unknown flag: --nope"},
		},
		{
			name:     "should document how to ask a question that begins with a dash",
			args:     []string{"--help"},
			wantCode: cli.ExitOK,
			contains: []string{"begins with a dash", "jev -o json -- '-is this urgent'"},
		},
		{
			name:     "should answer a question that begins with a dash after --",
			args:     []string{"-o", "json", "--", "-is this urgent"},
			stdin:    "the server is down",
			response: answered,
			wantCode: cli.ExitOK,
			contains: []string{`"answer":{"value":0.92}`},
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
			contains: []string{"jev: no state given. Pipe one to stdin, or pass --state or --state-file"},
		},
		{
			name:     "should reject a flagged invocation that carries no question",
			args:     []string{"-o", "json"},
			stdin:    "the server is down",
			wantCode: cli.ExitUsage,
			contains: []string{"jev: no question given. Pass a question, --ask, or -f"},
			absent:   []string{"Usage:", "Flags:"},
		},
		{
			name:     "should reject -r with two questions",
			args:     []string{"--ask", "a=one", "--ask", "b=two", "-r"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"-r needs a single question"},
		},
		{
			name:     "should reject -o raw with two questions",
			args:     []string{"--ask", "a=one", "--ask", "b=two", "-o", "raw"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"jev: -o raw needs a single question. 'a', 'b' were asked"},
		},
		{
			name:     "should reject two --ask flags sharing an id",
			args:     []string{"--ask", "a=one", "--ask", "a=two"},
			stdin:    "body",
			wantCode: cli.ExitUsage,
			contains: []string{"jev: question id 'a' is given twice"},
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
		{
			name: "should print the fallback word when the request fails",
			args: []string{
				"is this safe", "--pick", "safe,refuse",
				"--min-confidence", "0.7", "--fallback", "refuse", "-r",
			},
			stdin:    "rm -rf /",
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{"refuse"},
		},
		{
			name:     "should print an empty line when a failed question has no fallback",
			args:     []string{"is this urgent", "-r"},
			stdin:    "body",
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			absent:   []string{"refuse"},
		},
		{
			name: "should emit an http error for a 200 body it cannot use",
			args: []string{
				"--ask", "team=which team", "--pick", "billing,technical",
				"--min-confidence", "0.7", "--fallback", "human", "-o", "json",
			},
			stdin:    "body",
			response: "not json at all",
			wantCode: cli.ExitUnavailable,
			contains: []string{
				`"error"`, `"kind":"http"`, `"status":200`,
				`"decision":"human"`, `"fallback":"error"`,
			},
			absent: []string{`"kind":"transport"`},
		},
		{
			name: "should emit the error key in json on failure",
			args: []string{
				"--ask", "team=which team", "--pick", "billing,technical",
				"--min-confidence", "0.7", "--fallback", "human", "-o", "json",
			},
			stdin:    "body",
			status:   http.StatusInternalServerError,
			response: `{"error":{"message":"boom"}}`,
			wantCode: cli.ExitUnavailable,
			contains: []string{
				`"error"`, `"kind":"http"`, `"status":500`,
				`"decision":"human"`, `"fallback":"error"`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status := tc.status
				if status == 0 {
					status = http.StatusOK
				}

				w.WriteHeader(status)

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

func TestNewRootCmdFlagDrivenClient(t *testing.T) {
	t.Parallel()

	t.Run("should build a client from --api-key and --base-url", func(t *testing.T) {
		t.Parallel()

		var gotAuth string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")

			if _, err := w.Write([]byte(
				`{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`,
			)); err != nil {
				t.Errorf("writing stub response: %v", err)
			}
		}))
		defer srv.Close()

		var out bytes.Buffer

		root := cli.NewRootCmd(
			cli.BuildInfo{Version: "1.2.3"},
			cli.WithStdin(strings.NewReader("body")),
			cli.WithStdinTTY(false),
			cli.WithStdoutTTY(false),
			cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
		)

		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs([]string{
			"is this urgent", "-r", "--api-key", "secret", "--base-url", srv.URL,
		})

		if code := cli.Execute(t.Context(), root); code != cli.ExitOK {
			t.Fatalf("exit code = %d, output:\n%s", code, out.String())
		}

		if !strings.Contains(gotAuth, "secret") {
			t.Errorf("Authorization header = %q, want it to carry the flag's key", gotAuth)
		}
	})
}

func TestNewRootCmdEnvDrivenClient(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`

	t.Run("should build a client from the injected environment", func(t *testing.T) {
		t.Parallel()

		var gotAuth string

		srv := stubAnswering(t, answered, func(r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
		})
		defer srv.Close()

		env := map[string]string{
			jev.EnvAPIKey:  "from-env",
			jev.EnvBaseURL: srv.URL,
		}

		out, code := runWithEnv(t, env, []string{"is this urgent", "-r"})

		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, output:\n%s", code, out)
		}

		if !strings.Contains(gotAuth, "from-env") {
			t.Errorf("Authorization header = %q, want it to carry the environment key", gotAuth)
		}
	})

	t.Run("should let --base-url override the environment", func(t *testing.T) {
		t.Parallel()

		var wanted, unwanted int

		flagged := stubAnswering(t, answered, func(*http.Request) { wanted++ })
		defer flagged.Close()

		fromEnv := stubAnswering(t, answered, func(*http.Request) { unwanted++ })
		defer fromEnv.Close()

		env := map[string]string{
			jev.EnvAPIKey:  "from-env",
			jev.EnvBaseURL: fromEnv.URL,
		}

		out, code := runWithEnv(t, env,
			[]string{"is this urgent", "-r", "--base-url", flagged.URL})

		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, output:\n%s", code, out)
		}

		if wanted != 1 {
			t.Errorf("the flag's base url took %d requests, want 1", wanted)
		}

		if unwanted != 0 {
			t.Errorf("the environment's base url took %d requests, want 0", unwanted)
		}
	})
}

func runWithEnv(t *testing.T, env map[string]string, args []string) (string, int) {
	t.Helper()

	var out bytes.Buffer

	// No WithClientFactory, so the real factory runs and the injected lookup is the only thing
	// standing between it and the process environment.
	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "1.2.3"},
		cli.WithStdin(strings.NewReader("body")),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		cli.WithLookupEnv(func(name string) (string, bool) {
			value, ok := env[name]

			return value, ok
		}),
	)

	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)

	code := cli.Execute(t.Context(), root)

	return out.String(), code
}

func stubAnswering(t *testing.T, body string, observe func(*http.Request)) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observe(r)

		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("writing stub response: %v", err)
		}
	}))
}
