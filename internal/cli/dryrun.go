package cli

import (
	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

// DryRunRules returns the command tree, then by long name the flags a tool strips before either dry
// run and the ones it strips only before --print-questions.
func DryRunRules() (root *cobra.Command, always, questionsOnly []string) {
	for _, row := range settingrow.Table() {
		bound, registered := row.FlagOn(settingrow.CommandRoot)

		// A row in neither run mode is a flag that picks a mode of its own, such as --print-schema.
		if !registered || !row.Modes.Has(settingrow.ModeAsk) && !row.Modes.Has(settingrow.ModeStream) {
			continue
		}

		request, questions := row.Modes.Has(settingrow.ModePrintRequest), row.Modes.Has(settingrow.ModePrintQuestions)

		switch {
		// --print-request takes --stop-on=error but asks no gate, so a stop on a verdict is refused.
		case row.Name == "stop_on" || !request && !questions:
			always = append(always, bound.Long)
		case !questions:
			questionsOnly = append(questionsOnly, bound.Long)
		}
	}

	return NewRootCmd(BuildInfo{}).Command, always, questionsOnly
}
