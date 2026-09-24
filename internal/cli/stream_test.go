package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func TestStream(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`

	tests := []struct {
		name      string
		args      []string
		stdin     string
		status    int
		response  string
		wantCode  int
		wantLines int
		contains  []string
	}{
		{
			name:      "should write one line per input line",
			args:      []string{"is this urgent", "-i", "lines"},
			stdin:     "first\nsecond\nthird\n",
			response:  answered,
			wantCode:  cli.ExitOK,
			wantLines: 3,
			contains:  []string{`"answer":{"value":0.9}`},
		},
		{
			name:      "should write nothing for an empty stream",
			args:      []string{"is this urgent", "-i", "lines"},
			stdin:     "",
			response:  answered,
			wantCode:  cli.ExitOK,
			wantLines: 0,
		},
		{
			name:      "should emit an input error record and continue",
			args:      []string{"is this urgent", "-i", "jsonl"},
			stdin:     "\"good\"\nnot json\n\"also good\"\n",
			response:  answered,
			wantCode:  cli.ExitRecords,
			wantLines: 3,
			contains:  []string{`"kind":"input"`},
		},
		{
			name:      "should carry a fallback on an input error record",
			args:      []string{"is this safe", "--pick", "safe,refuse", "--min-confidence", "0.7", "--fallback", "refuse", "-i", "jsonl", "-o", "json"},
			stdin:     "not json\n",
			response:  answered,
			wantCode:  cli.ExitRecords,
			wantLines: 1,
			contains:  []string{`"kind":"input"`, `"decision":"refuse"`},
		},
		{
			name:      "should count a failed record toward exit six and continue",
			args:      []string{"is this urgent", "-i", "lines"},
			stdin:     "first\nsecond\n",
			status:    http.StatusInternalServerError,
			response:  `{"error":{"message":"boom"}}`,
			wantCode:  cli.ExitRecords,
			wantLines: 2,
			contains:  []string{`"kind":"http"`, `"status":500`},
		},
		{
			name:  "should report an answer shape mismatch as a response failure",
			args:  []string{"is this urgent", "-i", "lines"},
			stdin: "first\n",
			response: `{"model":"onesie-1.13.0","answers":{"answer":{"type":"choice",` +
				`"choice":"a","confidence":0.5,"probabilities":{"a":1}}}}`,
			wantCode:  cli.ExitRecords,
			wantLines: 1,
			contains:  []string{`"kind":"response"`, `"status":200`},
		},
		{
			name:      "should report a missing answer as a response failure",
			args:      []string{"is this urgent", "-i", "lines"},
			stdin:     "first\n",
			response:  `{"model":"onesie-1.13.0","answers":{"other":{"type":"noul","noul":0.5}}}`,
			wantCode:  cli.ExitRecords,
			wantLines: 1,
			contains:  []string{`"kind":"response"`, `"status":200`},
		},
		{
			name:      "should abort the whole stream on an authentication failure",
			args:      []string{"is this urgent", "-i", "lines", "-j", "1"},
			stdin:     "a\nb\nc\nd\ne\n",
			status:    http.StatusUnauthorized,
			response:  `{"error":{"message":"bad key"}}`,
			wantCode:  cli.ExitAuth,
			wantLines: 1,
		},
		{
			name:      "should merge answers into a json object",
			args:      []string{"is this urgent", "-i", "jsonl", "--merge"},
			stdin:     "{\"id\":7}\n",
			response:  answered,
			wantCode:  cli.ExitOK,
			wantLines: 1,
			contains:  []string{`"id":7`, `"answers":{`},
		},
		{
			name:      "should wrap a text line under state when merging",
			args:      []string{"is this urgent", "-i", "lines", "--merge"},
			stdin:     "a ticket\n",
			response:  answered,
			wantCode:  cli.ExitOK,
			wantLines: 1,
			contains:  []string{`"state":"a ticket"`},
		},
		{
			name:      "should keep merging a batch when one input already has the key",
			args:      []string{"is this urgent", "-i", "jsonl", "--merge"},
			stdin:     "{\"id\":1}\n{\"answers\":\"mine\"}\n{\"id\":3}\n",
			response:  answered,
			wantCode:  cli.ExitRecords,
			wantLines: 3,
			contains:  []string{`"id":1`, `--merge would overwrite`, `"id":3`},
		},
		{
			name:      "should drop blank lines under skip blank",
			args:      []string{"is this urgent", "-i", "lines", "--skip-blank"},
			stdin:     "first\n\nsecond\n",
			response:  answered,
			wantCode:  cli.ExitOK,
			wantLines: 2,
		},
		{
			name:      "should fail a record that maps to nothing usable and carry on",
			args:      []string{"is this urgent", "-i", "jsonl", "--map", ".body"},
			stdin:     "{\"body\":\"first\"}\n{\"customer\":\"c2\"}\n{\"body\":1}\n{\"body\":\"fourth\"}\n",
			response:  answered,
			wantCode:  cli.ExitRecords,
			wantLines: 4,
			contains: []string{
				`line 2: --map: state must be a string, object or array, got null`,
				`line 3: --map: state must be a string, object or array, got number`,
				`"answer":{"value":0.9}`,
			},
		},
		{
			name:      "should fail a record whose expression fails as it runs and carry on",
			args:      []string{"is this urgent", "-i", "jsonl", "--map", ".body.text"},
			stdin:     "{\"body\":\"first\"}\n{\"body\":{\"text\":\"second\"}}\n",
			response:  answered,
			wantCode:  cli.ExitRecords,
			wantLines: 2,
			contains:  []string{`"kind":"input"`, `line 1: --map fails: `, `"answer":{"value":0.9}`},
		},
		{
			name: "should fail a record whose --map result nests too deep and carry on",
			args: []string{
				"is this urgent", "-i", "jsonl",
				"--map", `if .deep then reduce range(10000) as $i ("x"; [.]) else .body end`,
			},
			stdin:     "{\"deep\":true}\n{\"body\":\"second\"}\n",
			response:  answered,
			wantCode:  cli.ExitRecords,
			wantLines: 2,
			contains: []string{
				`"kind":"input"`, `line 1: --map: result nests deeper than 9999 levels`,
				`"answer":{"value":0.9}`,
			},
		},
		{
			name: "should fail a record whose --map result is too large to send and carry on",
			args: []string{
				"is this urgent", "-i", "jsonl", "--map", `if .big then "x" * 9000000 else .body end`,
			},
			stdin:     "{\"big\":true}\n{\"body\":\"second\"}\n",
			response:  answered,
			wantCode:  cli.ExitRecords,
			wantLines: 2,
			contains: []string{
				`"kind":"input"`, `line 1: --map: result encodes to more than the limit of 8388608 bytes`,
				`"answer":{"value":0.9}`,
			},
		},
		{
			name:      "should emit every record under unordered",
			args:      []string{"is this urgent", "-i", "lines", "--unordered", "-j", "4"},
			stdin:     "a\nb\nc\nd\n",
			response:  answered,
			wantCode:  cli.ExitOK,
			wantLines: 4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}

				if _, err := w.Write([]byte(tc.response)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			var out, errOut bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3"},
				cli.WithKeychain(offKeychain{}),
				cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
					// No retries. The 500 cases would otherwise spend the client's backoff twice
					// per record for no coverage.
					policy := jev.DefaultRetryPolicy()
					policy.MaxRetries = 0

					return jev.New(append([]jev.Option{
						jev.WithAPIKey("k"),
						jev.WithBaseURL(srv.URL),
						jev.WithRetry(policy),
					}, opts...)...)
				}),
				cli.WithStdin(strings.NewReader(tc.stdin)),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(tc.args)

			code := cli.Execute(t.Context(), root)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out.String(), errOut.String())
			}

			// The exit code already says a record failed and the per record lines carry the
			// detail, so a failing stream reports nothing on stderr.
			if tc.wantCode == cli.ExitRecords && errOut.String() != "" {
				t.Errorf("stderr = %q, want nothing", errOut.String())
			}

			lines := 0
			for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
				if line != "" {
					lines++
				}
			}

			if lines != tc.wantLines {
				t.Errorf("lines = %d, want %d\ngot:\n%s", lines, tc.wantLines, out.String())
			}

			for _, want := range tc.contains {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q\ngot:\n%s", want, out.String())
				}
			}

			for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
				if line == "" {
					continue
				}

				var probe any
				if err := json.Unmarshal([]byte(line), &probe); err != nil {
					t.Errorf("line is not valid json: %q", line)
				}
			}
		})
	}

	t.Run("should gate every record on its assertion", func(t *testing.T) {
		t.Parallel()

		const gate = "answer.value < 0.5"

		tests := []struct {
			name      string
			args      []string
			stdin     string
			wantCode  int
			wantLines int
			wantFalse int
			contains  []string
			missing   []string
			wantErr   string
		}{
			{
				name:      "should print every line and exit one when one assertion is false",
				args:      []string{"is this urgent", "-i", "lines", "--assert", gate},
				stdin:     "cold\nhot\ncold\n",
				wantCode:  cli.ExitRejected,
				wantLines: 3,
				wantFalse: 1,
				contains:  []string{`"answer":{"value":0.9}`, `"answer":{"value":0.1}`},
			},
			{
				name:      "should exit zero when every record's assertion holds",
				args:      []string{"is this urgent", "-i", "lines", "--assert", gate},
				stdin:     "cold\ncold\n",
				wantCode:  cli.ExitOK,
				wantLines: 2,
				missing:   []string{"assert"},
			},
			{
				name: "should count no failed record when an assertion is false",
				args: []string{
					"is this urgent", "-i", "lines", "--assert", gate, "--stats",
				},
				stdin:     "cold\nhot\ncold\n",
				wantCode:  cli.ExitRejected,
				wantLines: 3,
				wantFalse: 1,
				wantErr:   "3 requests, 1 false assertion, 3 questions",
			},
			{
				name:      "should exit six when a failed record precedes a false assertion",
				args:      []string{"is this urgent", "-i", "lines", "--assert", gate},
				stdin:     "boom\nhot\n",
				wantCode:  cli.ExitRecords,
				wantLines: 2,
				wantFalse: 1,
				contains:  []string{`"kind":"http"`},
			},
			{
				name:      "should exit six when a false assertion precedes a failed record",
				args:      []string{"is this urgent", "-i", "lines", "--assert", gate},
				stdin:     "hot\nboom\n",
				wantCode:  cli.ExitRecords,
				wantLines: 2,
				wantFalse: 1,
				contains:  []string{`"kind":"http"`},
			},
			{
				name: "should not evaluate the assertion on a failed record",
				args: []string{
					"is this urgent", "-i", "lines", "--assert", "answer.value > 0.5",
				},
				stdin:     "boom\n",
				wantCode:  cli.ExitRecords,
				wantLines: 1,
				contains:  []string{`"kind":"http"`},
				missing:   []string{"assert"},
			},
			{
				name: "should end the run at the first false assertion under stop on assert",
				args: []string{
					"is this urgent", "-i", "lines", "--assert", gate, "--stop-on-assert",
				},
				stdin:     "cold\nhot\ncold\n",
				wantCode:  cli.ExitRejected,
				wantLines: 2,
				wantFalse: 1,
			},
			{
				name: "should end an unordered run at the first false assertion",
				args: []string{
					"is this urgent", "-i", "lines", "--assert", gate,
					"--stop-on-assert", "--unordered",
				},
				stdin:     "cold\nhot\ncold\n",
				wantCode:  cli.ExitRejected,
				wantLines: 2,
				wantFalse: 1,
			},
			{
				name: "should exit seven when an abstain is the worst the stream holds",
				args: []string{
					"is this urgent", "-i", "lines", "--assert", gate,
					"--abstain-if", "answer.value < 0.95", "--stats",
				},
				stdin:     "cold\nhot\ncold\n",
				wantCode:  cli.ExitAbstain,
				wantLines: 3,
				contains:  []string{`{"abstain":true,"model":"onesie-1.13.0","answer":{"value":0.9}}`},
				wantErr:   "3 requests, 1 abstain, 3 questions",
			},
			{
				name: "should exit six when a failed record sits beside an abstain",
				args: []string{
					"is this urgent", "-i", "lines", "--assert", gate,
					"--abstain-if", "answer.value < 0.95",
				},
				stdin:     "hot\nboom\n",
				wantCode:  cli.ExitRecords,
				wantLines: 2,
				contains:  []string{`"abstain":true`, `"kind":"http"`},
			},
			{
				name: "should not stop on an abstain under stop on assert",
				args: []string{
					"is this urgent", "-i", "lines", "--assert", gate,
					"--abstain-if", "answer.value < 0.95", "--stop-on-assert",
				},
				stdin:     "cold\nhot\ncold\n",
				wantCode:  cli.ExitAbstain,
				wantLines: 3,
				contains:  []string{`"abstain":true`},
			},
			{
				name: "should write abstain in the csv assert column",
				args: []string{
					"is this urgent", "-i", "lines", "-o", "csv", "--assert", gate,
					"--abstain-if", "answer.value < 0.95",
				},
				stdin:     "cold\nhot\n",
				wantCode:  cli.ExitAbstain,
				wantLines: 3,
				contains:  []string{"answer,assert,error\n0.1,true,\n0.9,abstain,\n"},
			},
			{
				name: "should read the whole stream under stop on assert when nothing is false",
				args: []string{
					"is this urgent", "-i", "lines", "--assert", gate, "--stop-on-assert",
				},
				stdin:     "cold\ncold\ncold\n",
				wantCode:  cli.ExitOK,
				wantLines: 3,
				missing:   []string{"assert"},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut := runAssertedStream(t, tc.args, tc.stdin, tc.wantCode)

				lines := 0

				for _, line := range strings.Split(out, "\n") {
					if line != "" {
						lines++
					}
				}

				if lines != tc.wantLines {
					t.Errorf("lines = %d, want %d\ngot:\n%s", lines, tc.wantLines, out)
				}

				if got := strings.Count(out, `"assert":false`); got != tc.wantFalse {
					t.Errorf("false assertions = %d, want %d\ngot:\n%s", got, tc.wantFalse, out)
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

				if tc.wantErr != "" && strings.Contains(errOut, "failed") {
					t.Errorf("stderr = %q, want no failed record", errOut)
				}
			})
		}
	})

	t.Run("should send the state the request body spells", func(t *testing.T) {
		t.Parallel()

		const answered = `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`

		const record = `{"ticket_id":12345678901234567890,"zebra":1,"alpha":2}`

		tests := []struct {
			name  string
			args  []string
			stdin string
			// wantWire is the state as the request body spelled it.
			wantWire string
			wantOut  string
		}{
			{
				name:     "should send a nineteen digit integer unchanged under jsonl",
				args:     []string{"x", "-i", "jsonl", "-o", "values"},
				stdin:    record + "\n",
				wantWire: record,
				wantOut:  `{"answer":0.9}`,
			},
			{
				name:     "should send a nineteen digit integer unchanged under json",
				args:     []string{"x", "-i", "json", "-o", "values"},
				stdin:    record,
				wantWire: record,
				wantOut:  `{"answer":0.9}`,
			},
			{
				name:     "should keep an array's large integer through merge under jsonl",
				args:     []string{"x", "-i", "jsonl", "--merge", "-o", "values"},
				stdin:    "[12345678901234567890]\n",
				wantWire: `[12345678901234567890]`,
				wantOut:  `{"state":[12345678901234567890],"answers":{"answer":0.9}}`,
			},
			{
				name:     "should keep an array's large integer through merge under json",
				args:     []string{"x", "-i", "json", "--merge", "-o", "values"},
				stdin:    "[12345678901234567890]",
				wantWire: `[12345678901234567890]`,
				wantOut:  `{"state":[12345678901234567890],"answers":{"answer":0.9}}`,
			},
			{
				name:     "should keep an object's key order through merge under jsonl",
				args:     []string{"x", "-i", "jsonl", "--merge", "-o", "values"},
				stdin:    record + "\n",
				wantWire: record,
				wantOut: `{"ticket_id":12345678901234567890,"zebra":1,"alpha":2,` +
					`"answers":{"answer":0.9}}`,
			},
			{
				name:     "should keep an object's key order through merge under json",
				args:     []string{"x", "-i", "json", "--merge", "-o", "values"},
				stdin:    record,
				wantWire: record,
				wantOut: `{"ticket_id":12345678901234567890,"zebra":1,"alpha":2,` +
					`"answers":{"answer":0.9}}`,
			},
			{
				name:     "should send the mapped state and merge into the whole jsonl record",
				args:     []string{"x", "-i", "jsonl", "--map", ".body", "--merge", "-o", "values"},
				stdin:    `{"id":7,"body":"the site is down"}` + "\n",
				wantWire: `"the site is down"`,
				wantOut:  `{"id":7,"body":"the site is down","answers":{"answer":0.9}}`,
			},
			{
				name:     "should send the mapped state and merge into the whole json record",
				args:     []string{"x", "-i", "json", "--map", "{ticket_id}", "--merge", "-o", "values"},
				stdin:    record,
				wantWire: `{"ticket_id":12345678901234567890}`,
				wantOut: `{"ticket_id":12345678901234567890,"zebra":1,"alpha":2,` +
					`"answers":{"answer":0.9}}`,
			},
			{
				name:     "should send the mapped column and merge into the whole csv row",
				args:     []string{"x", "-i", "csv", "--map", ".body", "--merge", "-o", "values"},
				stdin:    "id,body\n7,the site is down\n",
				wantWire: `"the site is down"`,
				wantOut:  `{"id":"7","body":"the site is down","answers":{"answer":0.9}}`,
			},
			{
				name:     "should send the mapped column and merge into the whole tsv row",
				args:     []string{"x", "-i", "tsv", "--map", ".body", "--merge", "-o", "values"},
				stdin:    "id\tbody\n7\tthe site is down\n",
				wantWire: `"the site is down"`,
				wantOut:  `{"id":"7","body":"the site is down","answers":{"answer":0.9}}`,
			},
			{
				name:     "should send the mapped line and merge into the line as read",
				args:     []string{"x", "-i", "lines", "--map", "ascii_upcase", "--merge", "-o", "values"},
				stdin:    "the site is down\n",
				wantWire: `"THE SITE IS DOWN"`,
				wantOut:  `{"state":"the site is down","answers":{"answer":0.9}}`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var (
					mu   sync.Mutex
					body string
				)

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sent, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("reading request body: %v", err)
					}

					mu.Lock()
					body = string(sent)
					mu.Unlock()

					if _, err := w.Write([]byte(answered)); err != nil {
						t.Errorf("writing stub response: %v", err)
					}
				}))
				defer srv.Close()

				var out, errOut bytes.Buffer

				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain{}),
					cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
						return jev.New(append([]jev.Option{
							jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL),
						}, opts...)...)
					}),
					cli.WithStdin(strings.NewReader(tc.stdin)),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
				)

				root.SetOut(&out)
				root.SetErr(&errOut)
				root.SetArgs(tc.args)

				if code := cli.Execute(t.Context(), root); code != cli.ExitOK {
					t.Fatalf("exit code = %d\nstdout:\n%s\nstderr:\n%s",
						code, out.String(), errOut.String())
				}

				mu.Lock()
				defer mu.Unlock()

				if !strings.Contains(body, `"state":`+tc.wantWire) {
					t.Errorf("request body = %s\nwant state %s", body, tc.wantWire)
				}

				if got := strings.TrimSuffix(out.String(), "\n"); got != tc.wantOut {
					t.Errorf("output = %s\nwant    %s", got, tc.wantOut)
				}
			})
		}
	})

	t.Run("should exit two on a --map syntax error before reading input", func(t *testing.T) {
		t.Parallel()

		var requests atomic.Int32

		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			requests.Add(1)
		}))
		defer srv.Close()

		stdin := &watchedReader{}

		var out, errOut bytes.Buffer

		root := cli.NewRootCmd(
			cli.BuildInfo{Version: "1.2.3"},
			cli.WithKeychain(offKeychain{}),
			cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
				return jev.New(append([]jev.Option{
					jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL),
				}, opts...)...)
			}),
			cli.WithStdin(stdin),
			cli.WithStdinTTY(false),
			cli.WithStdoutTTY(false),
			cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
		)

		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{"is this urgent", "-i", "jsonl", "--map", ".body |"})

		if code := cli.Execute(t.Context(), root); code != cli.ExitUsage {
			t.Errorf("exit code = %d, want %d", code, cli.ExitUsage)
		}

		if want := "onesie: --map: unexpected EOF at column 8\n"; errOut.String() != want {
			t.Errorf("stderr = %q, want %q", errOut.String(), want)
		}

		if stdin.read.Load() {
			t.Error("stdin was read")
		}

		if got := requests.Load(); got != 0 {
			t.Errorf("requests = %d, want 0", got)
		}

		if out.String() != "" {
			t.Errorf("stdout = %q, want nothing", out.String())
		}
	})

	t.Run("should report the failed records alongside a read failure", func(t *testing.T) {
		t.Parallel()

		t.Run("should report the failed records alongside the read failure", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)

				if _, err := w.Write([]byte(`{"error":{"message":"boom"}}`)); err != nil {
					t.Errorf("writing stub response: %v", err)
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
						jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL), jev.WithRetry(policy),
					}, opts...)...)
				}),
				cli.WithStdin(&breakingReader{lines: "first\n"}),
				cli.WithStdinTTY(false),
				cli.WithStdoutTTY(false),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs([]string{"x", "-i", "lines", "-j", "1"})

			// The read failure ends the stream, which a caller cannot tell from a complete one, so it
			// takes the exit code. The record that had already failed is named rather than dropped.
			if code := cli.Execute(t.Context(), root); code != cli.ExitUsage {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, cli.ExitUsage, errOut.String())
			}

			if !strings.Contains(errOut.String(), "1 record failed") {
				t.Errorf("stderr = %q, want it to name the failed record", errOut.String())
			}

			if !strings.Contains(errOut.String(), "stdin: ") {
				t.Errorf("stderr = %q, want it to name stdin", errOut.String())
			}
		})
	})

	t.Run("should exit 130 on an interrupt", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name          string
			args          []string
			sourceResumes bool
		}{
			{
				name:          "should exit 130 when input arrives after the interrupt",
				args:          []string{"is this urgent", "-i", "lines"},
				sourceResumes: true,
			},
			{
				name: "should exit 130 promptly while stdin is idle",
				args: []string{"is this urgent", "-i", "lines"},
			},
			{
				name:          "should exit 130 unordered when input arrives after the interrupt",
				args:          []string{"is this urgent", "-i", "lines", "--unordered"},
				sourceResumes: true,
			},
			{
				name: "should exit 130 promptly unordered while stdin is idle",
				args: []string{"is this urgent", "-i", "lines", "--unordered"},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if _, err := w.Write([]byte(
						`{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`,
					)); err != nil {
						t.Errorf("writing stub response: %v", err)
					}
				}))
				defer srv.Close()

				stdin, feed := io.Pipe()
				t.Cleanup(func() { feed.Close() })

				out := &lineCounter{lines: make(chan struct{}, 8)}

				var errOut bytes.Buffer

				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain{}),
					cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
						return jev.New(append([]jev.Option{
							jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL),
						}, opts...)...)
					}),
					cli.WithStdin(stdin),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
				)

				root.SetOut(out)
				root.SetErr(&errOut)
				root.SetArgs(tc.args)

				ctx, interrupt := context.WithCancel(t.Context())
				defer interrupt()

				exited := make(chan int, 1)

				go func() { exited <- cli.Execute(ctx, root) }()

				if _, err := io.WriteString(feed, "one\ntwo\n"); err != nil {
					t.Fatalf("feeding stdin: %v", err)
				}

				for range 2 {
					select {
					case <-out.lines:
					case <-time.After(5 * time.Second):
						t.Fatal("the records fed before the interrupt were never written")
					}
				}

				interrupt()

				if tc.sourceResumes {
					go io.WriteString(feed, "three\n")
				}

				select {
				case code := <-exited:
					if code != cli.ExitInterrupt {
						t.Errorf("exit code = %d, want %d\nstderr:\n%s",
							code, cli.ExitInterrupt, errOut.String())
					}
				case <-time.After(5 * time.Second):
					t.Fatal("the stream ignored the interrupt while waiting on stdin")
				}
			})
		}
	})

	t.Run("should exit 130 when an interrupt lands inside --map", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			args  []string
			stdin string
		}{
			{
				name:  "should exit 130 on one json record",
				args:  []string{"is this urgent", "-i", "json", "--map", "until(false; .)"},
				stdin: `{"body":"the site is down"}`,
			},
			{
				name:  "should exit 130 on one json record under --print-request",
				args:  []string{"is this urgent", "-i", "json", "--map", "until(false; .)", "--print-request"},
				stdin: `{"body":"the site is down"}`,
			},
			{
				name:  "should exit 130 on a jsonl stream",
				args:  []string{"is this urgent", "-i", "jsonl", "--map", "until(false; .)"},
				stdin: "{\"body\":\"first\"}\n{\"body\":\"second\"}\n",
			},
			{
				name:  "should exit 130 on a jsonl stream under --print-request",
				args:  []string{"is this urgent", "-i", "jsonl", "--map", "until(false; .)", "--print-request"},
				stdin: "{\"body\":\"first\"}\n{\"body\":\"second\"}\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var requests atomic.Int32

				srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					requests.Add(1)
				}))
				defer srv.Close()

				var out, errOut lockedBuffer

				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain{}),
					cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
						return jev.New(append([]jev.Option{
							jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL),
						}, opts...)...)
					}),
					cli.WithStdin(strings.NewReader(tc.stdin)),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
				)

				root.SetOut(&out)
				root.SetErr(&errOut)
				root.SetArgs(tc.args)

				ctx, interrupt := context.WithCancel(t.Context())
				defer interrupt()

				exited := make(chan int, 1)

				go func() { exited <- cli.Execute(ctx, root) }()

				time.AfterFunc(50*time.Millisecond, interrupt)

				select {
				case code := <-exited:
					if code != cli.ExitInterrupt {
						t.Errorf("exit code = %d, want %d\nstderr:\n%s",
							code, cli.ExitInterrupt, errOut.String())
					}
				case <-time.After(5 * time.Second):
					t.Fatal("--map ignored the interrupt")
				}

				if errOut.String() != "" {
					t.Errorf("stderr = %q, want nothing", errOut.String())
				}

				if out.String() != "" {
					t.Errorf("stdout = %q, want nothing", out.String())
				}

				if got := requests.Load(); got != 0 {
					t.Errorf("requests = %d, want 0", got)
				}
			})
		}
	})
}

func TestWriteMerged(t *testing.T) {
	t.Parallel()

	t.Run("should resolve auto to json under a merge", func(t *testing.T) {
		t.Parallel()

		t.Run("should resolve auto to json under merge on a terminal", func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if _, err := w.Write([]byte(
					`{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`,
				)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			var out bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3"},
				cli.WithKeychain(offKeychain{}),
				cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
					return jev.New(append([]jev.Option{
						jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL),
					}, opts...)...)
				}),
				cli.WithStdin(strings.NewReader(`{"id":7}`)),
				cli.WithStdinTTY(false),
				// A terminal in a non streaming mode would normally choose the table, which --merge
				// forbids. Section 7 says json wins.
				cli.WithStdoutTTY(true),
				cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
			)

			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{"is this urgent", "-i", "json", "--merge"})

			if code := cli.Execute(t.Context(), root); code != cli.ExitOK {
				t.Fatalf("exit code = %d, output:\n%s", code, out.String())
			}

			if !strings.Contains(out.String(), `"answers":{`) {
				t.Errorf("want merged json, got:\n%s", out.String())
			}

			if !strings.Contains(out.String(), `"id":7`) {
				t.Errorf("want the input's fields kept, got:\n%s", out.String())
			}
		})
	})

	t.Run("should merge a single record", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			args     []string
			stdin    string
			wantCode int
			contains string
			requests int
		}{
			{
				name:     "should wrap a text state under state",
				args:     []string{"is this urgent", "--merge"},
				stdin:    "a ticket",
				wantCode: cli.ExitOK,
				contains: `{"state":"a ticket","answers":{`,
				requests: 1,
			},
			{
				name:     "should fold the answers into an object state",
				args:     []string{"is this urgent", "-i", "json", "--merge-key", "out"},
				stdin:    `{"id":7}`,
				wantCode: cli.ExitOK,
				contains: `{"id":7,"out":{`,
				requests: 1,
			},
			{
				name:     "should reject a taken merge key before any request",
				args:     []string{"is this urgent", "-i", "json", "--merge"},
				stdin:    `{"answers":1}`,
				wantCode: cli.ExitUsage,
				contains: "",
				requests: 0,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var calls atomic.Int64

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					calls.Add(1)

					if _, err := w.Write([]byte(
						`{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`,
					)); err != nil {
						t.Errorf("writing stub response: %v", err)
					}
				}))
				defer srv.Close()

				var out, errOut bytes.Buffer

				root := cli.NewRootCmd(
					cli.BuildInfo{Version: "1.2.3"},
					cli.WithKeychain(offKeychain{}),
					cli.WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
						return jev.New(append([]jev.Option{
							jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL),
						}, opts...)...)
					}),
					cli.WithStdin(strings.NewReader(tc.stdin)),
					cli.WithStdinTTY(false),
					cli.WithStdoutTTY(false),
					cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
				)

				root.SetOut(&out)
				root.SetErr(&errOut)
				root.SetArgs(tc.args)

				if code := cli.Execute(t.Context(), root); code != tc.wantCode {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, tc.wantCode, out.String(), errOut.String())
				}

				if got := int(calls.Load()); got != tc.requests {
					t.Errorf("requests = %d, want %d", got, tc.requests)
				}

				if tc.contains != "" && !strings.Contains(out.String(), tc.contains) {
					t.Errorf("output missing %q\ngot:\n%s", tc.contains, out.String())
				}
			})
		}
	})
}

type breakingReader struct {
	lines string
}

func (r *breakingReader) Read(p []byte) (int, error) {
	if r.lines == "" {
		return 0, errors.New("disk fell over")
	}

	n := copy(p, r.lines)
	r.lines = r.lines[n:]

	return n, nil
}

type lineCounter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	lines chan struct{}
}

func (c *lineCounter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for range bytes.Count(p, []byte("\n")) {
		c.lines <- struct{}{}
	}

	return c.buf.Write(p)
}

type watchedReader struct {
	read atomic.Bool
}

func (r *watchedReader) Read([]byte) (int, error) {
	r.read.Store(true)

	return 0, io.EOF
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
