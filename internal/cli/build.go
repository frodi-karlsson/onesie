package cli

import (
	"fmt"
	"io"
	"slices"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func build(
	settings rootSettings, flags runConfig, given *setting.Given, command setting.Command, ignoreFileJudgment bool,
) (*invocation, assembled, error) {
	ready, err := assemble(settings, flags, given, command, ignoreFileJudgment)
	if err != nil {
		return nil, ready, err
	}

	inv, err := invocationOf(settings, ready.config, ready.plan, ready.file)

	return inv, ready, err
}

func assemble(
	settings rootSettings, flags runConfig, given *setting.Given, command setting.Command, ignoreFileJudgment bool,
) (assembled, error) {
	file, err := readBuildFile(settings, flags, ignoreFileJudgment)
	if err != nil {
		return assembled{}, err
	}

	merged, err := mergeRun(settings, flags, given, command, file.loaded)
	if err != nil {
		return assembled{}, err
	}

	flags = merged.config

	source := plan.Source{
		Events:     flags.events,
		Positional: flags.positional,
		FileName:   file.name,
		Replace:    flags.replace,
		ReadFile:   settings.readFile,
		Numbers:    setting.PolicyNumbers(),
	}

	if file.loaded != nil {
		source.File = file.loaded.Questions
	}

	built, err := plan.Assemble(source)
	if err != nil {
		return assembled{}, err
	}

	built.Model = setting.Get(flags.merged, setting.Model)
	built.LongContext = flags.env.longContext
	refined := setting.MergeQuestions(built, flags.events)

	warnings, typedTwice, err := validateRun(flags, file.facts, built, commandLine{given: given, args: settings.args})
	if err == nil {
		err = merged.refusal
	}

	return assembled{
		config: flags, plan: built, file: file, warnings: warnings,
		notices: inOrder(typedTwice, merged.notices, refined),
	}, err
}

func inOrder(typedTwice, merged, refined []setting.Notice) []setting.Notice {
	var run, questions []setting.Notice

	for _, notice := range typedTwice {
		if row, found := setting.Lookup(notice.Setting); found && row.Scope == setting.ScopeQuestion {
			questions = append(questions, notice)
		} else {
			run = append(run, notice)
		}
	}

	ordered := slices.Concat(run, merged)

	// A question setting typed twice follows the notice of what that setting did to the file.
	for _, notice := range refined {
		ordered = append(ordered, notice)

		for i, twice := range questions {
			if twice.Setting == notice.Setting && twice.Question == notice.Question {
				ordered = append(ordered, twice)
				questions = slices.Delete(questions, i, i+1)

				break
			}
		}
	}

	return append(ordered, questions...)
}

type assembled struct {
	config   runConfig
	plan     *plan.Plan
	file     buildFile
	warnings []string
	notices  []setting.Notice
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
		for i := range file.loaded.Questions {
			file.loaded.Questions[i].Policy = plan.Policy{}
		}
	}

	file.facts = runFacts{
		name: path, bodyState: file.loaded.HasState,
	}

	return file, nil
}

type buildFile struct {
	loaded   *qfile.File
	setAside fileGate
	facts    runFacts
	name     string
}

func invocationOf(settings rootSettings, flags runConfig, built *plan.Plan, file buildFile) (*invocation, error) {
	// After validation, since the checker reads a plan the run has accepted, and before any mode
	// dispatches, since the assertion is checked against the plan and every check runs before any
	// network call.
	gate, err := gateOf("assert", flags.merged, built)
	if err != nil {
		return nil, err
	}

	abstain, err := gateOf("abstain_if", flags.merged, built)
	if err != nil {
		return nil, err
	}

	mapper, err := exprOf(flags.typed(setting.Map.Name()), "--map", flags.mapSource)
	if err != nil {
		return nil, err
	}

	namer, err := exprOf(flags.typed(setting.ID.Name()), "--id", flags.idSource)
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

	return &invocation{
		plan: built, gate: gate, abstain: abstain, mapper: mapper, namer: namer, loaded: file.loaded,
		fileGate: file.setAside, images: images,
	}, nil
}

func warnAll(w io.Writer, warnings []string) error {
	for _, warning := range warnings {
		if err := output.Warning(w, warning); err != nil {
			return err
		}
	}

	return nil
}

func gateOf(name setting.Name, merged *setting.Merged, built *plan.Plan) (*assert.Expr, error) {
	sources, holds := merged.Value(name).([]string)
	if !holds {
		return nil, nil
	}
	origin := merged.Origin(name)
	named := origin.Spelled
	if origin.Source == setting.SourceFile {
		named = "'" + named + "'"
	}

	exprs := make([]*assert.Expr, 0, len(sources))

	for _, source := range sources {
		expr, err := gateExpr(source, named, built)
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, expr)
	}

	return assert.Combine(exprs...), nil
}

func gateExpr(source, named string, built *plan.Plan) (*assert.Expr, error) {
	expr, err := assert.Parse(source)
	if err != nil {
		if hint := assert.BracketHint(source, questionIDs(built.Questions)); hint != "" {
			return nil, fmt.Errorf("%s: %w. %s", named, err, hint)
		}

		return nil, fmt.Errorf("%s: %w", named, err)
	}

	// Each source is checked on its own rather than the combined gate, so a message names the one
	// that carried the mistake. Every source is a boolean in its own right, so a combination adds
	// nothing for the checker to reject.
	if checkErr := assert.Check(expr, built); checkErr != nil {
		return nil, fmt.Errorf("%s: %w", named, checkErr)
	}

	return expr, nil
}

func questionIDs(questions []plan.Question) []string {
	ids := make([]string, 0, len(questions))
	for _, q := range questions {
		ids = append(ids, q.ID)
	}

	return ids
}

func exprOf(given bool, flag, source string) (*jq.Expr, error) {
	if !given {
		return nil, nil
	}

	expr, err := jq.Compile(source)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flag, err)
	}

	return expr, nil
}

type invocation struct {
	plan     *plan.Plan
	gate     *assert.Expr
	abstain  *assert.Expr
	mapper   *jq.Expr
	namer    *jq.Expr
	loaded   *qfile.File
	fileGate fileGate
	images   *imaging
}

type fileGate struct {
	assert, abstainIf string
}
