package skillcheck

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		kind       string
		exitCode   int
		wantPass   bool
		wantFail   bool
		wantChecks func(t *testing.T, failure string)
	}{
		{
			name:     "should pass a good example that exits 0",
			kind:     "good",
			exitCode: 0,
			wantPass: true,
			wantFail: false,
		},
		{
			name:     "should fail a good example that exits 2, naming the skill and the rule id",
			kind:     "good",
			exitCode: 2,
			wantPass: true,
			wantFail: true,
			wantChecks: func(t *testing.T, failure string) {
				t.Helper()

				if !strings.Contains(failure, "demo-skill") || !strings.Contains(failure, "r1") {
					t.Errorf("failure = %q, want it to name the skill and the rule id", failure)
				}
			},
		},
		{
			name:     "should pass a bad example that exits 2",
			kind:     "bad",
			exitCode: 2,
			wantPass: false,
			wantFail: false,
		},
		{
			name:     "should fail a bad example that exits 0, saying the example was supposed to be wrong",
			kind:     "bad",
			exitCode: 0,
			wantPass: false,
			wantFail: true,
			wantChecks: func(t *testing.T, failure string) {
				t.Helper()

				if !strings.Contains(failure, "supposed to fail") {
					t.Errorf("failure = %q, want it to say the example was supposed to fail", failure)
				}
			},
		},
		{
			name:     "should invert for a good example marked good_fails, passing on exit 2",
			kind:     "good",
			exitCode: 2,
			wantPass: false,
			wantFail: false,
		},
		{
			name:     "should invert for a good example marked good_fails, failing on exit 0",
			kind:     "good",
			exitCode: 0,
			wantPass: false,
			wantFail: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runner := &Runner{
				Binary: "jev",
				exec: func(context.Context, string, []string) (int, error) {
					return tc.exitCode, nil
				},
			}

			report := &Report{}

			err := checkRule(
				context.Background(), runner, "demo-skill", "r1", tc.kind,
				"jev --ask a=x", tc.wantPass, report,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if report.Checked != 1 {
				t.Errorf("report.Checked = %d, want 1", report.Checked)
			}

			gotFail := len(report.Failures) == 1
			if gotFail != tc.wantFail {
				t.Fatalf("failures = %v, want a failure: %v", report.Failures, tc.wantFail)
			}

			if tc.wantChecks != nil {
				tc.wantChecks(t, report.Failures[0])
			}
		})
	}
}

func TestCheck(t *testing.T) {
	t.Parallel()

	t.Run("should report zero skills as a success when none exist", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()

		report, err := Check(context.Background(), root, &Runner{Binary: "jev", exec: neverCalled(t)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if report.Skills != 0 || report.Checked != 0 || report.Skipped != 0 || len(report.Failures) != 0 {
			t.Errorf("report = %+v, want an empty, passing report", report)
		}

		if !report.Passed() {
			t.Errorf("report.Passed() = false, want true")
		}
	})

	t.Run("should skip an example it cannot parse as a jev invocation and check the rest", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeCheckFixture(t, root, "demo", `{
			"name": "demo",
			"description": "a demo skill",
			"rules": [
				{
					"id": "r1",
					"short": "Do the thing.",
					"why": "because",
					"bad": "jev --ask a='BADMARKER'",
					"good": "jev --ask a='ok'"
				},
				{
					"id": "r2",
					"short": "Filter, not a command.",
					"why": "because",
					"bad": "jq '.value > 0.5'"
				}
			]
		}`)

		runner := &Runner{
			Binary: "jev",
			exec: func(_ context.Context, _ string, args []string) (int, error) {
				for _, arg := range args {
					if strings.Contains(arg, "BADMARKER") {
						return 2, nil
					}
				}

				return 0, nil
			},
		}

		report, err := Check(context.Background(), root, runner)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if report.Skills != 1 {
			t.Errorf("report.Skills = %d, want 1", report.Skills)
		}

		if report.Checked != 2 {
			t.Errorf("report.Checked = %d, want 2", report.Checked)
		}

		if report.Skipped != 1 {
			t.Errorf("report.Skipped = %d, want 1", report.Skipped)
		}

		if len(report.Failures) != 0 {
			t.Errorf("report.Failures = %v, want none", report.Failures)
		}
	})

	t.Run("should check rather than skip a command whose value collides with an unstripped flag", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		writeCheckFixture(t, root, "demo", `{
			"name": "demo",
			"description": "a demo skill",
			"rules": [
				{
					"id": "r1",
					"short": "Fallback is not part of the catalog.",
					"why": "the mode flag leads, so nothing can consume it as a value regardless",
					"good": "jev --ask a=x --fallback --merge"
				}
			]
		}`)

		var called bool

		runner := &Runner{
			Binary: "jev",
			exec: func(context.Context, string, []string) (int, error) {
				called = true

				return 0, nil
			},
		}

		report, err := Check(context.Background(), root, runner)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !called {
			t.Errorf("the runner was never called, want the example to reach it")
		}

		if report.Checked != 1 {
			t.Errorf("report.Checked = %d, want 1", report.Checked)
		}

		if report.Skipped != 0 {
			t.Errorf("report.Skipped = %d, want 0", report.Skipped)
		}

		if len(report.Failures) != 0 {
			t.Errorf("report.Failures = %v, want none", report.Failures)
		}
	})
}

func neverCalled(t *testing.T) func(context.Context, string, []string) (int, error) {
	t.Helper()

	return func(context.Context, string, []string) (int, error) {
		t.Fatalf("the runner was called, want no example to reach it")

		return 0, nil
	}
}

func writeCheckFixture(t *testing.T, root, name, skillJSON string) {
	t.Helper()

	dir := filepath.Join(root, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "skill.json"), []byte(skillJSON), 0o600); err != nil {
		t.Fatalf("write skill.json: %v", err)
	}
}
