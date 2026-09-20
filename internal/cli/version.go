package cli

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd(info BuildInfo) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, commit and build date",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// cobra's own Print helpers write to stderr, not stdout.
			out := cmd.OutOrStdout()

			rows := [][2]string{
				{"version", info.Version},
				{"commit", info.Commit},
				{"built", info.Date},
				{"go", runtime.Version()},
				{"platform", runtime.GOOS + "/" + runtime.GOARCH},
			}

			for _, row := range rows {
				if _, err := fmt.Fprintf(out, "%-9s %s\n", row[0], row[1]); err != nil {
					return err
				}
			}

			return nil
		},
	}
}
