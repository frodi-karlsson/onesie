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
				name:    "should refuse a positional question with -i request",
				args:    []string{"-i", "request", "is this urgent"},
				wantErr: "onesie: the question argument does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should refuse -f with -i request before it is read",
				args:    []string{"-i", "request", "-f", "nowhere.yaml"},
				wantErr: "onesie: -f does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should refuse an unparseable --assert with -i request",
				args:    []string{"-i", "request", "--assert", "nonsense syntax here !!"},
				wantErr: "onesie: --assert does not apply to -i request, which sends each body as written and prints each raw response",
			},
			{
				name:    "should name --merge-key when that is the flag given",
				args:    []string{"-i", "request", "--merge-key", "verdict"},
				wantErr: "onesie: --merge-key does not apply to -i request, which sends each body as written and prints each raw response",
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

	t.Run("should refuse every question flag with --list-models", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			args    []string
			wantErr string
		}{
			{
				name:    "should refuse a positional question with --list-models",
				args:    []string{"--list-models", "is this urgent"},
				wantErr: "onesie: the question argument does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should refuse -f with --list-models before it is read",
				args:    []string{"--list-models", "-f", "nowhere.yaml"},
				wantErr: "onesie: -f does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				// --input's default is text, so a refusal here can only come from Changed, not from
				// the value differing from the default.
				name:    "should refuse --input given by its long name with --list-models",
				args:    []string{"--list-models", "--input", "text"},
				wantErr: "onesie: -i does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should refuse -i request with --list-models by name",
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
				name:    "should refuse an unparseable --assert with --list-models",
				args:    []string{"--list-models", "--assert", "nonsense syntax here !!"},
				wantErr: "onesie: --assert does not apply to --list-models, which only lists the models a provider serves",
			},
			{
				name:    "should name --merge-key when that is the merge flag given",
				args:    []string{"--list-models", "--merge-key", "out"},
				wantErr: "onesie: --merge-key does not apply to --list-models, which only lists the models a provider serves",
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

	t.Run("should refuse --stats with a print flag", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			args []string
			want string
		}{
			{
				// The path that returns before a plan is built, so the rule has to sit above it.
				name: "should refuse stats with print-request under a request mode stream",
				args: []string{"-i", "request", "--print-request", "--stats"},
				want: "onesie: --stats does not apply to --print-request, which writes the request bodies and sends none",
			},
			{
				name: "should refuse both print flags at once",
				args: []string{
					"--ask", "urgent=is this urgent", "--print-request", "--print-questions",
				},
				want: "onesie: --print-questions does not apply to --print-request, which writes the request bodies and sends none",
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
