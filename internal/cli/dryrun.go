package cli

import (
	"github.com/spf13/cobra"
)

// DryRunRules returns the command tree, then by long name the flags a tool strips before either dry
// run and the ones it strips only before --print-questions.
func DryRunRules() (root *cobra.Command, always, questionsOnly []string) {
	always = []string{
		"output", "raw", "quiet", "usage", "merge", "merge-key", "stats", "stop-on", "mock", flagCache,
	}
	questionsOnly = []string{
		flagInput, flagJobs, flagModel, flagState, flagStateFile, "unordered", "skip-blank",
		"map", "id",
	}

	return NewRootCmd(BuildInfo{}).Command, always, questionsOnly
}
