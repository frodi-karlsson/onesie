package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func listModels(cmd *cobra.Command, settings rootSettings, stats *collector) error {
	client, err := settings.newClient(cmd.Context(), observing(stats)...)
	if err != nil {
		return err
	}

	models, err := client.ListModels(cmd.Context())
	if err != nil {
		// A listing carries no model of its own, so there is none to name in a remedy, and it
		// sends no questions, so no local bound was checked. It is advised all the same, so every
		// call that reaches the API answers the same way.
		advised := advise(err, "", false)

		stats.recordFailure(advised, true, 0)
		stats.terminalAttempt(advised)

		return advised
	}

	// No usage, no questions, and no model of its own. The ids in the listing are what the account
	// may ask, not what answered anything, so naming one here would report a model that never ran.
	stats.record("", jev.Usage{}, 0)

	return writeModels(cmd.OutOrStdout(), models)
}

func writeModels(w io.Writer, models []jev.ModelCard) error {
	for _, model := range models {
		_, err := fmt.Fprintf(w, "%s  %s  %s\n",
			output.Printable(model.Name), output.Printable(model.Description),
			output.Printable(model.ReleaseDate))
		if err != nil {
			return written(err)
		}
	}

	return nil
}
