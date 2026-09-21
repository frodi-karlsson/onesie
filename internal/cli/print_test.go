package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/qfile"
)

func TestNewRootCmdPrintQuestions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantCode int
		stdout   []string
		absent   []string
		stderr   []string
	}{
		{
			name:     "should write a question file and exit without a request",
			args:     []string{"--ask", "urgent=is this urgent", "--print-questions"},
			wantCode: ExitOK,
			stdout:   []string{"urgent:", "ask: is this urgent"},
		},
		{
			name: "should write the questions in the order they were asked",
			args: []string{
				"--ask", "zebra=z", "--ask", "mike=m", "--ask", "alpha=a", "--print-questions",
			},
			wantCode: ExitOK,
			stdout:   []string{"zebra:\n  ask: z\nmike:\n  ask: m\nalpha:\n  ask: a\n"},
		},
		{
			name: "should write the policy keys the loader reads",
			args: []string{
				"--ask", "team=who owns this", "--pick", "billing,platform",
				"--min-confidence", "0.7", "--fallback", "billing", "--print-questions",
			},
			wantCode: ExitOK,
			stdout:   []string{"min_confidence: 0.7", "fallback: billing"},
			absent:   []string{"min-confidence"},
		},
		{
			name: "should write a yes and no rubric under the _means keys",
			args: []string{
				"--ask", "spam=is this spam", "--desc", "yes=bulk or bot sent",
				"--desc", "no=written by a person", "--print-questions",
			},
			wantCode: ExitOK,
			stdout:   []string{"yes_means: bulk or bot sent", "no_means: written by a person"},
		},
		{
			name: "should read no state at all",
			args: []string{
				"--ask", "urgent=is this urgent", "--state-file", "/nonexistent",
				"--print-questions",
			},
			wantCode: ExitOK,
			stdout:   []string{"urgent:\n  ask: is this urgent\n"},
		},
		{
			name:     "should reject print-questions for a positional question",
			args:     []string{"is this urgent", "--print-questions"},
			wantCode: ExitUsage,
			stderr:   []string{"--print-questions needs a named question"},
		},
		{
			name:     "should report the missing key when the run does make a request",
			args:     []string{"--ask", "urgent=is this urgent", "--state", "the server is down"},
			wantCode: ExitUsage,
			stderr:   []string{"jev: no API key"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runOffline(t, tc.args)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out, errOut)
			}

			for _, want := range tc.stdout {
				if !strings.Contains(out, want) {
					t.Errorf("stdout missing %q\ngot:\n%s", want, out)
				}
			}

			for _, unwanted := range tc.absent {
				if strings.Contains(out, unwanted) {
					t.Errorf("stdout should not contain %q\ngot:\n%s", unwanted, out)
				}
			}

			for _, want := range tc.stderr {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr missing %q\ngot:\n%s", want, errOut)
				}
			}

			// A case that names nothing on a stream expects that stream to stay empty. The whole
			// point of the documented redirection is that the file lands on stdout and nothing
			// else does.
			if len(tc.stdout) == 0 && out != "" {
				t.Errorf("stdout should be empty, got:\n%s", out)
			}

			if len(tc.stderr) == 0 && errOut != "" {
				t.Errorf("stderr should be empty, got:\n%s", errOut)
			}
		})
	}
}

func TestWire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "should keep a score with string criteria on the wire",
			body: "questions:\n  tone:\n    type: score\n    instructions: how formal\n" +
				"    criteria:\n    - very casual\n    - neutral\n    - very formal\n",
		},
		{
			name: "should keep a score with structured criteria on the wire",
			body: "questions:\n  tone:\n    type: score\n    instructions: how formal\n" +
				"    criteria:\n    - zebra: z\n      alpha: a\n    - mike: m\n",
		},
		{
			name: "should keep a choice on the wire",
			body: "questions:\n  team:\n    type: choice\n    instructions: who owns this\n" +
				"    criteria:\n      billing: money\n      platform: systems\n",
		},
		{
			name: "should keep a noul with a rubric on the wire",
			body: "questions:\n  spam:\n    type: noul\n    instructions: is this spam\n" +
				"    criteria:\n      true: bulk or bot sent\n      false: written by a person\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "body.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("writing the body: %v", err)
			}

			printed, errOut, code := runOffline(t, []string{"-f", path, "--print-questions"})
			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			// Compared as wire bodies rather than as plans, because a body's levels have no labels
			// and the file gives them index ones by design, so the two plans differ where the two
			// requests must not.
			before := wireBody(t, []byte(tc.body))
			after := wireBody(t, []byte(printed))

			if before != after {
				t.Errorf("the printed file changed the request body\nbefore %s\nafter  %s\n"+
					"file was\n%s", before, after, printed)
			}
		})
	}
}

