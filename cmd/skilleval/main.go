// Command skilleval dry runs every jev command an agent wrote during the last claude plugin eval
// run, per docs/jev-skills-spec.md section 6.2. Pass an aggregate-result.json to grade another run.
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

	"github.com/frodi-karlsson/jev-cli/internal/skillcheck"
	"github.com/frodi-karlsson/jev-cli/internal/skilleval"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	resultPath, err := resultFile(args)
	if err != nil {
		return err
	}

	report, err := skilleval.NewGrader(skillcheck.NewRunner("")).Grade(ctx, resultPath)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(out, "jev: dry ran the commands in %s\n", resultPath)
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

func resultFile(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	runs, err := filepath.Glob(filepath.Join("evals", "results", "*", "aggregate-result.json"))
	if err != nil {
		return "", err
	}

	if len(runs) == 0 {
		return "", errors.New("jev: no eval result under evals/results, run make skills-eval first")
	}

	// The directory names are ISO timestamps, so the last one in lexical order is the newest run.
	slices.Sort(runs)

	return runs[len(runs)-1], nil
}

func printCase(out io.Writer, c skilleval.CaseReport) error {
	var b strings.Builder

	fmt.Fprintf(&b, "\n%s\n", c.Name)

	for _, arm := range c.Arms {
		fmt.Fprintf(&b, "  %-8s %d runs, %d jev commands, %d clean, %d failed, %d skipped",
			arm.Arm, arm.Runs, arm.Commands, arm.Clean, len(arm.Failed), len(arm.Skipped))

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
	}

	_, err := io.WriteString(out, b.String())

	return err
}
