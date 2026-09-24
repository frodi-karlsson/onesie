package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func TestPrintQuestions(t *testing.T) {
	t.Parallel()

	const body = `{"questions":{"frustration":{"type":"score",` +
		`"instructions":"how cross is the writer","criteria":["calm","annoyed","furious"]}}}`

	bodyFile := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(bodyFile, []byte(body), 0o600); err != nil {
		t.Fatalf("writing the body: %v", err)
	}

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
			name: "should write the abstain expression into the printed file",
			args: []string{
				"--ask", "urgent=is this urgent", "--assert", "urgent.value < 0.2",
				"--abstain-if", "urgent.value < 0.8", "--print-questions",
			},
			wantCode: ExitOK,
			stdout: []string{
				"assert: urgent.value < 0.2\nabstain_if: urgent.value < 0.8\nurgent:\n",
			},
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
			name: "should print a body's score question given policy flags",
			args: []string{
				"-f", bodyFile, "--min-confidence", "0.7", "--fallback", "human",
				"--print-questions",
			},
			stdout:   []string{"frustration:", "rate:", `"0": calm`, `"2": furious`},
			wantCode: ExitOK,
		},
		{
			name: "should reject --state with --print-questions",
			args: []string{
				"--ask", "urgent=is this urgent", "--state", "x", "--print-questions",
			},
			wantCode: ExitUsage,
			stderr: []string{
				"onesie: --state does not apply to --print-questions, which reads no state",
			},
		},
		{
			name: "should reject --state-file with --print-questions before it is read",
			args: []string{
				"--ask", "urgent=is this urgent", "--state-file", "/nonexistent",
				"--print-questions",
			},
			wantCode: ExitUsage,
			stderr: []string{
				"onesie: --state-file does not apply to --print-questions, which reads no state",
			},
		},
		{
			name: "should reject -i with --print-questions",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "json", "--print-questions",
			},
			wantCode: ExitUsage,
			stderr: []string{
				"onesie: -i does not apply to --print-questions, which reads no input",
			},
		},
		{
			name: "should reject --usage with --print-questions",
			args: []string{
				"--ask", "urgent=is this urgent", "--usage", "--print-questions",
			},
			wantCode: ExitUsage,
			stderr: []string{
				"onesie: --usage reports the tokens a question cost, " +
					"which --print-questions does not ask",
			},
		},
		{
			name: "should reject -j with --print-questions",
			args: []string{
				"--ask", "urgent=is this urgent", "-j", "8", "--print-questions",
			},
			wantCode: ExitUsage,
			stderr: []string{
				"onesie: -j does not apply to --print-questions, which makes no request",
			},
		},
		{
			name: "should reject -m with --print-questions",
			args: []string{
				"--ask", "urgent=is this urgent", "-m", "onesie-9", "--print-questions",
			},
			wantCode: ExitUsage,
			stderr: []string{
				"onesie: -m names a model to ask, which --print-questions does not do",
			},
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
			stderr:   []string{"onesie: no API key"},
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

			// A case that names nothing on a stream expects that stream to stay empty. The
			// documented redirection promises the file lands on stdout and nothing else does.
			if len(tc.stdout) == 0 && out != "" {
				t.Errorf("stdout should be empty, got:\n%s", out)
			}

			if len(tc.stderr) == 0 && errOut != "" {
				t.Errorf("stderr should be empty, got:\n%s", errOut)
			}
		})
	}

	t.Run("should warn that a body's model and state are not carried", func(t *testing.T) {
		t.Parallel()

		const body = `{"state":{"zebra":1},"model":"onesie-1.13.0","questions":` +
			`{"urgent":{"type":"noul","instructions":"is this urgent"}}}`

		path := filepath.Join(t.TempDir(), "body.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing the body: %v", err)
		}

		out, errOut, code := runOffline(t, []string{"-f", path, "--print-questions"})
		if code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
		}

		for _, want := range []string{
			"warning: --print-questions does not carry the body's model 'onesie-1.13.0'. " +
				"Pass -m when you reload",
			"warning: --print-questions does not carry the body's state. " +
				"Pass --state when you reload",
		} {
			if !strings.Contains(errOut, want) {
				t.Errorf("stderr missing %q\ngot:\n%s", want, errOut)
			}
		}

		if !strings.Contains(out, "urgent:") {
			t.Errorf("stdout missing the question file, got:\n%s", out)
		}
	})

	t.Run("should warn about nothing for a question file", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "q.yaml")
		if err := os.WriteFile(path, []byte("urgent:\n  ask: is this urgent\n"), 0o600); err != nil {
			t.Fatalf("writing the question file: %v", err)
		}

		_, errOut, code := runOffline(t, []string{"-f", path, "--print-questions"})
		if code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
		}

		if errOut != "" {
			t.Errorf("stderr should be empty, got:\n%s", errOut)
		}
	})
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

	criteria := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "should send an empty no when only yes is described",
			args: []string{"--desc", "yes=bulk or bot sent"},
			want: `"criteria":{"true":"bulk or bot sent","false":""}`,
		},
		{
			name: "should send an empty yes when only no is described",
			args: []string{"--desc", "no=written by a person"},
			want: `"criteria":{"true":"","false":"written by a person"}`,
		},
		{
			name: "should send both descriptions unchanged when both are given",
			args: []string{"--desc", "yes=bulk or bot sent", "--desc", "no=written by a person"},
			want: `"criteria":{"true":"bulk or bot sent","false":"written by a person"}`,
		},
		{
			name: "should send no criteria when neither is described",
			want: `"questions":{"spam":{"type":"noul","instructions":"is this spam"}}`,
		},
	}

	for _, tc := range criteria {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			args := append([]string{"--ask", "spam=is this spam"}, tc.args...)
			args = append(args, "--state", "hello", "--print-request")

			printed, errOut, code := runOffline(t, args)
			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			if !strings.Contains(printed, tc.want) {
				t.Errorf("request body = %s, want it to contain %s", printed, tc.want)
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
			name: "should reject --usage with --print-request",
			args: []string{"--ask", "urgent=is this urgent", "--usage", "--print-request"},
			// A printed body has no usage object to add one to, so the flag is as inert here as
			// it is under --print-questions.
			stdin:    "the server is down",
			wantCode: ExitUsage,
			stderr: []string{
				"onesie: --usage reports the tokens a question cost, " +
					"which --print-request does not ask",
			},
		},
		{
			name:     "should send only the mapped field of one json record",
			args:     []string{"--ask", "urgent=is this urgent", "-i", "json", "--map", ".body", "--print-request"},
			stdin:    `{"customer":"c1","body":"the site is down"}`,
			wantCode: ExitOK,
			stdout:   []string{`{"state":"the site is down","model":"jev-latest"`},
			absent:   []string{"customer"},
		},
		{
			name:     "should map a text state with the identity expression",
			args:     []string{"--ask", "urgent=is this urgent", "--map", ".", "--print-request"},
			stdin:    "the server is down",
			wantCode: ExitOK,
			stdout:   []string{`{"state":"the server is down","model":"jev-latest"`},
		},
		{
			name:     "should exit two when one record maps to null",
			args:     []string{"--ask", "urgent=is this urgent", "-i", "json", "--map", ".body", "--print-request"},
			stdin:    `{"customer":"c1"}`,
			wantCode: ExitUsage,
			stderr:   []string{"onesie: --map: state must be a string, object or array, got null"},
		},
		{
			name:     "should exit two when one record's expression fails as it runs",
			args:     []string{"--ask", "urgent=is this urgent", "-i", "json", "--map", ".body.text", "--print-request"},
			stdin:    `{"body":"the site is down"}`,
			wantCode: ExitUsage,
			stderr:   []string{"onesie: --map fails: "},
		},
		{
			name: "should exit two when one record's result nests too deep",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "json", "--print-request",
				"--map", `reduce range(10001) as $i ("x"; [.])`,
			},
			stdin:    `{"body":"the site is down"}`,
			wantCode: ExitUsage,
			stderr:   []string{"onesie: --map: result nests deeper than 10000 levels"},
		},
		{
			name:     "should exit two on a --map syntax error",
			args:     []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--map", "{subject, body", "--print-request"},
			stdin:    "{\"body\":\"a\"}\n",
			wantCode: ExitUsage,
			stderr:   []string{"onesie: --map: unexpected EOF at column 15"},
		},
		{
			// The control for every case above. The same command without the flag needs a key it
			// does not have, so an exit of zero there is the dry run and not an empty run.
			name:     "should report the missing key when the run does make a request",
			args:     []string{"--ask", "urgent=is this urgent"},
			stdin:    "the server is down",
			wantCode: ExitUsage,
			stderr:   []string{"onesie: no API key"},
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

		const body = `{"state":{"zebra":1,"alpha":2},"model":"onesie-1.13.0",` +
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

	t.Run("should map the state of every single record source", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		stateFile := filepath.Join(dir, "state.json")
		if err := os.WriteFile(stateFile, []byte(`{"customer":"c1","body":"from the file"}`), 0o600); err != nil {
			t.Fatalf("writing the state file: %v", err)
		}

		bodyFile := filepath.Join(dir, "body.json")
		if err := os.WriteFile(bodyFile, []byte(`{"state":{"customer":"c1","body":"from the body"},`+
			`"questions":{"urgent":{"type":"noul","instructions":"is this urgent"}}}`), 0o600); err != nil {
			t.Fatalf("writing the body: %v", err)
		}

		tests := []struct {
			name  string
			args  []string
			stdin string
			want  string
		}{
			{
				name: "should map a --state object",
				args: []string{
					"--ask", "urgent=is this urgent", "-i", "json",
					"--state", `{"customer":"c1","body":"from the flag"}`, "--map", ".body",
				},
				want: `"state":"from the flag"`,
			},
			{
				name: "should map a --state text",
				args: []string{"--ask", "urgent=is this urgent", "--state", "the site is down", "--map", "ascii_upcase"},
				want: `"state":"THE SITE IS DOWN"`,
			},
			{
				name: "should map a --state-file object",
				args: []string{"--ask", "urgent=is this urgent", "-i", "json", "--state-file", stateFile, "--map", ".body"},
				want: `"state":"from the file"`,
			},
			{
				name: "should map the state a request body brought with it",
				args: []string{"-f", bodyFile, "--map", ".body"},
				want: `"state":"from the body"`,
			},
			{
				name:  "should map a json record on stdin",
				args:  []string{"--ask", "urgent=is this urgent", "-i", "json", "--map", "{body}"},
				stdin: `{"customer":"c1","body":"from stdin"}`,
				want:  `"state":{"body":"from stdin"}`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				printed, errOut, code := runOfflineStdin(t, append(slices.Clone(tc.args), "--print-request"), tc.stdin)
				if code != ExitOK {
					t.Fatalf("printing exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
				}

				if !strings.Contains(printed, tc.want) || strings.Contains(printed, "customer") {
					t.Errorf("printed %s, want it to contain %s and no customer", printed, tc.want)
				}

				sent, errOut, code := runRecorded(t, tc.args, tc.stdin)
				if code != ExitOK {
					t.Fatalf("sending exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
				}

				if want := strings.TrimSuffix(printed, "\n"); sent != want {
					t.Errorf("sent body    %s\nprinted body %s", sent, want)
				}
			})
		}
	})

	t.Run("should print a body matching what the equivalent request would send", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name  string
			args  []string
			stdin string
			file  string
		}{
			{
				name:  "should print the trimmed model an untrimmed -m sends",
				args:  []string{"--ask", "urgent=is this urgent", "-m", " onesie-1.13.0 "},
				stdin: "the server is down",
			},
			{
				name:  "should print the resolved model a whitespace only -m sends",
				args:  []string{"--ask", "urgent=is this urgent", "-m", "   "},
				stdin: "the server is down",
			},
			{
				name: "should print the trimmed model an untrimmed request body sends",
				file: `{"state":"the server is down","model":" onesie-1.13.0 ","questions":` +
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
	})

	t.Run("should reject --merge with --print-request", func(t *testing.T) {
		t.Parallel()

		const message = "onesie: --merge needs answers to fold in, " +
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
					"--merge-key", "onesie",
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
	})

	t.Run("should reject --assert with --print-request", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		path := filepath.Join(dir, "q.yaml")

		if err := os.WriteFile(path,
			[]byte("assert: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n"), 0o600); err != nil {
			t.Fatalf("writing the question file: %v", err)
		}

		tests := []struct {
			name  string
			args  []string
			stdin string
			want  string
		}{
			{
				name: "should reject a valid --assert with --print-request",
				args: []string{
					"is this urgent", "--print-request", "--state", "x",
					"--assert", "answer.value > 0.99",
				},
				want: "onesie: --assert judges an answer, which --print-request does not produce",
			},
			{
				name: "should reject an unparseable --assert with --print-request",
				args: []string{
					"is this urgent", "--print-request", "--state", "x",
					"--assert", "nonsense syntax here !!",
				},
				want: "onesie: --assert judges an answer, which --print-request does not produce",
			},
			{
				name: "should name the file's key when the file carried the assertion",
				args: []string{"-f", path, "--print-request", "--state", "x"},
				want: "onesie: 'assert' judges an answer, which --print-request does not produce",
			},
			{
				name:  "should reject --stop-on-assert with --print-request in a stream",
				args:  []string{"is this urgent", "--print-request", "-i", "lines", "--stop-on-assert"},
				stdin: "a\nb\n",
				want: "onesie: --stop-on-assert ends a stream on a false assertion, " +
					"which --print-request does not produce",
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

				// Nothing on stdout, which is what tells a rejected dry run from an honoured one.
				if out != "" {
					t.Errorf("stdout should be empty, got:\n%s", out)
				}

				if !strings.Contains(errOut, tc.want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, tc.want)
				}
			})
		}
	})

	providers := []struct {
		name     string
		args     []string
		env      map[string]string
		wantCode int
		want     string
	}{
		{
			name:     "should fail an unknown provider with exit 2 before printing",
			args:     []string{"--provider", "nope", "--ask", "urgent=is this urgent", "--print-request"},
			wantCode: ExitUsage,
		},
		{
			name:     "should fail an unknown ONESIE_PROVIDER with exit 2 before printing",
			args:     []string{"--ask", "urgent=is this urgent", "--print-request"},
			env:      map[string]string{"ONESIE_PROVIDER": "nope"},
			wantCode: ExitUsage,
		},
		{
			name:     "should ignore TYPESAFE_DEFAULT_MODEL under openrouter",
			args:     []string{"--provider", "openrouter", "--ask", "urgent=is this urgent", "--print-request"},
			env:      map[string]string{jev.EnvDefaultModel: "onesie-1.2.0"},
			wantCode: ExitOK,
			want:     `"model":"jev-latest"`,
		},
		{
			name:     "should keep TYPESAFE_DEFAULT_MODEL under typesafe",
			args:     []string{"--ask", "urgent=is this urgent", "--print-request"},
			env:      map[string]string{jev.EnvDefaultModel: "onesie-1.2.0"},
			wantCode: ExitOK,
			want:     `"model":"onesie-1.2.0"`,
		},
	}

	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()}
			for name, value := range tc.env {
				env[name] = value
			}

			var out, errOut bytes.Buffer

			root := NewRootCmd(
				BuildInfo{Version: "1.2.3"},
				WithKeychain(noKeychain()),
				WithStdin(strings.NewReader("the server is down")),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithLookupEnv(lookupFrom(env)),
			)

			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(tc.args)

			if code := Execute(t.Context(), root); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut.String())
			}

			if tc.wantCode != ExitOK && out.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", out.String())
			}

			if !strings.Contains(out.String(), tc.want) {
				t.Errorf("stdout = %q, want it to contain %s", out.String(), tc.want)
			}
		})
	}
}

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
		WithKeychain(noKeychain()),
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

func TestStreamRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		stdin     string
		wantCode  int
		wantLines int
		stdout    []string
		absent    []string
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
		{
			name:      "should send only the mapped body of each jsonl record",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body", "--print-request"},
			stdin:     "{\"customer\":\"c1\",\"body\":\"first\"}\n{\"customer\":\"c2\",\"body\":\"second\"}\n",
			wantCode:  ExitOK,
			wantLines: 2,
			stdout:    []string{`"state":"first"`, `"state":"second"`},
			absent:    []string{"customer"},
		},
		{
			name:      "should send the mapped column of each csv row",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "csv", "--map", ".body", "--print-request"},
			stdin:     "customer,body\nc1,first\nc2,second\n",
			wantCode:  ExitOK,
			wantLines: 2,
			stdout:    []string{`"state":"first"`, `"state":"second"`},
			absent:    []string{"customer"},
		},
		{
			name: "should send each shape a --map expression builds",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request",
				"--map", `{subject, body, id: .ticket.id, last: .messages[-1].text}`,
			},
			stdin: `{"customer":"c1","subject":"down","body":"the site is down",` +
				`"ticket":{"id":12345678901234567890},"messages":[{"text":"hi"},{"text":"bye"}]}` + "\n",
			wantCode:  ExitOK,
			wantLines: 1,
			stdout: []string{
				`"state":{"body":"the site is down","id":12345678901234567890,"last":"bye","subject":"down"}`,
			},
			absent: []string{"customer"},
		},
		{
			name:      "should join fields into one string state",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--map", `.subject + "\n\n" + .body`, "--print-request"},
			stdin:     "{\"subject\":\"down\",\"body\":\"the site is down\"}\n",
			wantCode:  ExitOK,
			wantLines: 1,
			stdout:    []string{`"state":"down\n\nthe site is down"`},
		},
		{
			name:      "should write an error line for a record that maps to nothing usable and carry on",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body", "--print-request"},
			stdin:     "{\"body\":\"first\"}\n{\"customer\":\"c2\"}\n{\"body\":null}\n{\"body\":\"fourth\"}\n",
			wantCode:  ExitRecords,
			wantLines: 4,
			stdout: []string{
				`"state":"first"`,
				`"message":"line 2: --map: state must be a string, object or array, got null"`,
				`"message":"line 3: --map: state must be a string, object or array, got null"`,
				`"state":"fourth"`,
			},
		},
		{
			name:      "should write an error line for a record whose expression yields nothing",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".items[]", "--print-request"},
			stdin:     "{\"items\":[]}\n{\"items\":[\"a\",\"b\"]}\n{\"items\":[\"c\"]}\n",
			wantCode:  ExitRecords,
			wantLines: 3,
			stdout: []string{
				`"message":"line 1: --map yields no value"`,
				`"message":"line 2: --map yields more than one value"`,
				`"state":"c"`,
			},
		},
		{
			name:      "should map each line of -i lines",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "lines", "--map", "ascii_upcase", "--print-request"},
			stdin:     "first\nsecond\n",
			wantCode:  ExitOK,
			wantLines: 2,
			stdout:    []string{`"state":"FIRST"`, `"state":"SECOND"`},
		},
		{
			name:      "should send the mapped column of each tsv row",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "tsv", "--map", ".body", "--print-request"},
			stdin:     "customer\tbody\nc1\tfirst\nc2\tsecond\n",
			wantCode:  ExitOK,
			wantLines: 2,
			stdout:    []string{`"state":"first"`, `"state":"second"`},
			absent:    []string{"customer"},
		},
		{
			name:      "should write an error line for a record that maps to a number or a boolean",
			args:      []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".v", "--print-request"},
			stdin:     "{\"v\":1}\n{\"v\":true}\n{\"v\":\"third\"}\n",
			wantCode:  ExitRecords,
			wantLines: 3,
			stdout: []string{
				`"message":"line 1: --map: state must be a string, object or array, got number"`,
				`"message":"line 2: --map: state must be a string, object or array, got boolean"`,
				`"state":"third"`,
			},
		},
		{
			name: "should write an error line for a record whose result is too large to send",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request",
				"--map", `if .big then "x" * 9000000 else .body end`,
			},
			stdin:     "{\"big\":true}\n{\"body\":\"second\"}\n",
			wantCode:  ExitRecords,
			wantLines: 2,
			stdout: []string{
				`"message":"line 1: --map: result encodes to more than the limit of 8388608 bytes"`,
				`"state":"second"`,
			},
		},
		{
			name: "should write an error line for a record that nests too deep",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request",
				"--map", `if .deep then reduce range(10001) as $i ("x"; [.]) else .body end`,
			},
			stdin:     "{\"deep\":true}\n{\"body\":\"second\"}\n",
			wantCode:  ExitRecords,
			wantLines: 2,
			stdout: []string{
				`{"error":{"kind":"input"`,
				`"message":"line 1: --map: result nests deeper than 10000 levels"`,
				`"state":"second"`,
			},
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

			for _, unwanted := range tc.absent {
				if strings.Contains(out, unwanted) {
					t.Errorf("stdout should not contain %q\ngot:\n%s", unwanted, out)
				}
			}

			if errOut != "" {
				t.Errorf("stderr should be empty, got:\n%s", errOut)
			}
		})
	}
}

func TestRequestLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		state   any
		want    string
		wantErr bool
	}{
		{
			name:  "should encode the request body",
			state: "the site is down",
			want:  `{"state":"the site is down","model":"m","questions":{}}`,
		},
		{
			name:    "should write an error line for a state that cannot be encoded",
			state:   make(chan int),
			want:    `{"error":{"kind":"input","status":null,"message":"line 3: encoding the request: `,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := requestLine(3, jev.Request{State: tc.state, Model: "m"})

			if (err != nil) != tc.wantErr {
				t.Fatalf("requestLine() error = %v, want an error %t", err, tc.wantErr)
			}

			if !strings.HasPrefix(string(got), tc.want) {
				t.Errorf("requestLine() = %s, want it to start with %s", got, tc.want)
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

func TestWritten(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		wantErr bool
	}{
		{name: "should pass a write failure of onesie's own on", err: errors.New("no space"), wantErr: true},
		{
			name: "should swallow a consumer that stopped reading",
			err:  &fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE},
		},
		{name: "should pass a successful write on", err: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := written(tc.err); (got != nil) != tc.wantErr {
				t.Errorf("written(%v) = %v, want an error %t", tc.err, got, tc.wantErr)
			}
		})
	}

	t.Run("should exit 0 when the consumer stops reading", func(t *testing.T) {
		t.Parallel()

		const models = `{"models":[{"name":"jev-latest","description":"alias",` +
			`"release_date":"2026-08-01"}]}`

		tests := []struct {
			name   string
			args   []string
			stdin  string
			server bool
		}{
			{
				name: "should exit 0 when the consumer of --print-questions stops reading",
				args: []string{"--ask", "urgent=is this urgent", "--print-questions"},
			},
			{
				name:  "should exit 0 when the consumer of --print-request stops reading",
				args:  []string{"--ask", "urgent=is this urgent", "--print-request"},
				stdin: "the server is down",
			},
			{
				name:   "should exit 0 when the consumer of --list-models stops reading",
				args:   []string{"--list-models"},
				server: true,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				baseURL := ""

				if tc.server {
					srv := httptest.NewServer(http.HandlerFunc(
						func(w http.ResponseWriter, _ *http.Request) {
							w.Header().Set("Content-Type", "application/json")

							if _, err := io.WriteString(w, models); err != nil {
								t.Errorf("writing the stub response: %v", err)
							}
						}))
					defer srv.Close()

					baseURL = srv.URL
				}

				errOut, code := runClosed(t, tc.args, tc.stdin, baseURL)

				if code != ExitOK {
					t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
				}

				if errOut != "" {
					t.Errorf("stderr should be empty, got:\n%s", errOut)
				}
			})
		}
	})
}

