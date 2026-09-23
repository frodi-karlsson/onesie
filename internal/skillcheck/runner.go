package skillcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// NewRunner returns a Runner that runs binary, defaulting to jev on PATH when binary is empty.
func NewRunner(binary string) *Runner {
	if binary == "" {
		binary = "jev"
	}

	return &Runner{Binary: binary, exec: runProcess}
}

// Runner runs one jev invocation and reports its exit code and its stderr.
type Runner struct {
	// Binary is the jev executable to run.
	Binary string

	exec func(ctx context.Context, binary string, args []string) (exitCode int, stderr string, err error)
}

func (r *Runner) run(ctx context.Context, args []string) (int, string, error) {
	return r.exec(ctx, r.Binary, args)
}

func runProcess(ctx context.Context, binary string, args []string) (int, string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	// A state value of a bare - reads stdin, and an empty reader fails that fast instead of
	// hanging a check on a terminal that will never type anything.
	cmd.Stdin = bytes.NewReader(nil)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return 0, stderr.String(), nil
	}

	// A run killed for taking too long reports through the same *exec.ExitError as an ordinary
	// nonzero exit. Left unchecked, a bad example that hangs would exit nonzero for the wrong
	// reason and pass the check by accident.
	if ctx.Err() != nil {
		return 0, stderr.String(), fmt.Errorf("jev: running %s: %w", binary, ctx.Err())
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stderr.String(), nil
	}

	return 0, stderr.String(), fmt.Errorf("jev: running %s: %w", binary, err)
}
