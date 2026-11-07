package cli

import (
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func validateRun(
	config runConfig, facts runFacts, built *plan.Plan, typed commandLine,
) ([]string, []setting.Notice, error) {
	command := settingrow.CommandRoot
	if config.calibrate != nil {
		command = settingrow.CommandCalibrate
	}

	view := setting.NewView(config.merged, built, setting.Facts{
		Command: command, FileName: facts.name, BodyState: facts.bodyState, Events: config.events,
		Positional: config.positional, StdoutTTY: config.stdoutTTY, Given: typed.given, Args: typed.args,
	})

	return setting.Validate(view, setting.Rules())
}

type commandLine struct {
	given *setting.Given
	args  []string
}
