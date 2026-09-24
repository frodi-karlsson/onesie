package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestOpenOut(t *testing.T) {
	t.Parallel()

	const input = "{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n{\"id\":4}\n"

	const bodies = "{\"id\":1,\"body\":\"a\"}\n{\"id\":2,\"body\":\"b\"}\n" +
		"{\"id\":3,\"body\":\"c\"}\n{\"id\":4,\"body\":\"d\"}\n"

	matching := fingerprintFor(t, "is this urgent", "typesafe", jev.DefaultModel, "", "")
	changed := "the questions, provider, model, --map or --id changed since"
	malformed := "does not hold a fingerprint onesie wrote"

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
		env         map[string]string
		bare        bool

		emptySidecar     bool
		sidecarDir       bool
		readOnlyDir      bool
		wantEmptySidecar bool
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
			wantSidecar: fingerprintFor(t, "is this urgent", "typesafe", "m1", ".body", ".id"),
			wantCalls:   4,
		},
		{
			name:     "should resume when the fingerprint matches every setting",
			existing: idLines(1, 2),
			sidecar:  fingerprintFor(t, "is this urgent", "typesafe", "m1", ".body", ".id"),
			args: []string{
				"is this urgent", "-i", "jsonl", "-m", "m1", "--map", ".body", "--id", ".id", "--resume",
			},
			stdin:       bodies,
			wantFile:    idLines(1, 4),
			wantSidecar: fingerprintFor(t, "is this urgent", "typesafe", "m1", ".body", ".id"),
			wantCalls:   2,
		},
		{
			name:        "should refuse to resume when the questions changed",
			existing:    "keep me\n",
			sidecar:     fingerprintFor(t, "is this critical", "typesafe", jev.DefaultModel, "", ""),
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: fingerprintFor(t, "is this critical", "typesafe", jev.DefaultModel, "", ""),
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when the model changed",
			existing:    "keep me\n",
			sidecar:     fingerprintFor(t, "is this urgent", "typesafe", "a", "", ""),
			args:        []string{"is this urgent", "-i", "jsonl", "-m", "b", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: fingerprintFor(t, "is this urgent", "typesafe", "a", "", ""),
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
		{
			name:        "should refuse to resume when the provider changed",
			existing:    "keep me\n",
			sidecar:     matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--provider", "openrouter", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should refuse to resume when the default model changed",
			existing:    "keep me\n",
			sidecar:     matching,
			env:         map[string]string{jev.EnvDefaultModel: "jev-next"},
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching,
			wantErr:     changed,
		},
		{
			name:        "should resume when -m names the default model a plain run used",
			existing:    "old one\nold two\n",
			sidecar:     matching,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "-m", jev.DefaultModel, "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls:   2,
		},
		{
			name:        "should resume without rewriting a fingerprint that matches",
			existing:    "old one\nold two\n",
			sidecar:     matching,
			readOnlyDir: true,
			wantSidecar: matching,
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantFile:    "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
			wantCalls:   2,
		},
		{
			name:       "should refuse to resume beside a fingerprint it cannot read",
			existing:   "keep me\n",
			sidecarDir: true,
			args:       []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:      input,
			wantCode:   ExitUsage,
			wantFile:   "keep me\n",
			wantErr:    "answers.jsonl.onesie: ",
		},
		{
			name:        "should refuse to resume beside a malformed fingerprint",
			existing:    "keep me\n",
			sidecar:     "garbage",
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: "garbage",
			wantErr:     "answers.jsonl.onesie " + malformed,
		},
		{
			name:        "should refuse to resume beside a truncated fingerprint",
			existing:    "keep me\n",
			sidecar:     matching[:10],
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: matching[:10],
			wantErr:     malformed,
		},
		{
			name:             "should refuse to resume beside a fingerprint an interrupted resume left empty",
			existing:         "keep me\n",
			emptySidecar:     true,
			args:             []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:            input,
			wantCode:         ExitUsage,
			wantFile:         "keep me\n",
			wantEmptySidecar: true,
			wantErr:          malformed,
		},
		{
			name:        "should say an older onesie wrote a fingerprint of an earlier version",
			existing:    "keep me\n",
			sidecar:     "v0:" + strings.Repeat("0", 64),
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: "v0:" + strings.Repeat("0", 64),
			wantErr:     "an older onesie wrote",
		},
		{
			name:        "should say a newer onesie wrote a fingerprint of a later version",
			existing:    "keep me\n",
			sidecar:     "v2:" + strings.Repeat("0", 64),
			args:        []string{"is this urgent", "-i", "jsonl", "--resume"},
			stdin:       input,
			wantCode:    ExitUsage,
			wantFile:    "keep me\n",
			wantSidecar: "v2:" + strings.Repeat("0", 64),
			wantErr:     "a newer onesie wrote",
		},
		{
			name:     "should write no fingerprint beside a question file",
			existing: "stale\n",
			sidecar:  matching,
			bare:     true,
			args:     []string{"--ask", "urgent=is this urgent", "--print-questions"},
			wantFile: "urgent:\n  ask: is this urgent\n",
		},
		{
			name:      "should write no fingerprint beside a list of models",
			existing:  "stale\n",
			sidecar:   matching,
			bare:      true,
			args:      []string{"--list-models"},
			wantFile:  "jev-latest  d  r\n",
			wantCalls: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")

				body := `{"model":"m","answers":{"answer":{"type":"noul","noul":0.5}},"usage":{}}`
				if r.URL.Path == "/v1/models" {
					body = `{"models":[{"name":"jev-latest","description":"d","release_date":"r"}]}`
				}

				if _, err := io.WriteString(w, body); err != nil {
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

			writeSidecar(t, path+".onesie", tc.sidecar, tc.emptySidecar, tc.sidecarDir)

			if tc.readOnlyDir {
				lockDir(t, filepath.Dir(path))
			}

			var out, errOut bytes.Buffer

			root := NewRootCmd(
				BuildInfo{Version: "1.2.3"},
				WithStdin(strings.NewReader(tc.stdin)),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithKeychain(noKeychain()),
				WithLookupEnv(lookupFrom(tc.env)),
				WithClientFactory(stubFactory(baseFor(srv.URL, tc.unreached))),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			global := []string{"--out", path, "-o", "values"}
			if tc.bare {
				global = global[:2]
			}

			root.SetArgs(append(global, tc.args...))

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
			case tc.sidecarDir:
			case tc.wantEmptySidecar && (err != nil || len(sidecar) != 0):
				t.Errorf("fingerprint file = %q, %v, want it left empty", sidecar, err)
			case tc.wantEmptySidecar:
			case tc.wantSidecar == "" && !os.IsNotExist(err):
				t.Errorf("fingerprint file = %q, %v, want none", sidecar, err)
			case tc.wantSidecar != "" && string(sidecar) != tc.wantSidecar+"\n":
				t.Errorf("fingerprint file = %q, %v, want %q", sidecar, err, tc.wantSidecar+"\n")
			}
		})
	}
}

