package skillcheck

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestNewRunner(t *testing.T) {
	t.Parallel()

	t.Run("should default to jev on PATH when binary is empty", func(t *testing.T) {
		t.Parallel()

		if got := NewRunner("").Binary; got != "jev" {
			t.Errorf("NewRunner(\"\").Binary = %q, want jev", got)
		}
	})

	t.Run("should use the binary it is given", func(t *testing.T) {
		t.Parallel()

		if got := NewRunner("/usr/bin/jev").Binary; got != "/usr/bin/jev" {
			t.Errorf("NewRunner(...).Binary = %q, want /usr/bin/jev", got)
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

// TestHelperProcessSleeps is not a real test. TestRunProcess re-execs the test binary as a
// helper process that runs long enough to be killed by a deadline, the way the exec package's
// own tests do, since a real hang has to be a real process for the deadline logic to see.
func TestHelperProcessSleeps(t *testing.T) {
	if os.Getenv("SKILLCHECK_WANT_HELPER_PROCESS") != "1" {
		return
	}

	time.Sleep(10 * time.Second)
}
