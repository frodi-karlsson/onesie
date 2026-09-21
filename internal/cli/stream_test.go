package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func TestStreaming(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`

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
				cli.WithClientFactory(func(context.Context) (*jev.Client, error) {
					// No retries. The 500 cases would otherwise spend the client's backoff twice
					// per record for no coverage, and --retries is not a flag until a later
					// milestone.
					policy := jev.DefaultRetryPolicy()
					policy.MaxRetries = 0

					return jev.New(
						jev.WithAPIKey("k"),
						jev.WithBaseURL(srv.URL),
						jev.WithRetry(policy),
					)
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
}

func TestMergeAutoOutput(t *testing.T) {
	t.Parallel()

	t.Run("should resolve auto to json under merge on a terminal", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if _, err := w.Write([]byte(
				`{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`,
			)); err != nil {
				t.Errorf("writing stub response: %v", err)
			}
		}))
		defer srv.Close()

		var out bytes.Buffer

		root := cli.NewRootCmd(
			cli.BuildInfo{Version: "1.2.3"},
			cli.WithClientFactory(func(context.Context) (*jev.Client, error) {
				return jev.New(jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL))
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
}

func TestSingleRecordMerge(t *testing.T) {
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
					`{"model":"jev-1.13.0","answers":{"answer":{"type":"noul","noul":0.9}}}`,
				)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			var out, errOut bytes.Buffer

			root := cli.NewRootCmd(
				cli.BuildInfo{Version: "1.2.3"},
				cli.WithClientFactory(func(context.Context) (*jev.Client, error) {
					return jev.New(jev.WithAPIKey("k"), jev.WithBaseURL(srv.URL))
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
}
