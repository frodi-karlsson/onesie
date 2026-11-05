package cli

import (
	"github.com/spf13/cobra"
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
