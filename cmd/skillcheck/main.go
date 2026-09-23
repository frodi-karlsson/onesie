// Command skillcheck dry runs every rule's bad and good example under skills/, per
// docs/jev-skills-spec.md section 6.1.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/frodi-karlsson/jev-cli/internal/skillcheck"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "jev:", err)
		os.Exit(1)
	}

	report, err := skillcheck.Check(context.Background(), root, skillcheck.NewRunner(""))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("jev: checked %d example(s) across %d skill(s), skipped %d\n",
		report.Checked, report.Skills, report.Skipped)

	if report.Passed() {
		return
	}

	for _, failure := range report.Failures {
		fmt.Fprintln(os.Stderr, failure)
	}

	os.Exit(1)
}
