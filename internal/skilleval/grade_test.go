package skilleval

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/skillcheck"
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
		"/t/with-1.jsonl":          traceOf(`"onesie 'is this safe' -q --state x && onesie auth clear"`),
		"/t/without-1.jsonl":       traceOf(`"onesie 'is this safe' --bogus; onesie 'a' | jq ."`),
	}

	runner := fakeRunner(func(command string) skillcheck.DryRunResult {
		switch command {
		case "onesie 'is this safe' -q --state x", "onesie 'a'":
			return skillcheck.DryRunResult{ExitCode: 0}
		default:
			return skillcheck.DryRunResult{ExitCode: 2, Stderr: "onesie: unknown flag: --bogus"}
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

		if got := arms[1].Failed[0].Detail; got != "exit 2: onesie: unknown flag: --bogus" {
			t.Errorf("failure detail = %q", got)
		}
	})

	t.Run("should fail an unknown flag carrying a key in the dry run and redact the key", func(t *testing.T) {
		t.Parallel()

		const script = "onesie 'is this urgent' --token sk-secret1234 --state x"

		var ran []string

		runner := fakeRunner(func(command string) skillcheck.DryRunResult {
			ran = append(ran, command)

			return skillcheck.DryRunResult{ExitCode: 2, Stderr: "onesie: unknown flag: --token"}
		})

		grader := newTestGrader(runner, map[string]string{
			"/r.json":  `{"cases":[{"name":"c","arms":{"with":[{"tracePath":"/t.jsonl"}]}}]}`,
			"/t.jsonl": traceOf(`"` + script + `"`),
		})

		report, err := grader.Grade(context.Background(), "/r.json")
		if err != nil {
			t.Fatalf("Grade(...) error = %v", err)
		}

		arm := report.Cases[0].Arms[0]

		if len(ran) != 1 || ran[0] != script {
			t.Fatalf("ran %q, want one dry run of %q", ran, script)
		}

		if len(arm.Failed) != 1 || arm.Failed[0].Detail != "exit 2: onesie: unknown flag: --token" {
			t.Fatalf("arm = %+v, want one unknown flag failure", arm)
		}

		if want := "onesie 'is this urgent' --token REDACTED --state x"; arm.Failed[0].Command != want {
			t.Errorf("reported command = %q, want %q", arm.Failed[0].Command, want)
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
		name     string
		script   string
		wantSkip string
	}{
		{
			name:   "should dry run a quoted question that starts with a subcommand name",
			script: `onesie "help me decide if this is urgent"`,
		},
		{
			name:     "should skip a subcommand that follows a value flag",
			script:   "onesie -m x auth clear",
			wantSkip: "runs the auth subcommand",
		},
		{
			name:     "should skip --state-file with a variable value",
			script:   `onesie 'is this urgent' --state-file "$f"`,
			wantSkip: "reads a file through --state-file",
		},
		{
			name:     "should skip -f with a literal value",
			script:   "onesie -f questions.json",
			wantSkip: "reads a file through --file",
		},
		{
			name:     "should skip -f at the end of a short flag cluster",
			script:   "onesie -qf questions.json",
			wantSkip: "reads a file through --file",
		},
		{
			name:     "should skip --file joined to its value",
			script:   "onesie --file=questions.json",
			wantSkip: "reads a file through --file",
		},
		{
			name:   "should not read a value that spells a flag as the flag",
			script: "onesie 'is this urgent' --state -f",
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

			switch {
			case tc.wantSkip != "":
				if len(ran) != 0 || len(arm.Skipped) != 1 || arm.Skipped[0].Detail != tc.wantSkip {
					t.Fatalf("ran %q with %+v, want a skip saying %q", ran, arm, tc.wantSkip)
				}
			default:
				if len(ran) != 1 || arm.Clean != 1 {
					t.Errorf("ran %q with %+v, want one clean dry run", ran, arm)
				}
			}
		})
	}
}

func TestRedactAPIKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "should redact a key after an unknown flag",
			command: "onesie 'a' --token sk-abc12345XYZ -q",
			want:    "onesie 'a' --token REDACTED -q",
		},
		{
			name:    "should redact a key joined to a flag",
			command: "onesie --token=sk-abc_1234-5678 'a'",
			want:    "onesie --token=REDACTED 'a'",
		},
		{
			name:    "should redact a key inside double quotes",
			command: `onesie --state "use sk-or-v1-abcdef123456 here" 'a'`,
			want:    `onesie --state "use REDACTED here" 'a'`,
		},
		{
			name:    "should redact a key inside single quotes",
			command: "TYPESAFE_API_KEY='sk-abcdefgh12' onesie 'a'",
			want:    "TYPESAFE_API_KEY='REDACTED' onesie 'a'",
		},
		{
			name:    "should redact every key in the command",
			command: "onesie sk-aaaaaaaa1 && onesie sk-bbbbbbbb2",
			want:    "onesie REDACTED && onesie REDACTED",
		},
		{
			name:    "should leave a short sk- word alone",
			command: "onesie 'is sk-1 a key'",
			want:    "onesie 'is sk-1 a key'",
		},
		{name: "should leave a command without a key alone", command: "onesie 'a'", want: "onesie 'a'"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := redactAPIKeys(tc.command); got != tc.want {
				t.Errorf("redactAPIKeys(%q) = %q, want %q", tc.command, got, tc.want)
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
