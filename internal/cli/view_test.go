package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestValidateRun(t *testing.T) {
	t.Parallel()

	t.Run("should return no calibration arguments when validation fails", func(t *testing.T) {
		t.Parallel()
		config := runConfig{merged: &setting.Merged{}, calibrate: &calibrateFlags{}}
		args, _, _, err := validateRun(config, runFacts{}, nil, commandLine{})
		if err == nil {
			t.Fatal("expected a refusal")
		}
		if args.Cuts != nil || args.Requirements != nil {
			t.Fatalf("arguments = %v", args)
		}
	})

	t.Run("should refuse each flag -i request does not take through a run", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			args    []string
			wantErr string
		}{
			{
				name:    "should reject a positional question with -i request",
				args:    []string{"-i", "request", "is this urgent"},
				wantErr: "onesie: the question argument does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --ask with -i request",
				args:    []string{"-i", "request", "--ask", "urgent=is this urgent"},
				wantErr: "onesie: --ask does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject -f with -i request before it is read",
				args:    []string{"-i", "request", "-f", "nowhere.yaml"},
				wantErr: "onesie: -f does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --replace with -i request",
				args:    []string{"-i", "request", "--replace"},
				wantErr: "onesie: --replace does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --pick with -i request",
				args:    []string{"-i", "request", "--pick", "a,b"},
				wantErr: "onesie: --pick does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --rate with -i request",
				args:    []string{"-i", "request", "--rate", "low,high"},
				wantErr: "onesie: --rate does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --desc with -i request",
				args:    []string{"-i", "request", "--desc", "a=first"},
				wantErr: "onesie: --desc does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --sep with -i request",
				args:    []string{"-i", "request", "--sep", ";"},
				wantErr: "onesie: --sep does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --threshold with -i request",
				args:    []string{"-i", "request", "--threshold", "0.5"},
				wantErr: "onesie: --threshold does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --min-confidence with -i request",
				args:    []string{"-i", "request", "--min-confidence", "0.5"},
				wantErr: "onesie: --min-confidence does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --fallback with -i request",
				args:    []string{"-i", "request", "--fallback", "maybe"},
				wantErr: "onesie: --fallback does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --state with -i request",
				args:    []string{"-i", "request", "--state", "x"},
				wantErr: "onesie: --state does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --state-file with -i request",
				args:    []string{"-i", "request", "--state-file", "x"},
				wantErr: "onesie: --state-file does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject -o with -i request",
				args:    []string{"-i", "request", "-o", "json"},
				wantErr: "onesie: -o does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject -q with -i request",
				args:    []string{"-i", "request", "-q"},
				wantErr: "onesie: -q does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --assert with -i request",
				args:    []string{"-i", "request", "--assert", "answer.value > 0.5"},
				wantErr: "onesie: --assert does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject an unparseable --assert with -i request",
				args:    []string{"-i", "request", "--assert", "nonsense syntax here !!"},
				wantErr: "onesie: --assert does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --usage with -i request",
				args:    []string{"-i", "request", "--usage"},
				wantErr: "onesie: --usage does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --merge with -i request",
				args:    []string{"-i", "request", "--merge"},
				wantErr: "onesie: --merge does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should name --merge-key when that is the flag given",
				args:    []string{"-i", "request", "--merge-key", "verdict"},
				wantErr: "onesie: --merge-key does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject -m with -i request",
				args:    []string{"-i", "request", "-m", "onesie-1.13.0"},
				wantErr: "onesie: -m does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --stop-on=fail with -i request",
				args:    []string{"-i", "request", "--stop-on=fail"},
				wantErr: "onesie: --stop-on=fail does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should reject --print-questions with -i request",
				args:    []string{"-i", "request", "--print-questions"},
				wantErr: "onesie: --print-questions does not apply to -i request, which sends each body as written and prints each raw response",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, code := runOfflineStdin(t, tc.args, `{"state":"x"}`+"\n")

				if code != ExitUsage {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitUsage, out, errOut)
				}

				if strings.TrimSpace(errOut) != tc.wantErr {
					t.Errorf("stderr = %q, want %q", strings.TrimSpace(errOut), tc.wantErr)
				}

				if out != "" {
					t.Errorf("stdout should be empty, got:\n%s", out)
				}
			})
		}
	})

	t.Run("should print a flag warning once", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			args []string
			want string
		}{
			{
				name: "should print a flag warning once for a mode that builds a plan",
				args: []string{"--ask", "team=which team", "--pick", "a,b", "--desc", "a=first"},
				want: "'team' describes a but not b",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				_, out, errOut, code := runRequestMode(
					t, tc.args, "hello\n", 0,
					`{"model":"m","answers":{"team":{"type":"choice","choice":"a","confidence":0.9,`+
						`"probabilities":{"a":0.9,"b":0.1}}}}`)

				if code != ExitOK {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitOK, out, errOut)
				}

				// Validation has two callers, and a run that reaches both prints the warning once.
				if got := strings.Count(errOut, tc.want); got != 1 {
					t.Errorf("warning printed %d times, want 1\nstderr:\n%s", got, errOut)
				}
			})
		}
	})

	t.Run("should reject every question flag with --list-models", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			args    []string
			wantErr string
		}{
			{
				name:    "should reject a positional question with --list-models",
				args:    []string{"--list-models", "is this urgent"},
				wantErr: "onesie: the question argument does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --ask with --list-models",
				args:    []string{"--list-models", "--ask", "urgent=is this urgent"},
				wantErr: "onesie: --ask does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --pick with --list-models",
				args:    []string{"--list-models", "--pick", "a,b"},
				wantErr: "onesie: --pick does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --fallback with --list-models",
				args:    []string{"--list-models", "--fallback", "maybe"},
				wantErr: "onesie: --fallback does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject -f with --list-models before it is read",
				args:    []string{"--list-models", "-f", "nowhere.yaml"},
				wantErr: "onesie: -f does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --replace with --list-models",
				args:    []string{"--list-models", "--replace"},
				wantErr: "onesie: --replace does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --state with --list-models",
				args:    []string{"--list-models", "--state", "x"},
				wantErr: "onesie: --state does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --state-file with --list-models",
				args:    []string{"--list-models", "--state-file", "x"},
				wantErr: "onesie: --state-file does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				// --input's default is text, so a rejection here can only come from Changed, not from
				// the value differing from the default.
				name:    "should reject --input given by its long name with --list-models",
				args:    []string{"--list-models", "--input", "text"},
				wantErr: "onesie: -i does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				// --jobs's default is 1, for the same reason as the --input case above.
				name:    "should reject --jobs given by its long name with --list-models",
				args:    []string{"--list-models", "--jobs", "1"},
				wantErr: "onesie: -j does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --model given by its long name with --list-models",
				args:    []string{"--list-models", "--model", "x"},
				wantErr: "onesie: -m does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject -i with --list-models",
				args:    []string{"--list-models", "-i", "jsonl"},
				wantErr: "onesie: -i does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject -i request with --list-models by name",
				args:    []string{"--list-models", "-i", "request"},
				wantErr: "onesie: -i does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name: "should blame --list-models rather than -i request for a shared offender",
				args: []string{
					"--list-models", "-i", "request", "--ask", "urgent=is this urgent",
				},
				wantErr: "onesie: --ask does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject -o with --list-models",
				args:    []string{"--list-models", "-o", "json"},
				wantErr: "onesie: -o does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject -q with --list-models",
				args:    []string{"--list-models", "-q"},
				wantErr: "onesie: -q does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --assert with --list-models",
				args:    []string{"--list-models", "--assert", "answer.value > 0.5"},
				wantErr: "onesie: --assert does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject an unparseable --assert with --list-models",
				args:    []string{"--list-models", "--assert", "nonsense syntax here !!"},
				wantErr: "onesie: --assert does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --usage with --list-models",
				args:    []string{"--list-models", "--usage"},
				wantErr: "onesie: --usage does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --merge with --list-models",
				args:    []string{"--list-models", "--merge"},
				wantErr: "onesie: --merge does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should name --merge-key when that is the merge flag given",
				args:    []string{"--list-models", "--merge-key", "out"},
				wantErr: "onesie: --merge-key does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject -j with --list-models",
				args:    []string{"--list-models", "-j", "4"},
				wantErr: "onesie: -j does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject -m with --list-models",
				args:    []string{"--list-models", "-m", "onesie-1.13.0"},
				wantErr: "onesie: -m does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --unordered with --list-models",
				args:    []string{"--list-models", "--unordered"},
				wantErr: "onesie: --unordered does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --stop-on=error with --list-models",
				args:    []string{"--list-models", "--stop-on=error"},
				wantErr: "onesie: --stop-on does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --stop-on=fail with --list-models",
				args:    []string{"--list-models", "--stop-on=fail"},
				wantErr: "onesie: --stop-on does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --skip-blank with --list-models",
				args:    []string{"--list-models", "--skip-blank"},
				wantErr: "onesie: --skip-blank does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --print-request with --list-models",
				args:    []string{"--list-models", "--print-request"},
				wantErr: "onesie: --print-request does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should reject --print-questions with --list-models",
				args:    []string{"--list-models", "--print-questions"},
				wantErr: "onesie: --print-questions does not apply to --list-models, which only lists the models a provider serves",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, code := runOfflineStdin(t, tc.args, `{"state":"x"}`+"\n")

				if code != ExitUsage {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitUsage, out, errOut)
				}

				if strings.TrimSpace(errOut) != tc.wantErr {
					t.Errorf("stderr = %q, want %q", strings.TrimSpace(errOut), tc.wantErr)
				}

				if out != "" {
					t.Errorf("stdout should be empty, got:\n%s", out)
				}
			})
		}
	})

	t.Run("should reject --stats with a print flag", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			args []string
			want string
		}{
			{
				name: "should reject stats with print-request",
				args: []string{"--ask", "urgent=is this urgent", "--print-request", "--stats"},
				want: "onesie: --stats does not apply to --print-request, which writes the request bodies and sends none",
			},
			{
				name: "should reject stats with print-questions",
				args: []string{"--ask", "urgent=is this urgent", "--print-questions", "--stats"},
				want: "onesie: --stats does not apply to --print-questions, which reads no input and writes the questions as a question file",
			},
			{
				// The path that returns before a plan is built, so the rule has to sit above it.
				name: "should reject stats with print-request under a request mode stream",
				args: []string{"-i", "request", "--print-request", "--stats"},
				want: "onesie: --stats does not apply to --print-request, which writes the request bodies and sends none",
			},
			{
				name: "should reject both print flags at once",
				args: []string{
					"--ask", "urgent=is this urgent", "--print-request", "--print-questions",
				},
				want: "onesie: --print-questions does not apply to --print-request, which writes the request bodies and sends none",
			},
			{
				name: "should reject an output mode with print-questions",
				args: []string{
					"--ask", "urgent=is this urgent", "--print-questions", "-o", "json",
				},
				want: "onesie: -o does not apply to --print-questions, which reads no input and writes the questions as a question file",
			},
			{
				name: "should reject an output mode with print-request",
				args: []string{"--ask", "urgent=is this urgent", "--print-request", "-o", "json"},
				want: "onesie: -o does not apply to --print-request, which writes the request bodies and sends none",
			},
			{
				name: "should reject quiet with print-questions",
				args: []string{"--ask", "urgent=is this urgent", "--print-questions", "-q"},
				want: "onesie: -q does not apply to --print-questions, which reads no input and writes the questions as a question file",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, code := runOffline(t, tc.args)

				if code != ExitUsage {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitUsage, out, errOut)
				}

				// Rejected before anything reaches stdout, which is the reason the rule lives in
				// the validation stage rather than in the print path.
				if out != "" {
					t.Errorf("stdout should be empty, got:\n%s", out)
				}

				if !strings.Contains(errOut, tc.want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, tc.want)
				}
			})
		}
	})

	t.Run("should name an -o alias as it was typed when -q refuses it", func(t *testing.T) {
		t.Parallel()

		out, errOut, code := runOffline(t, []string{"is this urgent", "-q", "-o", "md"})
		if code != ExitUsage {
			t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitUsage, out, errOut)
		}

		if want := "onesie: -q suppresses output, which leaves -o md nothing to write"; strings.TrimSpace(errOut) != want {
			t.Errorf("stderr = %q, want %q", strings.TrimSpace(errOut), want)
		}
	})

	t.Run("should leave a file's lone abstain_if alone under --print-request, which ignores the file's gates", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "abstain.yaml")
		if err := os.WriteFile(path, []byte("abstain_if: urgent.value < 0.2\nurgent:\n  ask: is this urgent\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		out, errOut, code := runOfflineStdin(t, []string{"--print-request", "-f", path}, "hello\n")
		if code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitOK, out, errOut)
		}

		if !strings.Contains(out, "is this urgent") {
			t.Errorf("stdout should hold the request body, got:\n%s", out)
		}
	})

	t.Run("should report in the order of the one stage", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		body := filepath.Join(dir, "body.json")
		broken := filepath.Join(dir, "broken.yaml")
		missing := filepath.Join(dir, "missing.yaml")

		for path, content := range map[string]string{
			body:   `{"questions":{"urgent":{"type":"noul","instructions":"is this urgent"}},"state":"hello"}`,
			broken: "urgent: [unclosed\n",
		} {
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		bodyClash := "onesie: --state and the state in " + body + " both give a state. Drop one"

		tests := []struct {
			name    string
			args    []string
			wantErr string
		}{
			{
				name:    "should refuse a body's second state before parsing --assert",
				args:    []string{"-f", body, "--state", "x", "--assert", "urgent.value >"},
				wantErr: bodyClash,
			},
			{
				name:    "should refuse a body's second state before parsing --abstain-if",
				args:    []string{"-f", body, "--state", "x", "--assert", "urgent.value > 0.5", "--abstain-if", "("},
				wantErr: bodyClash,
			},
			{
				name:    "should refuse a body's second state before compiling --map",
				args:    []string{"-f", body, "--state", "x", "--map", ".[", "--print-request"},
				wantErr: bodyClash,
			},
			{
				name:    "should refuse a body's second state before reading --image",
				args:    []string{"-f", body, "--image", "photo=" + filepath.Join(dir, "missing.png"), "--print-request"},
				wantErr: "onesie: --image and the state in " + body + " both give a state. Drop one",
			},
			{
				name:    "should report a missing -f file before calibrate's needs",
				args:    []string{"calibrate", "-f", missing},
				wantErr: "onesie: reading " + missing + ": " + missingFileText(t, missing),
			},
			{
				name:    "should report a broken -f file before calibrate's needs",
				args:    []string{"calibrate", "-f", broken, "--offline"},
				wantErr: "onesie: " + broken + ": [1:9] sequence end token ']' not found",
			},
			{
				name:    "should report a malformed --ask before calibrate's needs",
				args:    []string{"calibrate", "--ask", "noequals", "--cuts", " "},
				wantErr: "onesie: --ask takes NAME=QUESTION, got 'noequals'",
			},
			{
				name:    "should still report calibrate's needs before a missing question",
				args:    []string{"calibrate", "--cuts", "0.5"},
				wantErr: "onesie: calibrate needs -i jsonl, csv or tsv, since only a record has fields to label",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, code := runOffline(t, tc.args)

				if code != ExitUsage {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitUsage, out, errOut)
				}

				if first, _, _ := strings.Cut(errOut, "\n"); first != tc.wantErr {
					t.Errorf("stderr first line = %q, want %q", first, tc.wantErr)
				}
			})
		}
	})
}

func missingFileText(t *testing.T, path string) string {
	t.Helper()

	_, err := os.Open(path)

	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("opening %s gave %v, want a path error", path, err)
	}

	return pathErr.Err.Error()
}
