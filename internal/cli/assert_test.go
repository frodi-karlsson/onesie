package cli_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/cli"
	"github.com/frodi-karlsson/onesie/internal/jev"
)

const (
	urgent = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`
	routed = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"choice","choice":"billing",` +
		`"confidence":0.9,"probabilities":{"billing":0.8,"technical":0.2}}}}`
	unsure = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"choice","choice":"billing",` +
		`"confidence":0.3,"probabilities":{"billing":0.8,"technical":0.2}}}}`
	both = `{"model":"onesie-1.13.0","answers":{"a":{"type":"noul","noul":0.9},` +
		`"b":{"type":"noul","noul":0.9}}}`
)

func TestAsk(t *testing.T) {
	t.Parallel()

	t.Run("should gate a single record on its assertion", func(t *testing.T) {
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
				contains: []string{`"assert":false`, `"answer":{"value":0.9}`, `"model":"onesie-1.13.0"`},
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
				contains: []string{"model onesie-1.13.0\nassert false\n", "answer  0.9000"},
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
					"\"model\":\"onesie-1.13.0\",\"answer\":{\"value\":0.9}}}\n",
			},
			{
				name: "should count a false assertion in the stats line",
				args: []string{
					"is this urgent", "-o", "json", "--assert", "answer.value > 0.95", "--stats",
				},
				response: urgent,
				wantCode: cli.ExitRejected,
				contains: []string{`"assert":false`},
				wantErr:  "1 request, 1 false assertion, 1 question",
			},
			{
				name:      "should reject a bad expression before any request",
				args:      []string{"is this urgent", "--assert", "answer.value >"},
				response:  urgent,
				wantCode:  cli.ExitUsage,
				wantOut:   "",
				wantErr:   "onesie: --assert: parse error at column 15",
				noRequest: true,
			},
			{
				name:      "should reject an unknown question before any request",
				args:      []string{"is this urgent", "--assert", "sevrity.value > 0.5"},
				response:  urgent,
				wantCode:  cli.ExitUsage,
				wantOut:   "",
				wantErr:   "onesie: --assert: unknown question 'sevrity'. Questions: answer",
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
	})

	t.Run("should follow the assertion under -q", func(t *testing.T) {
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
				name:      "should follow the assertion alone when the policy rejects and the assertion holds",
				assertion: holds,
				response:  unsure,
				wantCode:  cli.ExitOK,
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
	})

	t.Run("should write nothing when the run is cancelled", func(t *testing.T) {
		t.Parallel()

		t.Run("should write nothing when a single record run is cancelled", func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())

			var once sync.Once

			started := make(chan struct{})

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				once.Do(func() { close(started) })

				// Bounded rather than waiting on the request context. A net/http server only notices
				// a client that hung up when it next reads the connection, so a handler blocked on
				// Done would hold Close open past the test's deadline.
				time.Sleep(200 * time.Millisecond)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()

			go func() {
				<-started
				cancel()
			}()

			var out, errOut bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3"},
				cli.WithKeychain(offKeychain{}),
				cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
					return jev.New(append([]jev.Option{
						jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL),
					}, opts...)...)
				}),
				cli.WithStdin(strings.NewReader("a ticket")),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs([]string{"is this urgent", "-o", "values"})

			if code := cli.Execute(ctx, root); code != cli.ExitInterrupt {
				t.Errorf("exit code = %d, want %d", code, cli.ExitInterrupt)
			}

			// The caller ended the run themselves. A transport record written into the pipe they were
			// closing reports a network fault that never happened.
			if out.String() != "" {
				t.Errorf("stdout = %q, want nothing", out.String())
			}

			if errOut.String() != "" {
				t.Errorf("stderr = %q, want nothing", errOut.String())
			}
		})
	})
}

func TestBuild(t *testing.T) {
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
			wantErr: "onesie: -q on 'answer' needs --min-confidence and --fallback, or --assert. " +
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

func TestGateOf(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"onesie-1.13.0","answers":{"urgent":{"type":"noul","noul":0.9}}}`

	const picked = `{"model":"onesie-1.13.0","answers":{"team":{"type":"choice",` +
		`"choice":"billing","confidence":0.9,` +
		`"probabilities":{"billing":0.8,"technical":0.2}}}}`

	const question = "urgent: is this urgent\n"

	tests := []struct {
		name      string
		file      string
		args      []string
		response  string
		wantCode  int
		contains  []string
		missing   []string
		wantErr   string
		noRequest bool
	}{
		{
			name:     "should exit one when the file's assertion is false",
			file:     "assert: urgent.value > 0.95\n" + question,
			args:     []string{"-o", "json"},
			wantCode: cli.ExitRejected,
			contains: []string{`"assert":false`, `"urgent":{"value":0.9}`},
		},
		{
			name:     "should exit zero when the file's assertion holds",
			file:     "assert: urgent.value > 0.5\n" + question,
			args:     []string{"-o", "json"},
			wantCode: cli.ExitOK,
			contains: []string{`"urgent":{"value":0.9}`},
			missing:  []string{"assert"},
		},
		{
			name:     "should exit one when the command line's assertion is false",
			file:     "assert: urgent.value > 0.5\n" + question,
			args:     []string{"-o", "json", "--assert", "urgent.value > 0.95"},
			wantCode: cli.ExitRejected,
			contains: []string{`"assert":false`},
		},
		{
			name:     "should exit one when the file's assertion is the false half",
			file:     "assert: urgent.value > 0.95\n" + question,
			args:     []string{"-o", "json", "--assert", "urgent.value > 0.5"},
			wantCode: cli.ExitRejected,
			contains: []string{`"assert":false`},
		},
		{
			name:     "should exit zero when both assertions hold",
			file:     "assert: urgent.value > 0.5\n" + question,
			args:     []string{"-o", "json", "--assert", "urgent.value < 0.95"},
			wantCode: cli.ExitOK,
			missing:  []string{"assert"},
		},
		{
			name:      "should name the file's key when its assertion cannot parse",
			file:      "assert: urgent.value >\n" + question,
			wantCode:  cli.ExitUsage,
			wantErr:   "onesie: 'assert': parse error at column 15",
			noRequest: true,
		},
		{
			name:      "should name the file's key when its assertion names an unknown question",
			file:      "assert: sevrity.value > 0.5\n" + question,
			wantCode:  cli.ExitUsage,
			wantErr:   "onesie: 'assert': unknown question 'sevrity'. Questions: urgent",
			noRequest: true,
		},
		{
			name:      "should name the flag when the command line's assertion is the bad one",
			file:      "assert: urgent.value > 0.5\n" + question,
			args:      []string{"--assert", "sevrity.value > 0.5"},
			wantCode:  cli.ExitUsage,
			wantErr:   "onesie: --assert: unknown question 'sevrity'. Questions: urgent",
			noRequest: true,
		},
		{
			name:      "should report a bad path in --abstain-if before any request",
			file:      "assert: urgent.value > 0.5\n" + question,
			args:      []string{"--abstain-if", "sevrity.value > 0.5"},
			wantCode:  cli.ExitUsage,
			wantErr:   "onesie: --abstain-if: unknown question 'sevrity'. Questions: urgent",
			noRequest: true,
		},
		{
			name:      "should name the file's key when its abstain expression names an unknown question",
			file:      "assert: urgent.value > 0.5\nabstain_if: sevrity.value > 0.5\n" + question,
			wantCode:  cli.ExitUsage,
			wantErr:   "onesie: 'abstain_if': unknown question 'sevrity'. Questions: urgent",
			noRequest: true,
		},
		{
			name:      "should name the file's key when its abstain expression cannot parse",
			file:      "assert: urgent.value > 0.5\nabstain_if: urgent.value >\n" + question,
			wantCode:  cli.ExitUsage,
			wantErr:   "onesie: 'abstain_if': parse error at column 15",
			noRequest: true,
		},
		{
			name:      "should reject a file's abstain expression with no assertion anywhere",
			file:      "abstain_if: urgent.value > 0.5\n" + question,
			wantCode:  cli.ExitUsage,
			wantErr:   "onesie: 'abstain_if' needs --assert, since without one every record is a yes",
			noRequest: true,
		},
		{
			name:     "should let the file's assertion answer the quiet rule on a pick",
			file:     "assert: team.p.billing > 0.5\nteam:\n  ask: which team\n  pick: [billing, technical]\n",
			args:     []string{"-q"},
			response: picked,
			wantCode: cli.ExitOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			response := tc.response
			if response == "" {
				response = answered
			}

			out, errOut, requests := runAsserted(t,
				append([]string{"-f", "q.yaml"}, tc.args...), 0, response, tc.wantCode,
				cli.WithReadFile(func(string) ([]byte, error) { return []byte(tc.file), nil }))

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

	t.Run("should put the file's assertion ahead of the command line's", func(t *testing.T) {
		t.Parallel()

		const question = "urgent: is this urgent\n"

		tests := []struct {
			name    string
			file    string
			args    []string
			wantOut string
		}{
			{
				name:    "should write the file's assertion back unchanged",
				file:    "assert: urgent.value > 0.5\n" + question,
				args:    []string{"-f", "q.yaml"},
				wantOut: "assert: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n",
			},
			{
				name: "should write the file's assertion ahead of the command line's",
				file: "assert: urgent.value > 0.5\n" + question,
				args: []string{"-f", "q.yaml", "--assert", "urgent.value < 0.95"},
				wantOut: "assert: (urgent.value > 0.5) and (urgent.value < 0.95)\n" +
					"urgent:\n  ask: is this urgent\n",
			},
			{
				name:    "should write an assertion given only on the command line",
				args:    []string{"--ask", "urgent=is this urgent", "--assert", "urgent.value > 0.5"},
				wantOut: "assert: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n",
			},
			{
				name: "should combine a repeated --abstain-if and the file key with and",
				file: "assert: urgent.value < 0.2\nabstain_if: urgent.value < 0.8\n" + question,
				args: []string{
					"-f", "q.yaml", "--abstain-if", "urgent.value > 0.1",
					"--abstain-if", "urgent.value != 0.5",
				},
				wantOut: "assert: urgent.value < 0.2\n" +
					"abstain_if: (urgent.value < 0.8) and (urgent.value > 0.1) and (urgent.value != 0.5)\n" +
					"urgent:\n  ask: is this urgent\n",
			},
			{
				name:    "should write no assert key when nothing asserted",
				args:    []string{"--ask", "urgent=is this urgent"},
				wantOut: "urgent:\n  ask: is this urgent\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, requests := runAsserted(t,
					append([]string{"--print-questions"}, tc.args...), 0, "", cli.ExitOK,
					cli.WithReadFile(func(string) ([]byte, error) { return []byte(tc.file), nil }))

				if out != tc.wantOut {
					t.Errorf("stdout =\n%s\nwant\n%s", out, tc.wantOut)
				}

				if errOut != "" {
					t.Errorf("stderr = %q, want nothing", errOut)
				}

				if requests != 0 {
					t.Errorf("requests = %d, want none", requests)
				}
			})
		}
	})
}

