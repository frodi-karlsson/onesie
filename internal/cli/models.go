package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func listModels(cmd *cobra.Command, settings rootSettings, flags runConfig, stats *collector) error {
	chosen, err := connected(settings, flags)
	if err != nil {
		return err
	}

	client, err := settings.newClient(cmd.Context(), chosen, runOptions(flags, stats)...)
	if err != nil {
		return err
	}

	models, err := client.ListModels(cmd.Context())
	if err != nil {
		// A listing carries no model of its own, so there is none to name in a remedy. It is advised
		// all the same, so every call that reaches the API answers the same way.
		advised := advise(err, "")

		stats.recordFailure(advised, true, 0)
		stats.terminalAttempt(advised)

		return advised
	}

	// No usage, no questions, and no model of its own. The ids in the listing are what the account
	// may ask, not what answered anything, so naming one here would report a model that never ran.
	stats.record("", onesie.Usage{}, 0)

	return writeModels(cmd.OutOrStdout(), models)
}

func writeModels(w io.Writer, models []onesie.ModelCard) error {
	rows := make([][3]string, 0, len(models))
	nameWidth, dateWidth := 0, 0

	for _, model := range models {
		row := [3]string{
			output.Printable(model.Name), output.Printable(model.ReleaseDate), output.Printable(model.Description),
		}
		rows = append(rows, row)
		nameWidth = max(nameWidth, utf8.RuneCountInString(row[0]))
		dateWidth = max(dateWidth, utf8.RuneCountInString(row[1]))
	}

	for _, row := range rows {
		line := padded(row[0], nameWidth) + "  "
		if dateWidth > 0 {
			line += padded(row[1], dateWidth) + "  "
		}

		line += row[2]
		if _, err := fmt.Fprintln(w, strings.TrimRight(line, " ")); err != nil {
			return written(err)
		}
	}

	return nil
}

func padded(cell string, width int) string {
	return cell + strings.Repeat(" ", width-utf8.RuneCountInString(cell))
}
