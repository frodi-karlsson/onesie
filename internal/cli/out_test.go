package cli

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestOpenOut(t *testing.T) {
	t.Parallel()

	const input = "{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n{\"id\":4}\n"

	const bodies = "{\"id\":1,\"body\":\"a\"}\n{\"id\":2,\"body\":\"b\"}\n" +
		"{\"id\":3,\"body\":\"c\"}\n{\"id\":4,\"body\":\"d\"}\n"

	matching := fingerprintFor(t, "is this urgent", "", "", "")
	changed := "the questions, model, --map or --id changed since"

	tests := []struct {
		name        string
		existing    string
		noFile      bool
		sidecar     string
		args        []string
		stdin       string
		wantCode    int
		wantFile    string
		wantSidecar string
		wantErr     string
		wantCalls   int32
		unreached   bool
	}{
		{
			name:        "should write the answers to the file and nothing to stdout",
			noFile:      true,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl"},
			stdin:       input,
			wantFile:    strings.Repeat("{\"answer\":0.5}\n", 4),
			wantCalls:   4,
		},
		{
			name:        "should skip the records the file already answers",
			existing:    "old one\nold two\n",
			sidecar:     matching,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls:   2,
		},
		{
			name:        "should drop a line the earlier run was cut off in",
			existing:    "old one\nold two\nold thr",
			sidecar:     matching,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls:   2,
		},
		{
			name:        "should start fresh when the file does not exist yet",
			noFile:      true,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    strings.Repeat("{\"answer\":0.5}\n", 4),
			wantCalls:   4,
		},
		{
			name:        "should leave a finished file as it is",
			existing:    "a\nb\nc\nd\n",
			sidecar:     matching,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "a\nb\nc\nd\n",
		},
		{
			name:        "should empty the file when there is nothing to answer",
			existing:    "stale\n",
			sidecar:     "stale fingerprint",
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl"},
			wantFile:    "",
		},
		{
			name:        "should leave the file untouched when the command is rejected",
			existing:    "keep me\n",
			sidecar:     "keep this too",
			wantSidecar: "keep this too",
			args:        []string{"is this urgent", "--resume"},
			stdin:       "one record",
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
		},
		{
			name:        "should leave the file untouched when the run fails before any answer",
			existing:    "keep me\n",
			sidecar:     "keep this too",
			wantSidecar: "keep this too",
			args:        []string{"is this urgent", "-q"},
			stdin:       "the site is down",
			wantCode:    ExitTransport,
			wantFile:    "keep me\n",
			unreached:   true,
		},
		{
			name:        "should leave the file untouched on a rejected fresh run",
			existing:    "keep me\n",
			sidecar:     "keep this too",
			wantSidecar: "keep this too",
			args:        []string{"is this urgent", "-i", "jsonl", "--unordered", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
		},
		{
			name:     "should write the fingerprint beside the file on a fresh run",
			existing: "stale\n",
			args: []string{
				"is this urgent", "-i", "jsonl", "-m", "m1", "--map", ".body", "--id", ".id",
			},
			stdin:       bodies,
			wantFile:    idLines(1, 4),
			wantSidecar: fingerprintFor(t, "is this urgent", "m1", ".body", ".id"),
			wantCalls:   4,
		},
		{
			name:     "should resume when the fingerprint matches every setting",
			existing: idLines(1, 2),
			sidecar:  fingerprintFor(t, "is this urgent", "m1", ".body", ".id"),
			args: []string{
				"is this urgent", "-i", "jsonl", "-m", "m1", "--map", ".body", "--id", ".id", "--resume",
			},
			stdin:       bodies,
			wantFile:    idLines(1, 4),
			wantSidecar: fingerprintFor(t, "is this urgent", "m1", ".body", ".id"),
			wantCalls:   2,
		},
		{
			name:        "should refuse to resume when the questions changed",
			existing:    "keep me\n",
			sidecar:     fingerprintFor(t, "is this critical", "", "", ""),
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: fingerprintFor(t, "is this critical", "", "", ""),
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when the model changed",
			existing:    "keep me\n",
			sidecar:     fingerprintFor(t, "is this urgent", "a", "", ""),
			args:        []string{"is this urgent", "-i", "jsonl", "-m", "b", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: fingerprintFor(t, "is this urgent", "a", "", ""),
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when --map changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--map", ".body", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when --id changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--id", ".id", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:     "should refuse to resume a file with no fingerprint beside it",
			existing: "keep me\n",
			args:     []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:    input,
			wantCode: ExitUsage,
			wantFile: "keep me\n",
			wantErr:  "has no fingerprint beside it",
		},
		{
			name:        "should resume an empty file with no fingerprint beside it",
			existing:    "",
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    strings.Repeat("{\"answer\":0.5}\n", 4),
			wantSidecar: matching,
			wantCalls:   4,
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

			if tc.sidecar != "" {
				if err := os.WriteFile(path+".onesie", []byte(tc.sidecar+"\n"), 0o600); err != nil {
					t.Fatalf("writing the existing fingerprint: %v", err)
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

			if !strings.Contains(errOut.String(), tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tc.wantErr)
			}

			sidecar, err := os.ReadFile(path + ".onesie")

			switch {
			case tc.wantSidecar == "" && !os.IsNotExist(err):
				t.Errorf("fingerprint file = %q, %v, want none", sidecar, err)
			case tc.wantSidecar != "" && string(sidecar) != tc.wantSidecar+"\n":
				t.Errorf("fingerprint file = %q, %v, want %q", sidecar, err, tc.wantSidecar+"\n")
			}
		})
	}
}

func fingerprintFor(t *testing.T, question, model, mapSource, idSource string) string {
	t.Helper()

	built, err := plan.Assemble(plan.Source{Positional: question})
	if err != nil {
		t.Fatalf("assembling %q: %v", question, err)
	}

	fingerprint, err := fingerprintOf(built.Questions, model, mapSource, idSource)
	if err != nil {
		t.Fatalf("fingerprinting %q: %v", question, err)
	}

	return fingerprint
}

func idLines(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"id\":%d,\"answer\":0.5}\n", id)
	}

	return lines.String()
}

func baseFor(url string, unreached bool) string {
	if unreached {
		return "http://127.0.0.1:1"
	}

	return url
}
