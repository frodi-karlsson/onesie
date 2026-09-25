// Command skillcheck dry runs every rule's bad and good example under skills/.
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

	report, err := skillcheck.Check(context.Background(), root, skillcheck.NewRunner("", os.Environ()))
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

	if report.Passed() {
		return
	}

	for _, failure := range report.Failures {
		fmt.Fprintln(os.Stderr, failure)
	}

	os.Exit(1)
}
