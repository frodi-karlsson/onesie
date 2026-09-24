package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func newQuestionsCmd(settings rootSettings) *cobra.Command {
	format := ""

	cmd := &cobra.Command{
		Use:   "questions",
		Short: "List the question files -f finds by name",
		Long: "questions lists every name -f takes in place of a path, with the file it resolves to.\n\n" +
			"-f NAME looks in the nearest .onesie/questions at or above the working directory, then in " +
			"questions under the config dir auth set uses, and tries NAME.yaml, NAME.yml and NAME.json. " +
			"A name in both places lists only the repository's, which is the one -f uses. A name with two " +
			"files in one directory is listed once per file and marked as a clash, which -f refuses.",
		Example: "  mkdir -p .onesie/questions\n" +
			"  onesie --ask urgent='is this urgent' --print-questions > .onesie/questions/triage.yaml\n" +
			"  onesie questions",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}

			return errors.New("onesie: questions takes no argument. To ask it as a question, put it after --")
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return listQuestions(cmd.OutOrStdout(), settings, format)
		},
	}

	cmd.Flags().StringVarP(&format, "output", "o", "", "table, json or auto, which means table")

	return cmd
}

func listQuestions(w io.Writer, settings rootSettings, format string) error {
	switch format {
	case "", "auto", "table", "json":
	default:
		return fmt.Errorf("onesie: questions -o takes table, json or auto, got %s", format)
	}

	env, err := findEnv(settings)
	if err != nil {
		return err
	}

	listed, err := qfile.List(env)
	if err != nil {
		return err
	}

	rows := savedRows(listed)

	if format == "json" {
		return writeSavedJSON(w, rows)
	}

	return writeSavedTable(w, rows, shortPath(env.WorkDir, settings.homeDir))
}

func savedRows(listed []qfile.Found) []savedRow {
	rows := []savedRow{}

	for _, found := range listed {
		for _, path := range found.Paths {
			rows = append(rows, savedRow{
				Name: found.Name, Path: path, Clash: len(found.Paths) > 1, inRepo: found.InRepo,
			})
		}
	}

	return rows
}

type savedRow struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Clash bool   `json:"clash,omitempty"`

	inRepo bool
}

func writeSavedJSON(w io.Writer, rows []savedRow) error {
	encoded, err := json.Marshal(rows)
	if err != nil {
		return fmt.Errorf("onesie: encoding the question list: %w", err)
	}

	_, err = fmt.Fprintf(w, "%s\n", encoded)

	return err
}

func writeSavedTable(w io.Writer, rows []savedRow, short func(savedRow) string) error {
	width := 0
	for _, row := range rows {
		width = max(width, len(row.Name))
	}

	for _, row := range rows {
		line := fmt.Sprintf("%-*s  %s", width, row.Name, short(row))
		if row.Clash {
			line += "  clash"
		}

		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	return nil
}

func shortPath(workDir string, homeDir func() (string, error)) func(savedRow) string {
	home, err := homeDir()
	if err != nil {
		home = ""
	}

	return func(row savedRow) string {
		if row.inRepo {
			if relative, relErr := filepath.Rel(workDir, row.Path); relErr == nil {
				return relative
			}
		}

		if home == "" {
			return row.Path
		}

		relative, relErr := filepath.Rel(home, row.Path)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return row.Path
		}

		return filepath.Join("~", relative)
	}
}
