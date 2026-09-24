package skilleval

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/skillcheck"
)

func TestGraderGrade(t *testing.T) {
	t.Parallel()

	result := `{"cases":[{"name":"gate","arms":{
		"without":[{"tracePath":"/t/without-1.jsonl"}],
		"with":[{"tracePath":"/t/with-1.jsonl"},{"tracePath":"/t/gone.jsonl"}]}}]}`

	traceOf := func(command string) string {
		return `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":` +
			command + `}}]}}` + "\n"
	}

	files := map[string]string{
		"/r/aggregate-result.json": result,
		"/t/with-1.jsonl":          traceOf(`"jev 'is this safe' -q --state x && jev auth clear"`),
		"/t/without-1.jsonl":       traceOf(`"jev 'is this safe' --bogus; jev 'a' | jq ."`),
	}

	runner := fakeRunner(func(command string) skillcheck.DryRunResult {
		switch command {
		case "jev 'is this safe' -q --state x", "jev 'a'":
			return skillcheck.DryRunResult{ExitCode: 0}
		default:
			return skillcheck.DryRunResult{ExitCode: 2, Stderr: "jev: unknown flag: --bogus"}
		}
	})

	grader := newTestGrader(runner, files)

	report, err := grader.Grade(context.Background(), "/r/aggregate-result.json")
	if err != nil {
		t.Fatalf("Grade(...) error = %v", err)
	}

	arms := report.Cases[0].Arms

	t.Run("should put the plugin arm first", func(t *testing.T) {
		t.Parallel()

		if arms[0].Arm != "with" || arms[1].Arm != "without" {
			t.Errorf("arms = %s, %s, want with, without", arms[0].Arm, arms[1].Arm)
		}
	})

	t.Run("should count a run whose trace is gone rather than failing it", func(t *testing.T) {
		t.Parallel()

		if arms[0].Runs != 2 || arms[0].MissingTraces != 1 {
			t.Errorf("with arm = %d runs, %d missing, want 2 and 1", arms[0].Runs, arms[0].MissingTraces)
		}
	})

	t.Run("should skip a subcommand without dry running it", func(t *testing.T) {
		t.Parallel()

		if arms[0].Commands != 2 || arms[0].Clean != 1 || len(arms[0].Skipped) != 1 {
			t.Fatalf("with arm = %+v, want 2 commands, 1 clean, 1 skipped", arms[0])
		}

		if got := arms[0].Skipped[0].Detail; got != "runs the auth subcommand" {
			t.Errorf("skip detail = %q, want runs the auth subcommand", got)
		}
	})

	t.Run("should record a failed dry run with its exit code and stderr", func(t *testing.T) {
		t.Parallel()

		if len(arms[1].Failed) != 1 || arms[1].Clean != 1 {
			t.Fatalf("without arm = %+v, want 1 failed and 1 clean", arms[1])
		}

		if got := arms[1].Failed[0].Detail; got != "exit 2: jev: unknown flag: --bogus" {
			t.Errorf("failure detail = %q", got)
		}
	})

	errorTests := []struct {
		name    string
		files   map[string]string
		readErr error
	}{
		{name: "should fail when the result cannot be read", files: map[string]string{}},
		{name: "should fail when the result is not JSON", files: map[string]string{"/r.json": "nope"}},
		{
			name:    "should fail when a trace exists but cannot be read",
			files:   map[string]string{"/r.json": `{"cases":[{"name":"c","arms":{"with":[{"tracePath":"/t.jsonl"}]}}]}`},
			readErr: fs.ErrPermission,
		},
	}

	for _, tc := range errorTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			grader := newTestGrader(fakeRunner(nil), tc.files)
			grader.readFile = func(name string) ([]byte, error) {
				if body, ok := tc.files[name]; ok {
					return []byte(body), nil
				}

				if tc.readErr != nil {
					return nil, tc.readErr
				}

				return nil, errors.New("missing")
			}

			if _, err := grader.Grade(context.Background(), "/r.json"); err == nil {
				t.Errorf("Grade(...) error = nil, want an error")
			}
		})
	}

	commandTests := []struct {
		name        string
		script      string
		wantSkip    string
		wantQuiet   bool
		wantCommand string
	}{
		{
			name:   "should dry run a quoted question that starts with a subcommand name",
			script: `jev "help me decide if this is urgent"`,
		},
		{
			name:     "should skip a subcommand that follows a value flag",
			script:   "jev -m x auth clear",
			wantSkip: "runs the auth subcommand",
		},
		{
			name:     "should skip --state-file with a variable value",
			script:   `jev 'is this urgent' --state-file "$f"`,
			wantSkip: "reads a file through --state-file",
		},
		{
			name:     "should skip -f with a literal value",
			script:   "jev -f questions.json",
			wantSkip: "reads a file through --file",
		},
		{
			name:     "should skip -f at the end of a short flag cluster",
			script:   "jev -qf questions.json",
			wantSkip: "reads a file through --file",
		},
		{
			name:     "should skip --file joined to its value",
			script:   "jev --file=questions.json",
			wantSkip: "reads a file through --file",
		},
		{
			name:   "should not read a value that spells a flag as the flag",
			script: "jev 'is this urgent' --state -f",
		},
		{
			name:      "should flag -q with --assert",
			script:    "jev --ask d='is this destructive' -q --assert 'd.value < 0.5' --state x",
			wantQuiet: true,
		},
		{
			name:      "should flag -q inside a short flag cluster with --assert",
			script:    "jev --ask d='is it' -rq --assert=d.value",
			wantQuiet: true,
		},
		{
			name:      "should flag -q with --assert even when the command is skipped",
			script:    "jev -f q.json --quiet --assert 'a.value'",
			wantSkip:  "reads a file through --file",
			wantQuiet: true,
		},
		{
			name:   "should not flag --assert whose value spells -q",
			script: "jev --ask a='is it' --assert -q",
		},
		{
			name:        "should redact the --api-key value in a reported command",
			script:      "jev --api-key sk-secret auth status",
			wantSkip:    "runs the auth subcommand",
			wantCommand: "jev --api-key REDACTED auth status",
		},
	}

	for _, tc := range commandTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var ran []string

			runner := fakeRunner(func(command string) skillcheck.DryRunResult {
				ran = append(ran, command)

				return skillcheck.DryRunResult{}
			})

			trace, err := json.Marshal(map[string]any{
				"type": "assistant",
				"message": map[string]any{"content": []any{map[string]any{
					"type": "tool_use", "name": "Bash", "input": map[string]string{"command": tc.script},
				}}},
			})
			if err != nil {
				t.Fatal(err)
			}

			grader := newTestGrader(runner, map[string]string{
				"/r.json":  `{"cases":[{"name":"c","arms":{"with":[{"tracePath":"/t.jsonl"}]}}]}`,
				"/t.jsonl": string(trace) + "\n",
			})

			report, err := grader.Grade(context.Background(), "/r.json")
			if err != nil {
				t.Fatalf("Grade(...) error = %v", err)
			}

			arm := report.Cases[0].Arms[0]

			if tc.wantSkip == "" {
				if len(ran) != 1 || arm.Clean != 1 {
					t.Errorf("ran %q with %+v, want one clean dry run", ran, arm)
				}
			} else {
				if len(ran) != 0 || len(arm.Skipped) != 1 || arm.Skipped[0].Detail != tc.wantSkip {
					t.Fatalf("ran %q with %+v, want a skip saying %q", ran, arm, tc.wantSkip)
				}
			}

			if got := len(arm.QuietWithAssert) == 1; got != tc.wantQuiet {
				t.Errorf("QuietWithAssert = %+v, want flagged %v", arm.QuietWithAssert, tc.wantQuiet)
			}

			if tc.wantCommand != "" && arm.Skipped[0].Command != tc.wantCommand {
				t.Errorf("reported command = %q, want %q", arm.Skipped[0].Command, tc.wantCommand)
			}
		})
	}
}

func TestRedactAPIKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "should redact a separate value", command: "jev 'a' --api-key sk-1 -q", want: "jev 'a' --api-key REDACTED -q"},
		{name: "should redact a joined value", command: "jev --api-key=sk-1 'a'", want: "jev --api-key=REDACTED 'a'"},
		{
			name:    "should redact a quoted value with spaces",
			command: `jev --api-key "sk 1" 'a'`,
			want:    "jev --api-key REDACTED 'a'",
		},
		{name: "should leave a command without a key alone", command: "jev 'a'", want: "jev 'a'"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := redactAPIKey(tc.command); got != tc.want {
				t.Errorf("redactAPIKey(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func newTestGrader(runner DryRunner, files map[string]string) *Grader {
	grader := NewGrader(runner)
	grader.readFile = func(name string) ([]byte, error) {
		if body, ok := files[name]; ok {
			return []byte(body), nil
		}

		return nil, fs.ErrNotExist
	}

	return grader
}

type fakeRunner func(command string) skillcheck.DryRunResult

func (f fakeRunner) DryRun(_ context.Context, command string) (skillcheck.DryRunResult, error) {
	return f(command), nil
}
