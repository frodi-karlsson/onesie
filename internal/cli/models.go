package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func listModels(cmd *cobra.Command, settings rootSettings, stats *collector) error {
	client, err := settings.newClient(cmd.Context(), observing(stats)...)
	if err != nil {
		return err
	}

	models, err := client.ListModels(cmd.Context())
	if err != nil {
		stats.recordFailure(true, 0)
		stats.terminalAttempt(err)

		return err
	}

	// No usage, no questions, and no model of its own. The ids in the listing are what the account
	// may ask, not what answered anything, so naming one here would report a model that never ran.
	stats.record("", jev.Usage{}, 0)

	return writeModels(cmd.OutOrStdout(), models)
}

func writeModels(w io.Writer, models []jev.ModelCard) error {
	for _, model := range models {
		_, err := fmt.Fprintf(w, "%s  %s  %s\n",
			model.Name, model.Description, model.ReleaseDate)
		if err != nil {
			return written(err)
		}
	}

	return nil
}
