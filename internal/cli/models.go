package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func listModels(cmd *cobra.Command, settings rootSettings) error {
	client, err := settings.newClient(cmd.Context())
	if err != nil {
		return err
	}

	models, err := client.ListModels(cmd.Context())
	if err != nil {
		return err
	}

	return writeModels(cmd.OutOrStdout(), models)
}

func writeModels(w io.Writer, models []jev.ModelCard) error {
	for _, model := range models {
		_, err := fmt.Fprintf(w, "%s  %s  %s\n",
			model.Name, model.Description, model.ReleaseDate)
		if err != nil {
			return err
		}
	}

	return nil
}
