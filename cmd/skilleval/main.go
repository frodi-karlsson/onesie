// Command skilleval dry runs every onesie command an agent wrote during the last claude plugin eval
// run. Pass an aggregate-result.json to grade another run.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/skillcheck"
	"github.com/frodi-karlsson/onesie/internal/skilleval"
)

const quietAssertNote = "onesie: the -q with --assert count is a separate tally, not part of the eval score. " +
	"It checks every extracted command, so a broken form the agent only quotes as a warning is counted too."

func main() {
	if err := newApp().run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newApp() *app {
	return &app{
		glob:   filepath.Glob,
		grader: skilleval.NewGrader(skillcheck.NewRunner("")),
	}
}

type app struct {
	glob   func(pattern string) ([]string, error)
	grader grader
}

type grader interface {
	Grade(ctx context.Context, resultPath string) (skilleval.Report, error)
}

func (a *app) run(ctx context.Context, args []string, out io.Writer) error {
	resultPath, err := a.resultFile(args)
	if err != nil {
		return err
	}

	report, err := a.grader.Grade(ctx, resultPath)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "onesie: dry ran the commands in %s\n%s\n", resultPath, quietAssertNote)
	if err != nil {
		return err
	}

	for _, c := range report.Cases {
		if err := printCase(out, c); err != nil {
			return err
		}
	}

	return nil
}

func (a *app) resultFile(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	runs, err := a.glob(filepath.Join("evals", "results", "*", "aggregate-result.json"))
	if err != nil {
		return "", err
	}

	if len(runs) == 0 {
		return "", errors.New("onesie: no eval result under evals/results, run make skills-eval first")
	}

	// The directory names are ISO timestamps, so the last one in lexical order is the newest run.
	slices.Sort(runs)

	return runs[len(runs)-1], nil
}

func printCase(out io.Writer, c skilleval.CaseReport) error {
	var b strings.Builder

	fmt.Fprintf(&b, "\n%s\n", c.Name)

	for _, arm := range c.Arms {
		fmt.Fprintf(&b, "  %-8s %d runs, %d onesie commands, %d clean, %d failed, %d skipped, %d use -q with --assert",
			arm.Arm, arm.Runs, arm.Commands, arm.Clean, len(arm.Failed), len(arm.Skipped), len(arm.QuietWithAssert))

		if arm.MissingTraces > 0 {
			fmt.Fprintf(&b, ", %d traces missing, rerun with --keep-temp", arm.MissingTraces)
		}

		b.WriteString("\n")

		for _, f := range arm.Failed {
			fmt.Fprintf(&b, "    failed, run %d: %s\n      %s\n", f.Run, f.Command, strings.TrimSpace(f.Detail))
		}

		for _, f := range arm.Skipped {
			fmt.Fprintf(&b, "    skipped, run %d, %s: %s\n", f.Run, f.Detail, f.Command)
		}

		for _, f := range arm.QuietWithAssert {
			fmt.Fprintf(&b, "    -q with --assert, run %d: %s\n", f.Run, f.Command)
		}
	}

	_, err := io.WriteString(out, b.String())

	return err
}
