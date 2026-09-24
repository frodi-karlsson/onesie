package skillcheck

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewRunner(t *testing.T) {
	t.Parallel()

	t.Run("should default to onesie on PATH when binary is empty", func(t *testing.T) {
		t.Parallel()

		if got := NewRunner("").Binary; got != "onesie" {
			t.Errorf("NewRunner(\"\").Binary = %q, want onesie", got)
		}
	})

	t.Run("should use the binary it is given", func(t *testing.T) {
		t.Parallel()

		if got := NewRunner("/usr/bin/onesie").Binary; got != "/usr/bin/onesie" {
			t.Errorf("NewRunner(...).Binary = %q, want /usr/bin/onesie", got)
		}
	})
}

func TestRunProcess(t *testing.T) {
	// Sets an environment variable the helper process below reads, so it cannot run in parallel
	// with a sibling that relies on the environment being unchanged.
	t.Setenv("SKILLCHECK_WANT_HELPER_PROCESS", "1")

	t.Run("should report an error rather than an ordinary exit code when the deadline kills it", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, _, err := runProcess(ctx, os.Args[0], []string{"-test.run=TestHelperProcessSleeps"})
		if err == nil {
			t.Fatalf("runProcess(...) error = nil, want an error naming the deadline")
		}
	})
}

// TestHelperProcessSleeps is not a real test. TestRunProcess re-execs the binary into it, since
// only a real process lets the deadline logic see a real hang.
func TestHelperProcessSleeps(t *testing.T) {
	if os.Getenv("SKILLCHECK_WANT_HELPER_PROCESS") != "1" {
		return
	}

	time.Sleep(10 * time.Second)
}

func TestRunnerDryRun(t *testing.T) {
	t.Parallel()

	t.Run("should strip -q and run under --print-request, reporting the exit code and stderr", func(t *testing.T) {
		t.Parallel()

		var gotArgs []string

		runner := &Runner{Binary: "onesie", exec: func(_ context.Context, _ string, args []string) (int, string, error) {
			gotArgs = args

			return 2, "onesie: bad", nil
		}}

		result, err := runner.DryRun(context.Background(), "onesie 'is this safe' -q --state 'ls'")
		if err != nil {
			t.Fatalf("DryRun(...) error = %v", err)
		}

		want := "--print-request is this safe --state ls"
		if got := strings.Join(gotArgs, " "); got != want {
			t.Errorf("args = %q, want %q", got, want)
		}

		if result.ExitCode != 2 || result.Stderr != "onesie: bad" || result.Skipped != "" {
			t.Errorf("DryRun(...) = %+v, want exit 2 with stderr and no skip", result)
		}
	})

	t.Run("should skip a command it cannot parse without running anything", func(t *testing.T) {
		t.Parallel()

		runner := &Runner{Binary: "onesie", exec: neverCalled(t)}

		result, err := runner.DryRun(context.Background(), "onesie 'is this safe' | jq .")
		if err != nil {
			t.Fatalf("DryRun(...) error = %v", err)
		}

		if result.Skipped != "contains a pipe" {
			t.Errorf("Skipped = %q, want contains a pipe", result.Skipped)
		}
	})
}