func wireBody(t *testing.T, data []byte) string {
	t.Helper()

	file, err := qfile.Load(data)
	if err != nil {
		t.Fatalf("Load: %v\nfile was\n%s", err, data)
	}

	questions := make(jev.Questions, 0, len(file.Questions))
	for _, question := range file.Questions {
		questions = append(questions, jev.NamedQuestion{ID: question.ID, Question: wire(question)})
	}

	encoded, err := json.Marshal(questions)
	if err != nil {
		t.Fatalf("marshalling the wire body: %v", err)
	}

	return string(encoded)
}

func TestPrintRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		stdin    string
		wantCode int
		stdout   []string
		absent   []string
		stderr   []string
	}{
		{
			name:     "should print one request body and exit without a request",
			args:     []string{"--ask", "urgent=is this urgent", "--print-request"},
			stdin:    "the server is down",
			wantCode: ExitOK,
			stdout: []string{
				`{"state":"the server is down","model":"jev-latest","questions":` +
					`{"urgent":{"type":"noul","instructions":"is this urgent"}}}` + "\n",
			},
		},
		{
			name:     "should omit state when none was given",
			args:     []string{"--ask", "urgent=is this urgent", "--print-request"},
			wantCode: ExitOK,
			stdout: []string{
				`{"model":"jev-latest","questions":` +
					`{"urgent":{"type":"noul","instructions":"is this urgent"}}}` + "\n",
			},
			absent: []string{`"state"`},
		},
		{
			name: "should keep the state's key order and its digits",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "json", "--print-request",
			},
			stdin:    `{"ticket_id":12345678901234567890,"zebra":1,"alpha":2}`,
			wantCode: ExitOK,
			stdout:   []string{`"state":{"ticket_id":12345678901234567890,"zebra":1,"alpha":2}`},
		},
		{
			name: "should print the questions in the order they were asked",
			args: []string{
				"--ask", "zebra=z", "--ask", "mike=m", "--ask", "alpha=a", "--print-request",
			},
			stdin:    "s",
			wantCode: ExitOK,
			stdout: []string{
				`"questions":{"zebra":{"type":"noul","instructions":"z"},` +
					`"mike":{"type":"noul","instructions":"m"},` +
					`"alpha":{"type":"noul","instructions":"a"}}`,
			},
		},
		{
			name: "should print a body for a positional question",
			args: []string{"is this urgent", "--print-request"},
			// A positional question is rejected by --print-questions and accepted here, because a
			// request body needs no name a caller has to type.
			stdin:    "the server is down",
			wantCode: ExitOK,
			stdout:   []string{`"answer":{"type":"noul","instructions":"is this urgent"}`},
		},
		{
			// The control for every case above. The same command without the flag needs a key it
			// does not have, so an exit of zero there is the dry run and not an empty run.
			name:     "should report the missing key when the run does make a request",
			args:     []string{"--ask", "urgent=is this urgent"},
			stdin:    "the server is down",
			wantCode: ExitUsage,
			stderr:   []string{"jev: no API key"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runOfflineStdin(t, tc.args, tc.stdin)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out, errOut)
			}

			for _, want := range tc.stdout {
				if !strings.Contains(out, want) {
					t.Errorf("stdout missing %q\ngot:\n%s", want, out)
				}
			}

			for _, unwanted := range tc.absent {
				if strings.Contains(out, unwanted) {
					t.Errorf("stdout should not contain %q\ngot:\n%s", unwanted, out)
				}
			}

			for _, want := range tc.stderr {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr missing %q\ngot:\n%s", want, errOut)
				}
			}

			// The body belongs on stdout, so a case that names nothing on a stream expects that
			// stream to stay empty rather than to carry the body it forgot to ask for.
			if len(tc.stdout) == 0 && out != "" {
				t.Errorf("stdout should be empty, got:\n%s", out)
			}

			if len(tc.stderr) == 0 && errOut != "" {
				t.Errorf("stderr should be empty, got:\n%s", errOut)
			}
		})
	}

	t.Run("should print a question only body that -f accepts", func(t *testing.T) {
		t.Parallel()

		printed, errOut, code := runOffline(t,
			[]string{"--ask", "urgent=is this urgent", "--print-request"})
		if code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
		}

		path := filepath.Join(t.TempDir(), "body.json")
		if err := os.WriteFile(path, []byte(printed), 0o600); err != nil {
			t.Fatalf("writing the body: %v", err)
		}

		reloaded, errOut, code := runOffline(t, []string{"-f", path, "--print-request"})
		if code != ExitOK {
			t.Fatalf("reloading exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
		}

		if reloaded != printed {
			t.Errorf("the body did not survive -f\nprinted  %s\nreloaded %s", printed, reloaded)
		}
	})

	t.Run("should carry the state a request body brought with it", func(t *testing.T) {
		t.Parallel()

		const body = `{"state":{"zebra":1,"alpha":2},"model":"jev-1.13.0",` +
			`"questions":{"urgent":{"type":"noul","instructions":"is this urgent"}}}`

		path := filepath.Join(t.TempDir(), "body.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing the body: %v", err)
		}

		printed, errOut, code := runOffline(t, []string{"-f", path, "--print-request"})
		if code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
		}

		if printed != body+"\n" {
			t.Errorf("printed %s, want %s", printed, body)
		}
	})
}

func TestPrintRequestMatchesSentBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  []string
		stdin string
		file  string
	}{
		{
			name:  "should print the trimmed model an untrimmed -m sends",
			args:  []string{"--ask", "urgent=is this urgent", "-m", " jev-1.13.0 "},
			stdin: "the server is down",
		},
		{
			name:  "should print the resolved model a whitespace only -m sends",
			args:  []string{"--ask", "urgent=is this urgent", "-m", "   "},
			stdin: "the server is down",
		},
		{
			name: "should print the trimmed model an untrimmed request body sends",
			file: `{"state":"the server is down","model":" jev-1.13.0 ","questions":` +
				`{"urgent":{"type":"noul","instructions":"is this urgent"}}}`,
		},
		{
			name: "should print the resolved model a tab only request body sends",
			file: `{"state":"the server is down","model":"\t","questions":` +
				`{"urgent":{"type":"noul","instructions":"is this urgent"}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := tc.args

			if tc.file != "" {
				path := filepath.Join(t.TempDir(), "body.json")
				if err := os.WriteFile(path, []byte(tc.file), 0o600); err != nil {
					t.Fatalf("writing the body: %v", err)
				}

				args = []string{"-f", path}
			}

			printArgs := make([]string, 0, len(args)+1)
			printArgs = append(printArgs, args...)
			printArgs = append(printArgs, "--print-request")

			printed, errOut, code := runOfflineStdin(t, printArgs, tc.stdin)
			if code != ExitOK {
				t.Fatalf("printing exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			sent, errOut, code := runRecorded(t, args, tc.stdin)
			if code != ExitOK {
				t.Fatalf("sending exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			if want := strings.TrimSuffix(printed, "\n"); sent != want {
				t.Errorf("sent body    %s\nprinted body %s", sent, want)
			}
		})
	}
}

// runRecorded runs the given arguments against a stub that answers one noul, and returns the
// request body it received.
func runRecorded(t *testing.T, args []string, stdin string) (string, string, int) {
	t.Helper()

	var (
		mu   sync.Mutex
		sent string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}

		mu.Lock()
		sent = string(body)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		if _, err := io.WriteString(w,
			`{"model":"m","answers":{"urgent":{"type":"noul","noul":0.1}},"usage":{}}`); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
			// The client gets its own blank environment as well as the command, so the machine
			// running the test cannot supply a default model to one side of the comparison.
			return jev.New(append([]jev.Option{
				jev.WithAPIKey("k"),
				jev.WithBaseURL(srv.URL),
				jev.WithEnv(func(string) (string, bool) { return "", false }),
			}, opts...)...)
		}),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(func(string) (string, bool) { return "", false }),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	mu.Lock()
	defer mu.Unlock()

	return sent, errOut.String(), code
}

func TestPrintRequestMerge(t *testing.T) {
	t.Parallel()

	const message = "jev: --merge needs answers to fold in, " +
		"which --print-request does not produce"

	tests := []struct {
		name  string
		args  []string
		stdin string
	}{
		{
			name: "should reject merge on a single body",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "json", "--print-request", "--merge",
			},
			stdin: `{"answers":1}`,
		},
		{
			name: "should reject merge on a streamed body",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request", "--merge",
			},
			stdin: `{"answers":1}` + "\n",
		},
		{
			name: "should reject merge-key on a streamed body",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request",
				"--merge-key", "jev",
			},
			stdin: `{"answers":1}` + "\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runOfflineStdin(t, tc.args, tc.stdin)

			if code != ExitUsage {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, ExitUsage, out, errOut)
			}

			// Nothing on stdout, which is the point of rejecting before the body is written.
			if out != "" {
				t.Errorf("stdout should be empty, got:\n%s", out)
			}

			want := message
			if strings.Contains(strings.Join(tc.args, " "), "--merge-key") {
				want = strings.Replace(message, "--merge", "--merge-key", 1)
			}

			if !strings.Contains(errOut, want) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, want)
			}
		})
	}
}

func TestStreamRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		stdin     string
		wantCode  int
		wantLines int
		stdout    []string
	}{
		{
			name:      "should print one body per input line",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request"},
			stdin:     "{\"a\":1}\n{\"b\":2}\n",
			wantCode:  ExitOK,
			wantLines: 2,
			stdout:    []string{`"state":{"a":1}`, `"state":{"b":2}`},
		},
		{
			name:      "should print one body per line in lines mode",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "lines", "--print-request"},
			stdin:     "first\nsecond\nthird\n",
			wantCode:  ExitOK,
			wantLines: 3,
			stdout:    []string{`"state":"first"`, `"state":"second"`, `"state":"third"`},
		},
		{
			name:      "should fail a bad line and still write a line for it",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request"},
			stdin:     "{\"a\":1}\nnot json\n",
			wantCode:  ExitRecords,
			wantLines: 2,
			stdout:    []string{`"state":{"a":1}`, `{"error":{"kind":"input"`, "line 2"},
		},
		{
			name:      "should write nothing for an empty stream",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request"},
			stdin:     "",
			wantCode:  ExitOK,
			wantLines: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runOfflineStdin(t, tc.args, tc.stdin)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out, errOut)
			}

			written := outputLines(out)
			if len(written) != tc.wantLines {
				t.Errorf("wrote %d lines, want %d\ngot:\n%s", len(written), tc.wantLines, out)
			}

			// A failed record writes its error record rather than nothing, which is what keeps the
			// output one line per input line.
			for i, written := range written {
				if written == "" {
					t.Errorf("line %d is blank\ngot:\n%s", i+1, out)
				}
			}

			for _, want := range tc.stdout {
				if !strings.Contains(out, want) {
					t.Errorf("stdout missing %q\ngot:\n%s", want, out)
				}
			}

			if errOut != "" {
				t.Errorf("stderr should be empty, got:\n%s", errOut)
			}
		})
	}
}

func outputLines(out string) []string {
	trimmed := strings.TrimSuffix(out, "\n")
	if trimmed == "" {
		return nil
	}

	return strings.Split(trimmed, "\n")
}

func runOffline(t *testing.T, args []string) (string, string, int) {
	t.Helper()

	return runOfflineStdin(t, args, "")
}

func runOfflineStdin(t *testing.T, args []string, stdin string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	// No client factory and no environment, so the real factory runs against a machine with no key.
	// A case that exits ok here reached no network at all, which is the whole claim the two print
	// flags make.
	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithStdin(strings.NewReader(stdin)),
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
