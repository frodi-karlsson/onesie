package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenOut(t *testing.T) {
	t.Parallel()

	const input = "{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n{\"id\":4}\n"

	tests := []struct {
		name      string
		existing  string
		noFile    bool
		args      []string
		stdin     string
		wantCode  int
		wantFile  string
		wantCalls int32
		unreached bool
	}{
		{
			name:      "should write the answers to the file and nothing to stdout",
			noFile:    true,
			args:      []string{"is this urgent", "-i", "jsonl"},
			stdin:     input,
			wantFile:  strings.Repeat("{\"answer\":0.5}\n", 4),
			wantCalls: 4,
		},
		{
			name:      "should skip the records the file already answers",
			existing:  "old one\nold two\n",
			args:      []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:     input,
			wantFile:  "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls: 2,
		},
		{
			name:      "should drop a line the earlier run was cut off in",
			existing:  "old one\nold two\nold thr",
			args:      []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:     input,
			wantFile:  "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls: 2,
		},
		{
			name:      "should start fresh when the file does not exist yet",
			noFile:    true,
			args:      []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:     input,
			wantFile:  strings.Repeat("{\"answer\":0.5}\n", 4),
			wantCalls: 4,
		},
		{
			name:     "should leave a finished file as it is",
			existing: "a\nb\nc\nd\n",
			args:     []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:    input,
			wantFile: "a\nb\nc\nd\n",
		},
		{
			name:     "should empty the file when there is nothing to answer",
			existing: "stale\n",
			args:     []string{"is this urgent", "-i", "jsonl"},
			wantFile: "",
		},
		{
			name:     "should leave the file untouched when the command is rejected",
			existing: "keep me\n",
			args:     []string{"is this urgent", "--resume"},
			stdin:    "one record",
			wantCode: ExitUsage,
			wantFile: "keep me\n",
		},
		{
			name:      "should leave the file untouched when the run fails before any answer",
			existing:  "keep me\n",
			args:      []string{"is this urgent", "-q"},
			stdin:     "the site is down",
			wantCode:  ExitTransport,
			wantFile:  "keep me\n",
			unreached: true,
		},
		{
			name:     "should leave the file untouched on a rejected fresh run",
			existing: "keep me\n",
			args:     []string{"is this urgent", "-i", "jsonl", "--unordered", "--resume"},
			stdin:    input,
			wantCode: ExitUsage,
			wantFile: "keep me\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")

				if _, err := io.WriteString(w,
					`{"model":"m","answers":{"answer":{"type":"noul","noul":0.5}},"usage":{}}`); err != nil {
					t.Errorf("writing the stub response: %v", err)
				}
			}))
			defer srv.Close()

			path := filepath.Join(t.TempDir(), "answers.jsonl")
			if !tc.noFile {
				if err := os.WriteFile(path, []byte(tc.existing), 0o600); err != nil {
					t.Fatalf("writing the existing file: %v", err)
				}
			}

			var out, errOut bytes.Buffer

			root := NewRootCmd(
				BuildInfo{Version: "1.2.3"},
				WithStdin(strings.NewReader(tc.stdin)),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithKeychain(noKeychain()),
				WithLookupEnv(lookupFrom(nil)),
				WithClientFactory(stubFactory(baseFor(srv.URL, tc.unreached))),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(append([]string{"--out", path, "-o", "values"}, tc.args...))

			if code := Execute(t.Context(), root); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut.String())
			}

			if out.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", out.String())
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the file: %v", err)
			}

			if string(data) != tc.wantFile {
				t.Errorf("file = %q, want %q", data, tc.wantFile)
			}

			if got := calls.Load(); got != tc.wantCalls {
				t.Errorf("requests = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

func baseFor(url string, unreached bool) string {
	if unreached {
		return "http://127.0.0.1:1"
	}

	return url
}
