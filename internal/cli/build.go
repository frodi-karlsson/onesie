package cli

import (
	"fmt"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func build(settings rootSettings, flags runConfig, ignoreFileJudgment bool) (*invocation, []string, error) {
	events, positional := flags.events, flags.positional

	// Every mode except --list-models and -i request needs a question, and reporting that here
	// rather than from Assemble keeps the message the same whichever source was missing.
	if positional == "" && len(events) == 0 && flags.file == "" {
		return nil, nil, errNoQuestion
	}

	var (
		loaded   *qfile.File
		setAside fileGate
		facts    runFacts
	)

	fileName := flags.file

	if flags.file != "" {
		data, path, hides, readErr := readQuestionFile(settings, flags.file)
		if readErr != nil {
			return nil, nil, readErr
		}

		fileName = path

		var loadErr error

		loaded, loadErr = loadQuestionFile(data, path, hides, positional == "" && !asked(events))
		if loadErr != nil {
			return nil, nil, loadErr
		}

		if ignoreFileJudgment {
			setAside = fileGate{assert: loaded.Assert, abstainIf: loaded.AbstainIf}
			withoutJudgment(loaded)
		}

		facts = runFacts{assert: loaded.Assert, abstainIf: loaded.AbstainIf}
	}

	source := plan.Source{
		Events:     events,
		Positional: positional,
		FileName:   fileName,
		Replace:    flags.replace,
		ReadFile:   settings.readFile,
	}

	if loaded != nil {
		source.File = loaded.Questions
	}

	built, err := plan.Assemble(source)
	if err != nil {
		return nil, nil, err
	}

	built.Model = flags.model
	if built.Model == "" && loaded != nil {
		built.Model = loaded.Model
	}

	built.LongContext = flags.env.longContext || (loaded != nil && loaded.LongContext)

	warnings, err := validatePlan(built, flags, facts)
	if err != nil {
		return nil, warnings, err
	}

	// After validation, since the checker reads a plan the run has accepted, and before any mode
	// dispatches, since the assertion is checked against the plan and every check runs before any
	// network call.
	gate, err := gateOf("--assert", facts.assert, flags.assert, built)
	if err != nil {
		return nil, warnings, err
	}

	abstain, err := gateOf("--abstain-if", facts.abstainIf, flags.abstainIf, built)
	if err != nil {
		return nil, warnings, err
	}

	mapper, err := exprOf(flags.hasMap, "--map", flags.mapSource)
	if err != nil {
		return nil, warnings, err
	}

	namer, err := exprOf(flags.hasID, "--id", flags.idSource)
	if err != nil {
		return nil, warnings, err
	}

	var bodyAssets onesie.Assets
	if loaded != nil {
		bodyAssets = loaded.Assets
	}

	images, err := newImaging(
		flags.images, flags.imageFields, flags.inputMode == input.Lines, settings.readFile, bodyAssets)
	if err != nil {
		return nil, warnings, err
	}

	return &invocation{
		plan: built, gate: gate, abstain: abstain, mapper: mapper, namer: namer, loaded: loaded,
		fileGate: setAside, facts: facts, images: images,
	}, warnings, nil
}

func withoutJudgment(loaded *qfile.File) {
	loaded.Assert, loaded.AbstainIf = "", ""

	for i := range loaded.Questions {
		loaded.Questions[i].Policy = plan.Policy{}
	}
}

func gateOf(flag, fileSource string, sources []string, built *plan.Plan) (*assert.Expr, error) {
	exprs := make([]*assert.Expr, 0, len(sources)+1)

	// The file leads and the command line follows. The file's expression is the more general one,
	// and argv puts the flag after the -f that named the file. A file's key and its flag combine by
	// and, for the abstain expression as for the assertion, and and is commutative, so the order is
	// a matter of which one an error names first.
	if fileSource != "" {
		// Spelled the way the file spells it, which is the rule plan holds every other message to
		// that names where a setting came from.
		expr, err := gateExpr(fileSource, plan.Spelling(plan.OriginFile, flag), built)
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, expr)
	}

	for _, source := range sources {
		expr, err := gateExpr(source, flag, built)
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
	facts    runFacts
	images   *imaging
}

type fileGate struct {
	assert, abstainIf string
}
