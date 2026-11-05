package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/input"
)

const wrapKey = "state"

// DryRunRules returns the command tree, then by long name the flags both dry runs refuse and the
// ones only --print-questions refuses, so a tool that builds a dry run knows what to strip.
func DryRunRules() (root *cobra.Command, always, questionsOnly []string) {
	always = []string{"output", "raw", "quiet", "usage", "merge", "merge-key", "stats", "stop-on-assert", "mock"}
	questionsOnly = []string{
		flagInput, flagJobs, flagModel, flagState, flagStateFile, "unordered", "stop-on-error", "skip-blank",
	}

	return NewRootCmd(BuildInfo{}).Command, always, questionsOnly
}

func checkInputDelimiter(config runConfig) error {
	if !config.hasDelimiter || config.inputMode == input.CSV {
		return nil
	}

	return fmt.Errorf("--%s applies to -i csv. -i %s %s",
		flagDelimiter, inputName(config.input), delimiterlessReason(config.inputMode))
}

func delimiterlessReason(mode input.Mode) string {
	if mode == input.TSV {
		return "is tab separated by definition"
	}

	return "has no delimiter"
}
