package cli

import (
	"github.com/frodi-karlsson/onesie/internal/plan"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func validateRun(config runConfig, facts runFacts, built *plan.Plan) ([]string, error) {
	warnings, _, err := setting.Validate(viewOf(config, facts, built), setting.Rules())

	return warnings, err
}

func viewOf(config runConfig, facts runFacts, built *plan.Plan) setting.View {
	merged := &setting.Merged{}

	for _, value := range []struct {
		name  setting.Name
		value any
		typed bool
	}{
		{setting.Output.Name(), config.output, config.given.Count(setting.Output.Name()) > 0},
		{setting.Raw.Name(), config.raw, config.given.Count(setting.Raw.Name()) > 0},
		{setting.Quiet.Name(), config.quiet, config.given.Count(setting.Quiet.Name()) > 0},
		{setting.Input.Name(), config.input, config.hasInput},
		{setting.State.Name(), config.state, config.hasState},
		{setting.StateFile.Name(), config.stateFile, config.hasStateFile},
		{setting.PrintQuestions.Name(), config.printQuestions, config.given.Count(setting.PrintQuestions.Name()) > 0},
		{setting.ListModels.Name(), config.listModels, config.given.Count(setting.ListModels.Name()) > 0},
		{setting.PrintRequest.Name(), config.printRequest, config.given.Count(setting.PrintRequest.Name()) > 0},
		{setting.Unordered.Name(), config.unordered, config.given.Count(setting.Unordered.Name()) > 0},
		{setting.StopOnError.Name(), config.stopOnError, config.given.Count(setting.StopOnError.Name()) > 0},
		{setting.StopOnAssert.Name(), config.stopOnAssert, config.given.Count(setting.StopOnAssert.Name()) > 0},
		{setting.SkipBlank.Name(), config.skipBlank, config.given.Count(setting.SkipBlank.Name()) > 0},
		{setting.NoDedup.Name(), config.noDedup, config.given.Count(setting.NoDedup.Name()) > 0},
		{setting.MergeInput.Name(), config.merge, config.given.Count(setting.MergeInput.Name()) > 0},
		{setting.MergeKey.Name(), config.mergeKey, config.given.Count(setting.MergeKey.Name()) > 0},
		{setting.Map.Name(), config.mapSource, config.hasMap},
		{setting.ID.Name(), config.idSource, config.hasID},
		{setting.Model.Name(), config.model, config.hasModel},
		{setting.File.Name(), config.file, config.given.Count(setting.File.Name()) > 0},
		{setting.Replace.Name(), config.replace, config.given.Count(setting.Replace.Name()) > 0},
		{setting.Jobs.Name(), config.jobs, config.jobsSet},
		{setting.Timeout.Name(), config.timeout, config.timeoutSet},
		{setting.Retries.Name(), config.retries, config.retriesSet},
		{setting.MaxRetryAfter.Name(), config.maxRetryAfter, config.maxRetryAfterSet},
		{setting.Out.Name(), config.out, config.given.Count(setting.Out.Name()) > 0},
		{setting.Resume.Name(), config.resume, config.given.Count(setting.Resume.Name()) > 0},
		{setting.Prune.Name(), config.prune, config.given.Count(setting.Prune.Name()) > 0},
		{setting.Delimiter.Name(), config.delimiter, config.hasDelimiter},
		{setting.Stats.Name(), config.stats, config.given.Count(setting.Stats.Name()) > 0},
		{setting.Usage.Name(), config.usage, config.given.Count(setting.Usage.Name()) > 0},
		{"assert", config.assert, len(config.assert) > 0},
		{"abstain_if", config.abstainIf, len(config.abstainIf) > 0},
		{"image", config.images, len(config.images) > 0},
		{"image_field", config.imageFields, len(config.imageFields) > 0},
	} {
		merged.Set(value.name, value.value, sourceOf(value.typed))
	}

	merged.Set(setting.Mock.Name(), config.mockPath, mockSourceOf(config.mockSpelled))
	merged.Set(setting.Cache.Name(), config.env.cache, config.env.cacheFrom)

	viewFacts := setting.Facts{
		Command: setting.CommandRoot, FileAssert: facts.assert, FileAbstainIf: facts.abstainIf,
		BodyState: facts.bodyState, Events: config.events, Positional: config.positional, StdoutTTY: config.stdoutTTY,
	}

	if calib := config.calibrate; calib != nil {
		viewFacts.Command, viewFacts.Report, viewFacts.ReportSet = setting.CommandCalibrate, calib.report, config.reportSet
		merged.Set(setting.Cuts.Name(), calib.cuts, sourceOf(config.cutsSet))
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
