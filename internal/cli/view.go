package cli

import (
	"github.com/frodi-karlsson/onesie/internal/plan"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func validateRun(
	config runConfig, facts runFacts, built *plan.Plan, typed commandLine,
) ([]string, []setting.Notice, error) {
	view := viewOf(config, facts, built)
	view.Facts.Given, view.Facts.Args = typed.given, typed.args

	return setting.Validate(view, setting.Rules())
}

type commandLine struct {
	given *setting.Given
	args  []string
}

func viewOf(config runConfig, facts runFacts, built *plan.Plan) setting.View {
	merged := &setting.Merged{}

	for _, value := range []struct {
		name  setting.Name
		value any
	}{
		{setting.Output.Name(), config.output},
		{setting.Raw.Name(), config.raw},
		{setting.Quiet.Name(), config.quiet},
		{setting.Input.Name(), config.input},
		{setting.State.Name(), config.state},
		{setting.StateFile.Name(), config.stateFile},
		{setting.PrintQuestions.Name(), config.printQuestions},
		{setting.ListModels.Name(), config.listModels},
		{setting.PrintRequest.Name(), config.printRequest},
		{setting.Unordered.Name(), config.unordered},
		{setting.StopOnError.Name(), config.stopOnError},
		{setting.StopOnAssert.Name(), config.stopOnAssert},
		{setting.SkipBlank.Name(), config.skipBlank},
		{setting.NoDedup.Name(), config.noDedup},
		{setting.MergeInput.Name(), config.merge},
		{setting.MergeKey.Name(), config.mergeKey},
		{setting.Map.Name(), config.mapSource},
		{setting.ID.Name(), config.idSource},
		{setting.Model.Name(), config.model},
		{setting.File.Name(), config.file},
		{setting.Replace.Name(), config.replace},
		{setting.Jobs.Name(), config.jobs},
		{setting.Timeout.Name(), config.timeout},
		{setting.Retries.Name(), config.retries},
		{setting.MaxRetryAfter.Name(), config.maxRetryAfter},
		{setting.Out.Name(), config.out},
		{setting.Resume.Name(), config.resume},
		{setting.Prune.Name(), config.prune},
		{setting.Delimiter.Name(), config.delimiter},
		{setting.Stats.Name(), config.stats},
		{setting.Usage.Name(), config.usage},
	} {
		merged.Set(value.name, value.value, sourceOf(config.typed(value.name)))
	}

	for _, repeated := range []struct {
		name   setting.Name
		values []string
	}{
		{"assert", config.assert},
		{"abstain_if", config.abstainIf},
		{"image", config.images},
		{"image_field", config.imageFields},
	} {
		merged.Set(repeated.name, repeated.values, sourceOf(len(repeated.values) > 0))
	}

	merged.Set(setting.Mock.Name(), config.mockPath, mockSourceOf(config.mockSpelled))
	merged.Set(setting.Cache.Name(), config.env.cache, config.env.cacheFrom)

	viewFacts := setting.Facts{
		Command: setting.CommandRoot, FileName: facts.name, FileAssert: facts.assert, FileAbstainIf: facts.abstainIf,
		BodyState: facts.bodyState, Events: config.events, Positional: config.positional, StdoutTTY: config.stdoutTTY,
	}

	if calib := config.calibrate; calib != nil {
		viewFacts.Command, viewFacts.Report = setting.CommandCalibrate, calib.report
		viewFacts.ReportSet = config.typed(setting.Output.Name())
		merged.Set(setting.Cuts.Name(), calib.cuts, sourceOf(config.typed(setting.Cuts.Name())))
		merged.Set("require", calib.requires, sourceOf(len(calib.requires) > 0))
		merged.Set(setting.Offline.Name(), calib.offline, sourceOf(calib.offline))
	}

	return setting.NewView(merged, built, viewFacts)
}

func sourceOf(typed bool) setting.Source {
	if typed {
		return setting.SourceFlag
	}

	return setting.SourceDefault
}

func mockSourceOf(spelled string) setting.Source {
	switch spelled {
	case flagMock:
		return setting.SourceFlag
	case envMock:
		return setting.SourceEnv
	default:
		return setting.SourceDefault
	}
}
