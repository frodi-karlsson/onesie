package cli

import (
	"io"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/planbuild"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func build(settings rootSettings, flags runConfig, ignoreFileJudgment bool) (*invocation, []string, error) {
	events, positional := flags.events, flags.positional

	// Every mode except --list-models and -i request needs a question, and reporting that here
	// rather than from Assemble keeps the message the same whichever source was missing.
	if positional == "" && len(events) == 0 && flags.file == "" {
		return nil, nil, errNoQuestion
	}

	file, err := readBuildFile(settings, flags, ignoreFileJudgment)
	if err != nil {
		return nil, nil, err
	}

	source := planbuild.Source{
		Source: plan.Source{
			Events:     events,
			Positional: positional,
			FileName:   file.name,
			Replace:    flags.replace,
			ReadFile:   settings.readFile,
		},
		Model:       flags.model,
		LongContext: flags.env.longContext,
	}

	if file.loaded != nil {
		source.File = file.loaded.Questions
	}

	built, err := planbuild.Plan(source, file.loaded)
	if err != nil {
		return nil, nil, err
	}

	warnings, err := validatePlan(built, flags, file.facts)
	if err != nil {
		return nil, warnings, err
	}

	inv, err := invocationOf(settings, flags, built, file)

	return inv, warnings, err
}

func readBuildFile(settings rootSettings, flags runConfig, ignoreFileJudgment bool) (buildFile, error) {
	file := buildFile{name: flags.file}
	if flags.file == "" {
		return file, nil
	}

	data, path, hides, err := readQuestionFile(settings, flags.file)
	if err != nil {
		return buildFile{}, err
	}

	file.name = path

	file.loaded, err = loadQuestionFile(data, path, hides, flags.positional == "" && !asked(flags.events))
	if err != nil {
		return buildFile{}, err
	}

	if ignoreFileJudgment {
		file.setAside = fileGate{assert: file.loaded.Assert, abstainIf: file.loaded.AbstainIf}
		planbuild.WithoutJudgment(file.loaded)
	}

	file.facts = runFacts{assert: file.loaded.Assert, abstainIf: file.loaded.AbstainIf}

	return file, nil
}

type buildFile struct {
	loaded   *qfile.File
	setAside fileGate
	facts    runFacts
	name     string
}

func invocationOf(settings rootSettings, flags runConfig, built *plan.Plan, file buildFile) (*invocation, error) {
	compiled, err := planbuild.Compile(built, planbuild.Gates{
		FileAssert: file.facts.assert, FileAbstainIf: file.facts.abstainIf,
		Assert: flags.assert, AbstainIf: flags.abstainIf,
		Map: flags.mapSource, ID: flags.idSource, HasMap: flags.hasMap, HasID: flags.hasID,
	}, plan.Flags)
	if err != nil {
		return nil, err
	}

	var bodyAssets onesie.Assets
	if file.loaded != nil {
		bodyAssets = file.loaded.Assets
	}

	images, err := newImaging(
		flags.images, flags.imageFields, flags.inputMode == input.Lines, settings.readFile, bodyAssets)
	if err != nil {
		return nil, err
	}

	return &invocation{Built: compiled, loaded: file.loaded, fileGate: file.setAside, images: images}, nil
}

func warnAll(w io.Writer, warnings []string) error {
	for _, warning := range warnings {
		if err := output.Warning(w, warning); err != nil {
			return err
		}
	}

	return nil
}

func questionIDs(questions []plan.Question) []string {
	ids := make([]string, 0, len(questions))
	for _, q := range questions {
		ids = append(ids, q.ID)
	}

	return ids
}

type invocation struct {
	*planbuild.Built

	loaded   *qfile.File
	fileGate fileGate
	images   *imaging
}

type fileGate struct {
	assert, abstainIf string
}
