package skillcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Runner runs one jev invocation and reports its exit code.
type Runner struct {
	// Binary is the jev executable to run.
	Binary string

	exec func(ctx context.Context, binary string, args []string) (int, error)
}

// NewRunner returns a Runner that runs binary, defaulting to jev on PATH when binary is empty.
func NewRunner(binary string) *Runner {
	if binary == "" {
		binary = "jev"
	}

	return &Runner{Binary: binary, exec: runProcess}
}

func (r *Runner) run(ctx context.Context, args []string) (int, error) {
	return r.exec(ctx, r.Binary, args)
}

func runProcess(ctx context.Context, binary string, args []string) (int, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	// A state value of a bare - reads stdin, and an empty reader fails that fast instead of
	// hanging a check on a terminal that will never type anything.
	cmd.Stdin = bytes.NewReader(nil)

	err := cmd.Run()
	if err == nil {
		return 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}

	return 0, fmt.Errorf("jev: running %s: %w", binary, err)
}
