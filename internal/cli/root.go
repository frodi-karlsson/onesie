// Package cli assembles the jev command tree. It sits outside package main so
// the tree can be built and exercised in tests without spawning a process.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds a fresh command tree that reads and writes no global state.
func NewRootCmd(info BuildInfo) *cobra.Command {
	root := &cobra.Command{
		Use:   "jev",
		Short: "Command line interface for TypeSafe Jev",
		Long: "jev is the command line interface for TypeSafe Jev.\n\n" +
			"Run `jev <command> --help` for details on any subcommand.",
		Version: info.Version,
		// Cobra otherwise buries every returned error under the full help text.
		// main owns the reporting instead.
		SilenceUsage:  true,
		SilenceErrors: true,
		// Without this, a bare jev exits non zero with "unknown command".
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	root.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	root.AddCommand(newVersionCmd(info))

	return root
}

// BuildInfo carries the build metadata stamped into the binary at link time.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}
