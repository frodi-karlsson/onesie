package skillcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const runTimeout = 10 * time.Second

// NewRunner returns a Runner that runs binary in environ, defaulting to onesie on PATH when binary
// is empty. ONESIE_MOCK is removed, since a dry run beside it would be refused.
func NewRunner(binary string, environ []string) *Runner {
	if binary == "" {
		binary = "onesie"
	}

	env := withoutMock(environ)

	return &Runner{
		Binary: binary,
		exec: func(ctx context.Context, binary string, args []string) (int, string, error) {
			return runProcess(ctx, binary, args, env)
		},
		help: func(ctx context.Context, binary string, args []string) (string, error) {
			return runHelp(ctx, binary, args, env)
		},
	}
}

func withoutMock(environ []string) []string {
	kept := make([]string, 0, len(environ))

	for _, entry := range environ {
		if !strings.HasPrefix(entry, "ONESIE_MOCK=") {
			kept = append(kept, entry)
		}
	}

	return kept
}

// Runner runs one onesie invocation and reports its exit code and its stderr.
type Runner struct {
	// Binary is the onesie executable to run.
	Binary string

	exec func(ctx context.Context, binary string, args []string) (exitCode int, stderr string, err error)
	help func(ctx context.Context, binary string, args []string) (stdout string, err error)
	book *helpBook
}

// DryRun strips the flags a dry run rejects from command, adds the print flag and runs it without a
// shell. A command that is not a onesie invocation is not run, and Skipped says why.
func (r *Runner) DryRun(ctx context.Context, command string) (DryRunResult, error) {
	tokens, args, reason, err := prepare(command)
	if err != nil {
		return DryRunResult{}, err
	}

	if reason != "" {
		return DryRunResult{Skipped: reason}, nil
	}

	problem, err := r.answerFileProblem(ctx, command, tokens)
	if err != nil {
		return DryRunResult{}, err
	}

	if problem != "" {
		return DryRunResult{Args: args, ExitCode: 2, Stderr: "skillcheck: " + problem}, nil
	}

	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	exitCode, stderr, err := r.run(runCtx, args)
	if err != nil {
		return DryRunResult{}, err
	}

	return DryRunResult{Args: args, ExitCode: exitCode, Stderr: stderr}, nil
}

// DryRunResult is what one DryRun did. Skipped is empty when the command ran.
type DryRunResult struct {
	Args     []string
	Skipped  string
	ExitCode int
	Stderr   string
}

// Help returns the --help text of the onesie command that path names, such as calibrate or auth
// set. An empty path is the root command.
func (r *Runner) Help(ctx context.Context, path []string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()

	return r.help(runCtx, r.Binary, append(append([]string{}, path...), "--help"))
}

func prepare(command string) (tokens, args []string, reason string, err error) {
	tokens, reason = Tokenize(command)
	if reason != "" {
		return nil, nil, reason, nil
	}

	args, reason, err = DryRunArgs(tokens)

	return tokens, args, reason, err
}

// answerFileProblem checks the flags the dry run strips from command before it can hide an error:
// each must be in the --help of the command it follows, and each must come with the flags it needs.
func (r *Runner) answerFileProblem(ctx context.Context, command string, tokens []string) (string, error) {
	if !hasAnswerFileFlag(tokens) {
		return "", nil
	}

	if problem := pairingProblem(tokens); problem != "" {
		return problem, nil
	}

	if r.book == nil {
		r.book = &helpBook{runner: r, pages: map[string]helpPage{}}
	}

	problems, err := r.book.checkCommand(ctx, shellWords(command))
	if err != nil || len(problems) == 0 {
		return "", err
	}

	return problems[0], nil
}

func (r *Runner) run(ctx context.Context, args []string) (int, string, error) {
	return r.exec(ctx, r.Binary, args)
}

func runProcess(ctx context.Context, binary string, args, env []string) (int, string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
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
		return 0, stderr.String(), fmt.Errorf("onesie: running %s: %w", binary, ctx.Err())
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stderr.String(), nil
	}

	return 0, stderr.String(), fmt.Errorf("onesie: running %s: %w", binary, err)
}

func runHelp(ctx context.Context, binary string, args, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(nil)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("onesie: running %s %s: %w: %s",
			binary, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}
