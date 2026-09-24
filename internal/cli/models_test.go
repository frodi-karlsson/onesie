package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestListModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		status   int
		wantCode int
		wantOut  string
	}{
		{
			name: "should print one line per model in the order the api returned them",
			response: `{"models":[` +
				`{"name":"jev-latest","description":"alias","release_date":"2026-08-01"},` +
				`{"name":"onesie-1.13.0","description":"current","release_date":"2026-08-01"}]}`,
			wantCode: ExitOK,
			wantOut: "jev-latest  alias  2026-08-01\n" +
				"onesie-1.13.0  current  2026-08-01\n",
		},
		{
			name:     "should print nothing when the account has no models",
			response: `{"models":[]}`,
			wantCode: ExitOK,
			wantOut:  "",
		},
		{
			name:     "should exit 3 on a rejected key",
			response: `{"error":{"message":"invalid api key"}}`,
			status:   401,
			wantCode: ExitAuth,
			wantOut:  "",
		},
		{
			name:     "should exit 3 on a forbidden key",
			response: `{"error":{"message":"forbidden"}}`,
			status:   403,
			wantCode: ExitAuth,
			wantOut:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sent, out, errOut, code := runRequestMode(
				t, []string{"--list-models"}, "", tc.status, tc.response)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out, errOut)
			}

			if out != tc.wantOut {
				t.Errorf("stdout = %q, want %q", out, tc.wantOut)
			}

			// The listing is the whole output, so a success case that wrote anything to stderr
			// would be writing part of it to the wrong stream.
			if tc.wantCode == ExitOK && errOut != "" {
				t.Errorf("stderr should be empty, got:\n%s", errOut)
			}

			if len(sent) != 1 {
				t.Fatalf("made %d requests, want 1", len(sent))
			}

			if sent[0] != "" {
				t.Errorf("request body = %q, want no body at all", sent[0])
			}
		})
	}
}

func TestNewRootCmdListModelsFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "should reject a positional question with --list-models",
			args:    []string{"--list-models", "is this urgent"},
			wantErr: "onesie: --list-models asks no question. Drop the question argument",
		},
		{
			name:    "should reject --ask with --list-models",
			args:    []string{"--list-models", "--ask", "urgent=is this urgent"},
			wantErr: "onesie: --list-models asks no question. Drop --ask",
		},
		{
			name:    "should reject --pick with --list-models",
			args:    []string{"--list-models", "--pick", "a,b"},
			wantErr: "onesie: --list-models asks no question. Drop --pick",
		},
		{
			name:    "should reject --fallback with --list-models",
			args:    []string{"--list-models", "--fallback", "maybe"},
			wantErr: "onesie: --list-models asks no question. Drop --fallback",
		},
		{
			name:    "should reject -f with --list-models before it is read",
			args:    []string{"--list-models", "-f", "nowhere.yaml"},
			wantErr: "onesie: -f does not apply to --list-models, which asks no question",
		},
		{
			name:    "should reject --replace with --list-models",
			args:    []string{"--list-models", "--replace"},
			wantErr: "onesie: --replace applies to -f, which --list-models does not accept",
		},
		{
			name:    "should reject --state with --list-models",
			args:    []string{"--list-models", "--state", "x"},
			wantErr: "onesie: --state does not apply to --list-models, which reads no state",
		},
		{
			name:    "should reject --state-file with --list-models",
			args:    []string{"--list-models", "--state-file", "x"},
			wantErr: "onesie: --state-file does not apply to --list-models, which reads no state",
		},
		{
			// --input's default is text, so a rejection here can only come from Changed, not from
			// the value differing from the default.
			name:    "should reject --input given by its long name with --list-models",
			args:    []string{"--list-models", "--input", "text"},
			wantErr: "onesie: -i does not apply to --list-models, which reads no input",
		},
		{
			// --jobs's default is 1, for the same reason as the --input case above.
			name:    "should reject --jobs given by its long name with --list-models",
			args:    []string{"--list-models", "--jobs", "1"},
			wantErr: "onesie: -j does not apply to --list-models, which makes one request",
		},
		{
			name:    "should reject --model given by its long name with --list-models",
			args:    []string{"--list-models", "--model", "x"},
			wantErr: "onesie: -m names a model to ask, which --list-models does not do",
		},
		{
			name:    "should reject -i with --list-models",
			args:    []string{"--list-models", "-i", "jsonl"},
			wantErr: "onesie: -i does not apply to --list-models, which reads no input",
		},
		{
			name:    "should reject -i request with --list-models by name",
			args:    []string{"--list-models", "-i", "request"},
			wantErr: "onesie: -i does not apply to --list-models, which reads no input",
		},
		{
			name: "should blame --list-models rather than -i request for a shared offender",
			args: []string{
				"--list-models", "-i", "request", "--ask", "urgent=is this urgent",
			},
			wantErr: "onesie: --list-models asks no question. Drop --ask",
		},
		{
			name:    "should reject -o with --list-models",
			args:    []string{"--list-models", "-o", "json"},
			wantErr: "onesie: -o does not apply to --list-models, which writes a fixed listing",
		},
		{
			// Its own rule rather than the -o one, since run leaves Output empty for a bare -r.
			name:    "should reject -r with --list-models",
			args:    []string{"--list-models", "-r"},
			wantErr: "onesie: -r does not apply to --list-models, which writes a fixed listing",
		},
		{
			name:    "should reject -q with --list-models",
			args:    []string{"--list-models", "-q"},
			wantErr: "onesie: -q suppresses output, which leaves --list-models nothing to write",
		},
		{
			name:    "should reject --assert with --list-models",
			args:    []string{"--list-models", "--assert", "answer.value > 0.5"},
			wantErr: "onesie: --assert judges an answer, which --list-models does not produce",
		},
		{
			name:    "should reject an unparseable --assert with --list-models",
			args:    []string{"--list-models", "--assert", "nonsense syntax here !!"},
			wantErr: "onesie: --assert judges an answer, which --list-models does not produce",
		},
		{
			name: "should reject --usage with --list-models",
			args: []string{"--list-models", "--usage"},
			wantErr: "onesie: --usage reports the tokens a question cost, " +
				"which --list-models does not ask",
		},
		{
			name: "should reject --merge with --list-models",
			args: []string{"--list-models", "--merge"},
			wantErr: "onesie: --merge needs answers to fold in, " +
				"which --list-models does not produce",
		},
		{
			name: "should name --merge-key when that is the merge flag given",
			args: []string{"--list-models", "--merge-key", "out"},
			wantErr: "onesie: --merge-key needs answers to fold in, " +
				"which --list-models does not produce",
		},
		{
			name:    "should reject -j with --list-models",
			args:    []string{"--list-models", "-j", "4"},
			wantErr: "onesie: -j does not apply to --list-models, which makes one request",
		},
		{
			name:    "should reject -m with --list-models",
			args:    []string{"--list-models", "-m", "onesie-1.13.0"},
			wantErr: "onesie: -m names a model to ask, which --list-models does not do",
		},
		{
			name: "should reject --unordered with --list-models",
			args: []string{"--list-models", "--unordered"},
			wantErr: "onesie: --unordered applies to streaming input, " +
				"which --list-models does not read",
		},
		{
			name: "should reject --stop-on-error with --list-models",
			args: []string{"--list-models", "--stop-on-error"},
			wantErr: "onesie: --stop-on-error applies to streaming input, " +
				"which --list-models does not read",
		},
		{
			name: "should reject --stop-on-assert with --list-models",
			args: []string{"--list-models", "--stop-on-assert"},
			wantErr: "onesie: --stop-on-assert applies to streaming input, " +
				"which --list-models does not read",
		},
		{
			name: "should reject --skip-blank with --list-models",
			args: []string{"--list-models", "--skip-blank"},
			wantErr: "onesie: --skip-blank applies to streaming input, " +
				"which --list-models does not read",
		},
		{
			name: "should reject --print-request with --list-models",
			args: []string{"--list-models", "--print-request"},
			wantErr: "onesie: --print-request and --list-models each write a different thing " +
				"to stdout. Pass one",
		},
		{
			name: "should reject --print-questions with --list-models",
			args: []string{"--list-models", "--print-questions"},
			wantErr: "onesie: --print-questions and --list-models each write a different thing " +
				"to stdout. Pass one",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runOfflineStdin(t, tc.args, `{"state":"x"}`+"\n")

			if code != ExitUsage {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, ExitUsage, out, errOut)
			}

			if strings.TrimSpace(errOut) != tc.wantErr {
				t.Errorf("stderr = %q, want %q", strings.TrimSpace(errOut), tc.wantErr)
			}

			if out != "" {
				t.Errorf("stdout should be empty, got:\n%s", out)
			}
		})
	}
}