func TestOutFile_Write(t *testing.T) {
	t.Parallel()

	failedRename := errors.New("disk full")

	tests := []struct {
		name        string
		resume      bool
		bind        bool
		rename      func(string, string) error
		wantErr     string
		wantFile    string
		wantSidecar string
		wantRenames []string
	}{
		{
			name:        "should write the fingerprint through a temporary file and a rename",
			bind:        true,
			wantFile:    "answer\n",
			wantSidecar: "v1:" + strings.Repeat("a", 64) + "\n",
			wantRenames: []string{"answers.jsonl.onesie.tmp answers.jsonl.onesie"},
		},
		{
			name:        "should refuse a resume that was never bound",
			resume:      true,
			wantErr:     "was never checked against its fingerprint",
			wantFile:    "keep me\n",
			wantSidecar: "keep this\n",
		},
		{
			name:        "should let go of the file when the fingerprint cannot be written",
			bind:        true,
			rename:      func(string, string) error { return failedRename },
			wantErr:     "disk full",
			wantFile:    "",
			wantSidecar: "keep this\n",
			wantRenames: []string{
				"answers.jsonl.onesie.tmp answers.jsonl.onesie",
				"answers.jsonl.onesie.tmp answers.jsonl.onesie",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "answers.jsonl")

			if err := os.WriteFile(path, []byte("keep me\n"), 0o600); err != nil {
				t.Fatalf("writing the existing file: %v", err)
			}

			if err := os.WriteFile(path+".onesie", []byte("keep this\n"), 0o600); err != nil {
				t.Fatalf("writing the existing fingerprint: %v", err)
			}

			rename := tc.rename
			if rename == nil {
				rename = os.Rename
			}

			var renames []string

			out := &outFile{
				path:    path,
				open:    os.OpenFile,
				remove:  os.Remove,
				resolve: filepath.EvalSymlinks,
				rename: func(from, to string) error {
					renames = append(renames, filepath.Base(from)+" "+filepath.Base(to))

					return rename(from, to)
				},
				resume: tc.resume,
				keep:   int64(len("keep me\n")),
			}

			if tc.bind {
				if err := out.bind("v1:" + strings.Repeat("a", 64)); err != nil {
					t.Fatalf("bind: %v", err)
				}
			}

			_, writeErr := io.WriteString(out, "answer\n")
			if writeErr == nil {
				writeErr = out.finish(nil)
			}

			if !strings.Contains(fmt.Sprint(writeErr), tc.wantErr) || (tc.wantErr == "") != (writeErr == nil) {
				t.Fatalf("error = %v, want %q", writeErr, tc.wantErr)
			}

			if writeErr != nil {
				if out.file != nil {
					t.Errorf("file = %v, want it let go after the failure", out.file)
				}

				_, againErr := io.WriteString(out, "again\n")
				if againErr == nil || errors.Is(againErr, os.ErrClosed) {
					t.Errorf("second write error = %v, want the first failure again", againErr)
				}

				if err := out.finish(writeErr); err != nil {
					t.Errorf("finish after the failure: %v", err)
				}
			}

			assertFileHolds(t, path, tc.wantFile)
			assertFileHolds(t, path+".onesie", tc.wantSidecar)

			if _, err := os.Stat(path + ".onesie.tmp"); !os.IsNotExist(err) {
				t.Errorf("temporary fingerprint left behind: %v", err)
			}

			if strings.Join(renames, ",") != strings.Join(tc.wantRenames, ",") {
				t.Errorf("renames = %q, want %q", renames, tc.wantRenames)
			}
		})
	}
}

func TestFingerprintOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		question string
		provider string
		model    string
		mapSrc   string
		idSrc    string
		want     string
	}{
		{
			name:     "should keep the fingerprint format a sidecar on disk was written in",
			question: "is this urgent",
			provider: "typesafe",
			model:    "jev-latest",
			mapSrc:   ".body",
			idSrc:    ".id",
			want:     "v1:1ac939d902a081db7010b65849b69fc0a88cfa4373d81bd466316aae7dd6caef",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := fingerprintFor(t, tc.question, tc.provider, tc.model, tc.mapSrc, tc.idSrc)
			if got != tc.want {
				t.Errorf("fingerprint = %q, want %q", got, tc.want)
			}
		})
	}
}

func assertFileHolds(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", filepath.Base(path), err)
	}

	if string(data) != want {
		t.Errorf("%s = %q, want %q", filepath.Base(path), data, want)
	}
}

func writeSidecar(t *testing.T, path, content string, empty, dir bool) {
	t.Helper()

	switch {
	case dir:
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatalf("making the fingerprint a directory: %v", err)
		}
	case empty:
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("writing the empty fingerprint: %v", err)
		}
	case content != "":
		if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
			t.Fatalf("writing the existing fingerprint: %v", err)
		}
	}
}

func lockDir(t *testing.T, dir string) {
	t.Helper()

	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("locking %s: %v", dir, err)
	}

	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("unlocking %s: %v", dir, err)
		}
	})
}

func fingerprintFor(t *testing.T, question, provider, model, mapSource, idSource string) string {
	t.Helper()

	built, err := plan.Assemble(plan.Source{Positional: question})
	if err != nil {
		t.Fatalf("assembling %q: %v", question, err)
	}

	fingerprint, err := fingerprintOf(built.Questions, provider, model, mapSource, idSource)
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
