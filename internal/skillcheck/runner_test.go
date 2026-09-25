package skillcheck

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// TestRunProcess re-execs the test binary into this, since only a real process lets the
	// deadline logic see a real hang.
	switch os.Getenv("SKILLCHECK_WANT_HELPER_PROCESS") {
	case "1":
		time.Sleep(10 * time.Second)

		return
	case "mock":
		// Exits 3 when the variable reached it, so the parent can tell from the exit code alone.
		if _, found := os.LookupEnv("ONESIE_MOCK"); found {
			os.Exit(3)
		}

		os.Exit(0)
	}

	os.Exit(m.Run())
}

func TestNewRunner(t *testing.T) {
	t.Parallel()

	t.Run("should default to onesie on PATH when binary is empty", func(t *testing.T) {
		t.Parallel()

		if got := NewRunner("", nil).Binary; got != "onesie" {
			t.Errorf("NewRunner(\"\").Binary = %q, want onesie", got)
		}
	})

	t.Run("should use the binary it is given", func(t *testing.T) {
		t.Parallel()

		if got := NewRunner("/usr/bin/onesie", nil).Binary; got != "/usr/bin/onesie" {
			t.Errorf("NewRunner(...).Binary = %q, want /usr/bin/onesie", got)
		}
	})

	t.Run("should run onesie in the environment it is given, with ONESIE_MOCK removed", func(t *testing.T) {
		t.Parallel()

		environ := []string{"SKILLCHECK_WANT_HELPER_PROCESS=mock", "ONESIE_MOCK=answers.json", "ONESIE_MOCKED=x"}
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "SKILLCHECK_WANT_HELPER_PROCESS=") && !strings.HasPrefix(entry, "ONESIE_MOCK=") {
				environ = append(environ, entry)
			}
		}

		code, stderr, err := NewRunner(os.Args[0], environ).run(context.Background(), []string{"-test.run=^$"})
		if err != nil || code != 0 {
			t.Errorf("the child exited %d, %v, so ONESIE_MOCK reached it\n%s", code, err, stderr)
		}

		if got := withoutMock(environ); slices.Contains(got, "ONESIE_MOCK=answers.json") ||
			!slices.Contains(got, "ONESIE_MOCKED=x") {
			t.Errorf("withoutMock kept the wrong entries: %v", got)
		}
	})
}

func TestRunProcess(t *testing.T) {
	// Sets the variable TestMain reads, so it cannot run in parallel with a sibling that relies on
	// the environment being unchanged.
	t.Setenv("SKILLCHECK_WANT_HELPER_PROCESS", "1")

	t.Run("should report an error rather than an ordinary exit code when the deadline kills it", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		_, _, err := runProcess(ctx, os.Args[0], []string{"-test.run=^$"}, os.Environ())
		if err == nil {
			t.Fatalf("runProcess(...) error = nil, want an error naming the deadline")
		}
	})
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
