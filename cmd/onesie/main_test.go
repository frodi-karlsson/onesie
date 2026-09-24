//go:build !windows

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

const helperEnv = "ONESIE_INTERRUPTIBLE_HELPER"

func TestMain(m *testing.M) {
	// The signals under test go to a whole process, so the test binary runs itself as the process
	// being signalled rather than signalling the one that runs the tests.
	if os.Getenv(helperEnv) == "1" {
		waitForSignals()

		return
	}

	os.Exit(m.Run())
}

func TestInterruptible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "should cancel on the first signal and die of the second"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			helper := exec.Command(os.Args[0])
			helper.Env = append(os.Environ(), helperEnv+"=1")

			stdout, err := helper.StdoutPipe()
			if err != nil {
				t.Fatalf("stdout pipe: %v", err)
			}

			if err := helper.Start(); err != nil {
				t.Fatalf("starting the helper: %v", err)
			}

			t.Cleanup(func() { helper.Process.Kill() })

			lines := bufio.NewScanner(stdout)

			expectLine(t, lines, "ready")

			if err := helper.Process.Signal(syscall.SIGINT); err != nil {
				t.Fatalf("first SIGINT: %v", err)
			}

			expectLine(t, lines, "cancelled")

			exited := make(chan error, 1)

			go func() { exited <- helper.Wait() }()

			// The handler is released on its own goroutine after the cancel, so a single second
			// signal could land before that and be caught. Repeating it is what a user does too.
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()

			deadline := time.After(5 * time.Second)

			for {
				select {
				case err := <-exited:
					assertKilledBySIGINT(t, err)

					return
				case <-tick.C:
					helper.Process.Signal(syscall.SIGINT)
				case <-deadline:
					t.Fatal("a second SIGINT was caught rather than ending the process")
				}
			}
		})
	}
}

func waitForSignals() {
	ctx, stop := interruptible(context.Background())
	defer stop()

	fmt.Println("ready")

	<-ctx.Done()

	fmt.Println("cancelled")

	// Stands in for a shutdown that never finishes, which only a second signal can end.
	time.Sleep(time.Minute)
}

func expectLine(t *testing.T, lines *bufio.Scanner, want string) {
	t.Helper()

	if !lines.Scan() {
		t.Fatalf("the helper exited before printing %q: %v", want, lines.Err())
	}

	if got := lines.Text(); got != want {
		t.Fatalf("helper printed %q, want %q", got, want)
	}
}

func assertKilledBySIGINT(t *testing.T, err error) {
	t.Helper()

	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("helper exited with %v, want it killed by SIGINT", err)
	}

	status, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGINT {
		t.Errorf("helper exit = %v, want it killed by SIGINT", exit)
	}
}