func TestNewRootCmdListModelsStats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		response string
		wantCode int
		wantErr  string
	}{
		{
			name: "should report one request, no questions and no tokens",
			response: `{"models":[` +
				`{"name":"onesie-1.13.0","description":"current","release_date":"2026-08-01"}]}`,
			wantCode: ExitOK,
			wantErr:  "1 request, 0 questions, 0 in / 0 out, 1 attempt, 10s/attempt,",
		},
		{
			// A rejected key ends the request rather than causing a retry, so the attempt it
			// spent belongs in the attempt count and nowhere in the breakdown.
			name:     "should count a rejected listing as a failed request and not as a retry",
			status:   http.StatusUnauthorized,
			response: `{"error":{"message":"invalid api key"}}`,
			wantCode: ExitAuth,
			wantErr:  "1 request, 1 failed, 0 questions, 0 in / 0 out, 1 attempt, 10s/attempt,",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, out, errOut, code := runRequestMode(
				t, []string{"--list-models", "--stats"}, "", tc.status, tc.response)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out, errOut)
			}

			if !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr missing %q\ngot:\n%s", tc.wantErr, errOut)
			}

			// The ids in a listing are the models an account may ask, not the model that answered
			// anything, so a model clause here would name one that never ran.
			if strings.Contains(errOut, "model ") {
				t.Errorf("stderr should carry no model clause, got:\n%s", errOut)
			}

			if strings.Contains(out, "attempt") {
				t.Errorf("stdout should not carry the summary, got:\n%s", out)
			}
		})
	}
}

func TestNewRootCmdListModelsRetryFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		attempts int64
	}{
		{
			name:     "should make one attempt with retries disabled",
			args:     []string{"--retries", "0"},
			attempts: 1,
		},
		{
			name:     "should make three attempts by default",
			args:     []string{},
			attempts: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var attempts atomic.Int64

			srv := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) {
					attempts.Add(1)
					w.WriteHeader(http.StatusServiceUnavailable)
				}))
			defer srv.Close()

			args := append([]string{
				"--list-models", "--base-url", srv.URL, "--api-key", "test",
			}, tc.args...)

			out, errOut, code := runRealFactory(t, args)

			if code == ExitOK {
				t.Fatalf("exit code = %d, want a failure\nstdout:\n%s\nstderr:\n%s",
					code, out, errOut)
			}

			if got := attempts.Load(); got != tc.attempts {
				t.Errorf("attempts = %d, want %d", got, tc.attempts)
			}
		})
	}
}

func TestNewRootCmdListModelsTimeoutFlag(t *testing.T) {
	t.Parallel()

	t.Run("should abandon a listing the server never answers", func(t *testing.T) {
		t.Parallel()

		blocked := make(chan struct{})

		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			<-blocked
		}))

		// Both through Cleanup rather than defer, and in this order, so the handler is released
		// before Close waits on it.
		t.Cleanup(srv.Close)
		t.Cleanup(func() { close(blocked) })

		out, errOut, code := runRealFactory(t, []string{
			"--list-models", "--base-url", srv.URL, "--api-key", "test",
			"--timeout", "1", "--retries", "0",
		})

		if code != ExitTransport {
			t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
				code, ExitTransport, out, errOut)
		}

		// The duration is part of the claim. Without the flag on the client the attempt runs to
		// the ten second default, which this handler outlasts.
		if !strings.Contains(errOut, "timed out after 1s") {
			t.Errorf("stderr = %q, want it to name the one second timeout", errOut)
		}
	})
}

// runRealFactory runs the given arguments through the client factory the binary uses, which is the
// only one the timing flags are wired onto.
func runRealFactory(t *testing.T, args []string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithStdin(strings.NewReader("")),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(func(string) (string, bool) { return "", false }),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}
