package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func runOffline(t *testing.T, args []string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	// No client factory and no environment, so the real factory runs against a machine with no key
	// and no state on stdin. A case that exits ok here reached neither the network nor stdin, which
	// is the whole claim --print-questions makes.
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
