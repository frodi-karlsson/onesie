package skilleval

import (
	"context"
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

	grader := &Grader{runner: runner, readFile: func(name string) ([]byte, error) {
		if body, ok := files[name]; ok {
			return []byte(body), nil
		}

		return nil, fs.ErrNotExist
	}}

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
		name  string
		files map[string]string
	}{
		{name: "should fail when the result cannot be read", files: map[string]string{}},
		{name: "should fail when the result is not JSON", files: map[string]string{"/r.json": "nope"}},
	}

	for _, tc := range errorTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			grader := &Grader{runner: fakeRunner(nil), readFile: func(name string) ([]byte, error) {
				if body, ok := tc.files[name]; ok {
					return []byte(body), nil
				}

				return nil, errors.New("missing")
			}}

			if _, err := grader.Grade(context.Background(), "/r.json"); err == nil {
				t.Errorf("Grade(...) error = nil, want an error")
			}
		})
	}
}

type fakeRunner func(command string) skillcheck.DryRunResult

func (f fakeRunner) DryRun(_ context.Context, command string) (skillcheck.DryRunResult, error) {
	return f(command), nil
}