func runAsserted(
	t *testing.T,
	args []string,
	status int,
	response string,
	wantCode int,
	extra ...cli.RootOption,
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

	options := []cli.RootOption{
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
		cli.WithKeychain(offKeychain{}),
	}

	root := cli.NewRootCmd(cli.BuildInfo{Version: "1.2.3"}, append(options, extra...)...)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	if code := cli.Execute(t.Context(), root); code != wantCode {
		t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			code, wantCode, out.String(), errOut.String())
	}

	return out.String(), errOut.String(), requests.Load()
}

func runAssertedStream(t *testing.T, args []string, stdin string, wantCode int) (string, string) {
	t.Helper()

	// Keyed on the state the body carries rather than on a call counter, so a case reads the same
	// whatever order the records complete in.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("reading request body: %v", readErr)
		}

		reply := `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.1}}}`

		switch {
		case bytes.Contains(body, []byte("boom")):
			w.WriteHeader(http.StatusInternalServerError)

			reply = `{"error":{"message":"boom"}}`
		case bytes.Contains(body, []byte("hot")):
			reply = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`
		}

		if _, writeErr := w.Write([]byte(reply)); writeErr != nil {
			t.Errorf("writing stub response: %v", writeErr)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer

	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "1.2.3"},
		cli.WithKeychain(offKeychain{}),
		cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
			policy := jev.DefaultRetryPolicy()
			policy.MaxRetries = 0

			return jev.New(append([]jev.Option{
				jev.WithAPIKey("k"),
				jev.WithBaseURL(srv.URL),
				jev.WithRetry(policy),
			}, opts...)...)
		}),
		cli.WithStdin(strings.NewReader(stdin)),
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

	return out.String(), errOut.String()
}

func TestStopping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{
			name: "should count no failed record for a request the stop cancelled",
			want: "1 request, 1 false assertion, 1 question",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			errOut := runStoppedStream(t)

			if !strings.Contains(errOut, tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.want)
			}

			if strings.Contains(errOut, "failed") {
				t.Errorf("stderr = %q, want no failed record", errOut)
			}
		})
	}
}

func runStoppedStream(t *testing.T) string {
	t.Helper()

	// The second record blocks until its own request context is cancelled, and the first answers
	// only once the second is in flight. So the stop always has a request to cancel, which is the
	// record that used to be counted as a failure.
	inFlight := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("reading request body: %v", readErr)

			return
		}

		if bytes.Contains(body, []byte("wait")) {
			close(inFlight)
			<-r.Context().Done()

			return
		}

		<-inFlight

		if _, writeErr := w.Write([]byte(urgent)); writeErr != nil {
			t.Errorf("writing stub response: %v", writeErr)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer

	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "1.2.3"},
		cli.WithKeychain(offKeychain{}),
		cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
			policy := jev.DefaultRetryPolicy()
			policy.MaxRetries = 0

			return jev.New(append([]jev.Option{
				jev.WithAPIKey("k"),
				jev.WithBaseURL(srv.URL),
				jev.WithRetry(policy),
			}, opts...)...)
		}),
		cli.WithStdin(strings.NewReader("hot\nwait\n")),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{
		"is this urgent", "-i", "lines", "-j", "2",
		"--assert", "answer.value < 0.5", "--stop-on-assert", "--stats",
	})

	if code := cli.Execute(t.Context(), root); code != cli.ExitRejected {
		t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			code, cli.ExitRejected, out.String(), errOut.String())
	}

	return errOut.String()
}