func runClosed(t *testing.T, args []string, stdin, baseURL string) (string, int) {
	t.Helper()

	var errOut bytes.Buffer

	// ONESIE_CONFIG_DIR points at an empty directory for the same reason runOfflineStdin does it: the
	// cases with no base URL build no client, and a credential file in the developer's home would
	// have them build one.
	opts := []RootOption{
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
	}

	if baseURL != "" {
		opts = append(opts, WithClientFactory(
			func(_ context.Context, extra ...jev.Option) (*jev.Client, error) {
				return jev.New(append([]jev.Option{
					jev.WithAPIKey("k"),
					jev.WithBaseURL(baseURL),
					jev.WithEnv(func(string) (string, bool) { return "", false }),
				}, extra...)...)
			}))
	}

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, append(opts, WithKeychain(noKeychain()))...)

	root.SetOut(closedConsumer{})
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return errOut.String(), code
}

type closedConsumer struct{}

func (closedConsumer) Write([]byte) (int, error) {
	return 0, syscall.EPIPE
}

func runOffline(t *testing.T, args []string) (string, string, int) {
	t.Helper()

	return runOfflineStdin(t, args, "")
}

func runOfflineStdin(t *testing.T, args []string, stdin string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	// No client factory, so the real factory runs on a machine with no key, and a case that exits
	// ok reached no network.
	//
	// ONESIE_CONFIG_DIR points at an empty directory, because section 16.1's third source would
	// otherwise read the developer's own key into these tests.
	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithKeychain(noKeychain()),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}
