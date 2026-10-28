package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
)

func TestListModels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		response string
		status   int
		wantCode int
		wantOut  string
		wantErr  string
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
			name: "should strip terminal controls from what the api returned",
			response: `{"models":[` +
				`{"name":"jev\u001b[2J-latest","description":"al\u009bias\r","release_date":"2026\u0007-08-01"}]}`,
			wantCode: ExitOK,
			wantOut:  "jev[2J-latest  alias  \n",
		},
		{
			name: "should print the day of a release date that carries a time",
			response: `{"models":[` +
				`{"name":"jev-latest","description":"alias","release_date":"2026-08-01T09:30:00Z"}]}`,
			wantCode: ExitOK,
			wantOut:  "jev-latest  alias  2026-08-01\n",
		},
		{
			name:     "should print no date for a release date that is not a day",
			response: `{"models":[{"name":"jev-latest","description":"alias","release_date":"soon"}]}`,
			wantCode: ExitOK,
			wantOut:  "jev-latest  alias  \n",
		},
		{
			name:     "should strip terminal controls from an error message the api returned",
			response: `{"error":{"message":"invalid \u001b]0;owned\u0007api key"}}`,
			status:   401,
			wantCode: ExitAuth,
			wantOut:  "",
			wantErr:  "invalid ]0;ownedapi key",
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

			if !strings.Contains(errOut, tc.wantErr) || strings.ContainsAny(errOut, "\x1b\x07\r") {
				t.Errorf("stderr = %q, want it to hold %q and no terminal control", errOut, tc.wantErr)
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

	t.Run("should report --stats for a listing", func(t *testing.T) {
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
				wantErr:  "1 request, 0 questions asked, 0 in / 0 out tokens, 1 attempt, 10s/attempt,",
			},
			{
				name:     "should count a rejected listing as a failed request and not as a retry",
				status:   http.StatusUnauthorized,
				response: `{"error":{"message":"invalid api key"}}`,
				wantCode: ExitAuth,
				wantErr:  "1 request, 1 failed, 0 questions asked, 0 in / 0 out tokens, 1 attempt, 10s/attempt,",
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

				if strings.Contains(errOut, "model ") {
					t.Errorf("stderr should carry no model clause, got:\n%s", errOut)
				}

				if strings.Contains(out, "attempt") {
					t.Errorf("stdout should not carry the summary, got:\n%s", out)
				}
			})
		}
	})

	t.Run("should honour the retry flags", func(t *testing.T) {
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
					"--list-models", "--base-url", srv.URL,
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
	})

	t.Run("should list the models under levanto", func(t *testing.T) {
		t.Parallel()

		var path atomic.Value

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path.Store(r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[{"name":"levanto-sage-v1.4",` +
				`"description":"Levanto Sage decision model","release_date":"2026-10-04"},` +
				`{"name":"sage-latest","description":"Alias of levanto-sage-v1.4.","release_date":"2026-10-04"}]}`))
		}))
		defer srv.Close()

		out, errOut, code := runRealFactory(t, []string{"--provider", "levanto", "--list-models", "--base-url", srv.URL})
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		want := "levanto-sage-v1.4  Levanto Sage decision model  2026-10-04\n" +
			"sage-latest  Alias of levanto-sage-v1.4.  2026-10-04\n"
		if out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}

		if got := path.Load(); got != "/v1/models" {
			t.Errorf("path = %v, want /v1/models", got)
		}
	})

	t.Run("should list only the system-one models under berget", func(t *testing.T) {
		t.Parallel()

		var path atomic.Value

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path.Store(r.URL.Path)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[` +
				`{"id":"mistral","model_type":"text","aliases":["mistral-small"],"release_date":"2025-10-01"},` +
				`{"id":"Qwen/Qwen3.5-2B","model_type":"system-one","aliases":["systemone"],` +
				`"release_date":"2026-09-23"}]}`))
		}))
		defer srv.Close()

		out, errOut, code := runRealFactory(t, []string{"--provider", "berget", "--list-models", "--base-url", srv.URL})
		if code != ExitOK {
			t.Fatalf("exit code = %d, stderr:\n%s", code, errOut)
		}

		if want := "Qwen/Qwen3.5-2B  aliases systemone  2026-09-23\n"; out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}

		if got := path.Load(); got != "/v1/models/" {
			t.Errorf("path = %v, want /v1/models/", got)
		}
	})

	t.Run("should honour --timeout", func(t *testing.T) {
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
				"--list-models", "--base-url", srv.URL,
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
	})
}

func runRealFactory(t *testing.T, args []string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		withKeychain(noKeychain()),
		WithStdin(strings.NewReader("")),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(map[string]string{
			onesie.TypeSafe().EnvAPIKey(): "test", "BERGET_API_KEY": "test", "LEVANTO_API_KEY": "test",
		})),
		WithClock(instantClock{now: time.Now}),
	)

	root.Command.SetOut(&out)
	root.Command.SetErr(&errOut)
	root.Command.SetArgs(args)

	code := root.Execute(t.Context())

	return out.String(), errOut.String(), code
}
