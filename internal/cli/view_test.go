package cli

import (
	"strings"
	"testing"

	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestValidateRun(t *testing.T) {
	t.Parallel()

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
				wantErr: "onesie: -i request carries its own questions. Drop the question argument",
			},
			{
				name:    "should reject --ask with -i request",
				args:    []string{"-i", "request", "--ask", "urgent=is this urgent"},
				wantErr: "onesie: -i request carries its own questions. Drop --ask",
			},
			{
				name: "should reject -f with -i request before it is read",
				args: []string{"-i", "request", "-f", "nowhere.yaml"},
				wantErr: "onesie: -f does not apply to -i request, " +
					"which carries its own questions",
			},
			{
				name:    "should reject --replace with -i request",
				args:    []string{"-i", "request", "--replace"},
				wantErr: "onesie: --replace applies to -f, which -i request does not accept",
			},
			{
				name:    "should reject --pick with -i request",
				args:    []string{"-i", "request", "--pick", "a,b"},
				wantErr: "onesie: -i request carries its own questions. Drop --pick",
			},
			{
				name:    "should reject --rate with -i request",
				args:    []string{"-i", "request", "--rate", "low,high"},
				wantErr: "onesie: -i request carries its own questions. Drop --rate",
			},
			{
				name:    "should reject --desc with -i request",
				args:    []string{"-i", "request", "--desc", "a=first"},
				wantErr: "onesie: -i request carries its own questions. Drop --desc",
			},
			{
				name:    "should reject --sep with -i request",
				args:    []string{"-i", "request", "--sep", ";"},
				wantErr: "onesie: -i request carries its own questions. Drop --sep",
			},
			{
				name: "should reject --threshold with -i request",
				args: []string{"-i", "request", "--threshold", "0.5"},
				wantErr: "onesie: --threshold does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name: "should reject --min-confidence with -i request",
				args: []string{"-i", "request", "--min-confidence", "0.5"},
				wantErr: "onesie: --min-confidence does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name: "should reject --fallback with -i request",
				args: []string{"-i", "request", "--fallback", "maybe"},
				wantErr: "onesie: --fallback does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name: "should reject --state with -i request",
				args: []string{"-i", "request", "--state", "x"},
				wantErr: "onesie: --state does not apply to -i request, " +
					"whose bodies carry their own state",
			},
			{
				name: "should reject --state-file with -i request",
				args: []string{"-i", "request", "--state-file", "x"},
				wantErr: "onesie: --state-file does not apply to -i request, " +
					"whose bodies carry their own state",
			},
			{
				name:    "should reject -o with -i request",
				args:    []string{"-i", "request", "-o", "json"},
				wantErr: "onesie: -o does not apply to -i request, which forwards raw responses",
			},
			{
				name:    "should reject -r with -i request",
				args:    []string{"-i", "request", "-r"},
				wantErr: "onesie: -r does not apply to -i request, which forwards raw responses",
			},
			{
				name:    "should reject -q with -i request",
				args:    []string{"-i", "request", "-q"},
				wantErr: "onesie: -q needs a policy to report, which -i request has none of",
			},
			{
				name: "should reject --assert with -i request",
				args: []string{"-i", "request", "--assert", "answer.value > 0.5"},
				wantErr: "onesie: --assert does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name: "should reject an unparseable --assert with -i request",
				args: []string{"-i", "request", "--assert", "nonsense syntax here !!"},
				wantErr: "onesie: --assert does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name: "should reject --usage with -i request",
				args: []string{"-i", "request", "--usage"},
				wantErr: "onesie: --usage does not apply to -i request, " +
					"whose response bodies already carry usage",
			},
			{
				name:    "should reject --merge with -i request",
				args:    []string{"-i", "request", "--merge"},
				wantErr: "onesie: --merge does not apply to -i request, which forwards raw responses",
			},
			{
				name: "should name --merge-key when that is the flag given",
				args: []string{"-i", "request", "--merge-key", "verdict"},
				wantErr: "onesie: --merge-key does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name: "should reject -m with -i request",
				args: []string{"-i", "request", "-m", "onesie-1.13.0"},
				wantErr: "onesie: -m does not apply to -i request, " +
					"whose bodies carry their own model",
			},
			{
				name: "should reject --stop-on-assert with -i request",
				args: []string{"-i", "request", "--stop-on-assert"},
				wantErr: "onesie: --stop-on-assert does not apply to -i request, " +
					"whose bodies carry no assertion",
			},
			{
				name: "should reject --print-questions with -i request",
				args: []string{"-i", "request", "--print-questions"},
				wantErr: "onesie: --print-questions needs questions of its own, " +
					"which -i request does not build",
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
				args: []string{"is this urgent", "-j", "4"},
				want: "-j 4 ignored. -i text reads one record",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				_, out, errOut, code := runRequestMode(
					t, tc.args, "hello\n", 0,
					`{"model":"m","answers":{"answer":{"type":"noul","noul":0.9}}}`)

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
				wantErr: "onesie: --list-models asks no question. Drop the question argument",
			},
			{
				name:    "should reject --ask with --list-models",
				args:    []string{"--list-models", "--ask", "urgent=is this urgent"},
				wantErr: "onesie: --list-models asks no question. Drop --ask",
			},
			{
				name:    "should reject --pick with --list-models",
				args:    []string{"--list-models", "--pick", "a,b"},
				wantErr: "onesie: --list-models asks no question. Drop --pick",
			},
			{
				name:    "should reject --fallback with --list-models",
				args:    []string{"--list-models", "--fallback", "maybe"},
				wantErr: "onesie: --list-models asks no question. Drop --fallback",
			},
			{
				name:    "should reject -f with --list-models before it is read",
				args:    []string{"--list-models", "-f", "nowhere.yaml"},
				wantErr: "onesie: -f does not apply to --list-models, which asks no question",
			},
			{
				name:    "should reject --replace with --list-models",
				args:    []string{"--list-models", "--replace"},
				wantErr: "onesie: --replace applies to -f, which --list-models does not accept",
			},
			{
				name:    "should reject --state with --list-models",
				args:    []string{"--list-models", "--state", "x"},
				wantErr: "onesie: --state does not apply to --list-models, which reads no state",
			},
			{
				name:    "should reject --state-file with --list-models",
				args:    []string{"--list-models", "--state-file", "x"},
				wantErr: "onesie: --state-file does not apply to --list-models, which reads no state",
			},
			{
				// --input's default is text, so a rejection here can only come from Changed, not from
				// the value differing from the default.
				name:    "should reject --input given by its long name with --list-models",
				args:    []string{"--list-models", "--input", "text"},
				wantErr: "onesie: -i does not apply to --list-models, which reads no input",
			},
			{
				// --jobs's default is 1, for the same reason as the --input case above.
				name:    "should reject --jobs given by its long name with --list-models",
				args:    []string{"--list-models", "--jobs", "1"},
				wantErr: "onesie: -j does not apply to --list-models, which makes one request",
			},
			{
				name:    "should reject --model given by its long name with --list-models",
				args:    []string{"--list-models", "--model", "x"},
				wantErr: "onesie: -m names a model to ask, which --list-models does not do",
			},
			{
				name:    "should reject -i with --list-models",
				args:    []string{"--list-models", "-i", "jsonl"},
				wantErr: "onesie: -i does not apply to --list-models, which reads no input",
			},
			{
				name:    "should reject -i request with --list-models by name",
				args:    []string{"--list-models", "-i", "request"},
				wantErr: "onesie: -i does not apply to --list-models, which reads no input",
			},
			{
				name: "should blame --list-models rather than -i request for a shared offender",
				args: []string{
					"--list-models", "-i", "request", "--ask", "urgent=is this urgent",
				},
				wantErr: "onesie: --list-models asks no question. Drop --ask",
			},
			{
				name:    "should reject -o with --list-models",
				args:    []string{"--list-models", "-o", "json"},
				wantErr: "onesie: -o does not apply to --list-models, which writes a fixed listing",
			},
			{
				// Its own rule rather than the -o one, since run leaves Output empty for a bare -r.
				name:    "should reject -r with --list-models",
				args:    []string{"--list-models", "-r"},
				wantErr: "onesie: -r does not apply to --list-models, which writes a fixed listing",
			},
			{
				name:    "should reject -q with --list-models",
				args:    []string{"--list-models", "-q"},
				wantErr: "onesie: -q suppresses output, which leaves --list-models nothing to write",
			},
			{
				name:    "should reject --assert with --list-models",
				args:    []string{"--list-models", "--assert", "answer.value > 0.5"},
				wantErr: "onesie: --assert judges an answer, which --list-models does not produce",
			},
			{
				name:    "should reject an unparseable --assert with --list-models",
				args:    []string{"--list-models", "--assert", "nonsense syntax here !!"},
				wantErr: "onesie: --assert judges an answer, which --list-models does not produce",
			},
			{
				name: "should reject --usage with --list-models",
				args: []string{"--list-models", "--usage"},
				wantErr: "onesie: --usage reports the tokens a question cost, " +
					"which --list-models does not ask",
			},
			{
				name: "should reject --merge with --list-models",
				args: []string{"--list-models", "--merge"},
				wantErr: "onesie: --merge needs answers to fold in, " +
					"which --list-models does not produce",
			},
			{
				name: "should name --merge-key when that is the merge flag given",
				args: []string{"--list-models", "--merge-key", "out"},
				wantErr: "onesie: --merge-key needs answers to fold in, " +
					"which --list-models does not produce",
			},
			{
				name:    "should reject -j with --list-models",
				args:    []string{"--list-models", "-j", "4"},
				wantErr: "onesie: -j does not apply to --list-models, which makes one request",
			},
			{
				name:    "should reject -m with --list-models",
				args:    []string{"--list-models", "-m", "onesie-1.13.0"},
				wantErr: "onesie: -m names a model to ask, which --list-models does not do",
			},
			{
				name: "should reject --unordered with --list-models",
				args: []string{"--list-models", "--unordered"},
				wantErr: "onesie: --unordered applies to streaming input, " +
					"which --list-models does not read",
			},
			{
				name: "should reject --stop-on-error with --list-models",
				args: []string{"--list-models", "--stop-on-error"},
				wantErr: "onesie: --stop-on-error applies to streaming input, " +
					"which --list-models does not read",
			},
			{
				name: "should reject --stop-on-assert with --list-models",
				args: []string{"--list-models", "--stop-on-assert"},
				wantErr: "onesie: --stop-on-assert applies to streaming input, " +
					"which --list-models does not read",
			},
			{
				name: "should reject --skip-blank with --list-models",
				args: []string{"--list-models", "--skip-blank"},
				wantErr: "onesie: --skip-blank applies to streaming input, " +
					"which --list-models does not read",
			},
			{
				name: "should reject --print-request with --list-models",
				args: []string{"--list-models", "--print-request"},
				wantErr: "onesie: --print-request and --list-models each write a different thing " +
					"to stdout. Pass one",
			},
			{
				name: "should reject --print-questions with --list-models",
				args: []string{"--list-models", "--print-questions"},
				wantErr: "onesie: --print-questions and --list-models each write a different thing " +
					"to stdout. Pass one",
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
				want: "onesie: --stats has nothing to report with --print-request, " +
					"which makes no request",
			},
			{
				name: "should reject stats with print-questions",
				args: []string{"--ask", "urgent=is this urgent", "--print-questions", "--stats"},
				want: "onesie: --stats has nothing to report with --print-questions, " +
					"which makes no request",
			},
			{
				// The path that returns before a plan is built, so the rule has to sit above it.
				name: "should reject stats with print-request under a request mode stream",
				args: []string{"-i", "request", "--print-request", "--stats"},
				want: "onesie: --stats has nothing to report with --print-request, " +
					"which makes no request",
			},
			{
				name: "should reject both print flags at once",
				args: []string{
					"--ask", "urgent=is this urgent", "--print-request", "--print-questions",
				},
				want: "onesie: --print-request and --print-questions each write a different thing " +
					"to stdout. Pass one",
			},
			{
				name: "should reject an output mode with print-questions",
				args: []string{
					"--ask", "urgent=is this urgent", "--print-questions", "-o", "json",
				},
				want: "onesie: -o does not apply to --print-questions, which writes a question file",
			},
			{
				name: "should reject an output mode with print-request",
				args: []string{"--ask", "urgent=is this urgent", "--print-request", "-o", "json"},
				want: "onesie: -o does not apply to --print-request, which writes a request body",
			},
			{
				name: "should reject the raw shorthand with print-request",
				args: []string{"--ask", "urgent=is this urgent", "--print-request", "-r"},
				want: "onesie: -r does not apply to --print-request, which writes a request body",
			},
			{
				name: "should reject quiet with print-questions",
				args: []string{"--ask", "urgent=is this urgent", "--print-questions", "-q"},
				want: "onesie: -q suppresses output, which leaves --print-questions nothing to write",
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
}

func TestViewOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		config     runConfig
		setting    setting.Name
		wantSource setting.Source
	}{
		{
			name:       "should tag a typed flag as given on the command line",
			config:     runConfig{hasState: true},
			setting:    setting.State.Name(),
			wantSource: setting.SourceFlag,
		},
		{
			name:       "should tag a flag left out as a default",
			config:     runConfig{},
			setting:    setting.State.Name(),
			wantSource: setting.SourceDefault,
		},
		{
			name:       "should tag --mock as given on the command line",
			config:     runConfig{mockPath: "answers.jsonl", mockSpelled: flagMock},
			setting:    setting.Mock.Name(),
			wantSource: setting.SourceFlag,
		},
		{
			name:       "should tag ONESIE_MOCK as the environment",
			config:     runConfig{mockPath: "answers.jsonl", mockSpelled: envMock},
			setting:    setting.Mock.Name(),
			wantSource: setting.SourceEnv,
		},
		{
			name:       "should tag a repeated flag given once as given on the command line",
			config:     runConfig{runFlags: runFlags{assert: []string{"answer.value > 0.5"}}},
			setting:    "assert",
			wantSource: setting.SourceFlag,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			view := viewOf(tc.config, runFacts{}, nil)

			if got := view.Merged.Source(tc.setting); got != tc.wantSource {
				t.Errorf("Source(%s) = %d, want %d", tc.setting, got, tc.wantSource)
			}
		})
	}
}
