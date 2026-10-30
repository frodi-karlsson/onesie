// Package planbuild turns questions from the command line, a question file or a tool's arguments into
// a plan with its gates compiled.
package planbuild

import (
	"errors"
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

// ErrNoQuestion refuses a question file that asks nothing.
var ErrNoQuestion = errors.New("no question given")

// FromFile builds the plan a question file asks on its own, validated, with the file's own gates
// compiled. name is how the caller names the file, and the warnings come back whether or not it fails.
func FromFile(file *qfile.File, name string, speller plan.Speller) (*Built, []string, error) {
	if len(file.Questions) == 0 {
		return nil, nil, ErrNoQuestion
	}

	built, err := Plan(Source{Source: plan.Source{File: file.Questions, FileName: name}}, file)
	if err != nil {
		return nil, nil, err
	}

	warnings, err := plan.Validate(built, name, speller)
	if err != nil {
		return nil, warnings, err
	}

	compiled, err := Compile(built, Gates{FileAssert: file.Assert, FileAbstainIf: file.AbstainIf}, speller)

	return compiled, warnings, err
}

// Plan assembles the questions of src. The plan takes src's model, else the file's, and long context
// when either asks for it. file is nil when no question file was read.
func Plan(src Source, file *qfile.File) (*plan.Plan, error) {
	built, err := plan.Assemble(src.Source)
	if err != nil {
		return nil, err
	}

	built.Model = src.Model
	if built.Model == "" && file != nil {
		built.Model = file.Model
	}

	built.LongContext = src.LongContext || (file != nil && file.LongContext)

	return built, nil
}

// Source is where the questions of a plan come from, with the run's own model and long context.
type Source struct {
	plan.Source

	Model       string
	LongContext bool
}

// Compile compiles the gates, the mapper and the namer of a plan. A file's gate is named by its key,
// and the caller's own settings by speller.
func Compile(built *plan.Plan, gates Gates, speller plan.Speller) (*Built, error) {
	// After validation, since the checker reads a plan the run has accepted, and before any mode
	// dispatches, since the assertion is checked against the plan and every check runs before any
	// network call.
	gate, err := gateOf("--assert", gates.FileAssert, gates.Assert, built, speller)
	if err != nil {
		return nil, err
	}

	abstain, err := gateOf("--abstain-if", gates.FileAbstainIf, gates.AbstainIf, built, speller)
	if err != nil {
		return nil, err
	}

	compiled := &Built{Plan: built, Gate: gate, Abstain: abstain}

	if gates.HasMap {
		if compiled.Mapper, err = Expr(speller.Spell("--map"), gates.Map); err != nil {
			return nil, err
		}
	}

	if gates.HasID {
		if compiled.Namer, err = Expr(speller.Spell("--id"), gates.ID); err != nil {
			return nil, err
		}
	}

	return compiled, nil
}

// Gates holds the expressions a plan is compiled with. FileAssert and FileAbstainIf are a question
// file's own, and the rest are the caller's.
type Gates struct {
	FileAssert, FileAbstainIf string
	Assert, AbstainIf         []string
	Map, ID                   string
	HasMap, HasID             bool
}

// Built is a plan with its gates, its mapper and its namer compiled. Each is nil when not given.
type Built struct {
	Plan    *plan.Plan
	Gate    *assert.Expr
	Abstain *assert.Expr
	Mapper  *jq.Expr
	Namer   *jq.Expr
}

func gateOf(flag, fileSource string, sources []string, built *plan.Plan, speller plan.Speller) (*assert.Expr, error) {
	exprs := make([]*assert.Expr, 0, len(sources)+1)

	// The file leads and the command line follows. The file's expression is the more general one,
	// and argv puts the flag after the -f that named the file. A file's key and its flag combine by
	// and, for the abstain expression as for the assertion, and and is commutative, so the order is
	// a matter of which one an error names first.
	if fileSource != "" {
		// Spelled the way the file spells it, which is the rule plan holds every other message to
		// that names where a setting came from.
		expr, err := gateExpr(fileSource, plan.Keys.Spell(flag), built, speller)
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, expr)
	}

	for _, source := range sources {
		expr, err := gateExpr(source, speller.Spell(flag), built, speller)
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, expr)
	}

	return assert.Combine(exprs...), nil
}

func gateExpr(source, named string, built *plan.Plan, speller plan.Speller) (*assert.Expr, error) {
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
	if checkErr := assert.Check(expr, built, speller); checkErr != nil {
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

// Expr compiles a jq expression, its error named by flag.
func Expr(flag, source string) (*jq.Expr, error) {
	expr, err := jq.Compile(source)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flag, err)
	}

	return expr, nil
}

// WithoutJudgment sets aside a question file's gates and every question's policy, which calibrate
// measures without.
func WithoutJudgment(loaded *qfile.File) {
	loaded.Assert, loaded.AbstainIf = "", ""

	for i := range loaded.Questions {
		loaded.Questions[i].Policy = plan.Policy{}
	}
}
