// Command skillcheck dry runs every rule's bad and good example under skills/, and checks the flags
// and commands the prose names against onesie --help.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/frodi-karlsson/onesie/internal/skillcheck"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "onesie:", err)
		os.Exit(1)
	}

	runner := skillcheck.NewRunner("", os.Environ())

	report, err := skillcheck.Check(context.Background(), root, runner)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	prose, err := skillcheck.CheckProse(context.Background(), root, runner)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("onesie: checked %d examples across %d skills, skipped %d\n",
		report.Checked, report.Skills, len(report.Skipped))

	for _, skip := range report.Skipped {
		fmt.Printf("onesie: skill '%s' rule '%s' %s example skipped: %s\n  %s\n",
			skip.Skill, skip.RuleID, skip.Kind, skip.Reason, skip.Command)
	}

	fmt.Printf("onesie: checked %d flag and command mentions in the prose\n", prose.Checked)

	failures := append(report.Failures, prose.Failures...)
	if len(failures) == 0 {
		return
	}

	for _, failure := range failures {
		fmt.Fprintln(os.Stderr, failure)
	}

	os.Exit(1)
}
