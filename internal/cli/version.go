package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func newVersionCmd(info BuildInfo) *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version, commit, build date and the built in limits",
		Args:  noArguments,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// cobra's own Print helpers write to stderr, not stdout.
			out := cmd.OutOrStdout()

			asJSON, err := versionFormat("version", format)
			if err != nil {
				return err
			}

			if asJSON {
				return writeVersionJSON(out, info)
			}

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

			return writeLimits(out)
		},
	}

	cmd.Flags().StringVarP(&format, "output", "o", "", "json prints the same as one JSON object")

	return cmd
}

func versionFormat(command, format string) (asJSON bool, err error) {
	switch format {
	case "", "auto":
		return false, nil
	case output.JSON.String():
		return true, nil
	}

	return false, fmt.Errorf("%s prints text, or JSON under -o json, so -o %s does not apply", command, format)
}

func writeVersionJSON(w io.Writer, info BuildInfo) error {
	limited := map[string]string{}
	for _, entry := range limits.Report() {
		limited[entry.Name] = entry.Value
	}

	encoded, err := json.Marshal(versionReport{
		Version: info.Version, Commit: info.Commit, Date: info.Date,
		Go: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH, Limits: limited,
	})
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(w, "%s\n", encoded)

	return err
}

type versionReport struct {
	Version  string            `json:"version"`
	Commit   string            `json:"commit"`
	Date     string            `json:"date"`
	Go       string            `json:"go"`
	Platform string            `json:"platform"`
	Limits   map[string]string `json:"limits"`
}

func writeLimits(w io.Writer) error {
	for _, entry := range limits.Report() {
		if _, err := fmt.Fprintf(w, "%s %s\n", entry.Name, entry.Value); err != nil {
			return err
		}
	}

	return nil
}
