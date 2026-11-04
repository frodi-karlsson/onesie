package calibrate

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// ResolveRequirements binds each requirement to its scored question and to the cut it reads, from
// the requirement itself or from the file's assert or abstain_if. speller names the settings.
func ResolveRequirements(
	scope Scope, assert, abstainIf string, reqs []Requirement, speller plan.Speller,
) ([]BoundRequirement, error) {
	bound := make([]BoundRequirement, 0, len(reqs))
	gate := fileGates{assert: assert, abstainIf: abstainIf, speller: speller}
	require, label := speller.Spell("--require"), speller.Spell("--label")

	for _, req := range reqs {
		if slices.Contains(scope.Skipped, req.ID) {
			return nil, fmt.Errorf("%s '%s' reads '%s', which has no %s, so the report "+
				"skips it. Pass %s %s=EXPR", require, req.Source, req.ID, label, label, req.ID)
		}

		resolved, reason := resolveRequirement(scope.Scored, gate, req)
		if reason != "" {
			return nil, fmt.Errorf("%s '%s' %s", require, req.Source, reason)
		}

		bound = append(bound, resolved)
	}

	return bound, nil
}

// BoundRequirement is a requirement bound to its question and, when it reads one, its cut.
type BoundRequirement struct {
	Requirement

	question int
	cut      float64
	hasCut   bool
}

type fileGates struct {
	assert, abstainIf string
	speller           plan.Speller
}

func resolveRequirement(built *plan.Plan, gate fileGates, req Requirement) (BoundRequirement, string) {
	index := slices.IndexFunc(built.Questions, func(q plan.Question) bool { return q.ID == req.ID })
	if index < 0 {
		ids := make([]string, 0, len(built.Questions))
		for _, q := range built.Questions {
			ids = append(ids, q.ID)
		}

		return BoundRequirement{}, fmt.Sprintf("names an unknown question '%s'. Questions: %s",
			req.ID, strings.Join(ids, ", "))
	}

	question := built.Questions[index]
	if needs := shapeNeeded(req.Measure, question.Shape); needs != "" {
		return BoundRequirement{}, fmt.Sprintf("reads %s, which needs %s, and '%s' is a %s question",
			req.Measure, needs, question.ID, shapeWords(question.Shape))
	}

	resolved := BoundRequirement{Requirement: req, question: index}
	if !req.Measure.NeedsCut() {
		return resolved, ""
	}

	cut, reason := cutOfRequirement(gate, req)
	if reason != "" {
		return BoundRequirement{}, reason
	}

	resolved.cut, resolved.hasCut = cut, true

	return resolved, ""
}

func shapeNeeded(measure Measure, shape plan.Shape) string {
	switch {
	case measure.YesNo() && shape != plan.Noul:
		return "a yes or no question"
	case measure == WithinOne && shape != plan.Rate:
		return "a rate question"
	case !measure.YesNo() && shape == plan.Noul:
		return "a pick or rate question"
	}

	return ""
}

func cutOfRequirement(gate fileGates, req Requirement) (float64, string) {
	switch req.At.Kind {
	case AtNumber:
		return req.At.Value, ""
	case AtAbstain:
		if gate.abstainIf == "" {
			return 0, fmt.Sprintf("reads at abstain, and there is no abstain_if to read the cut from. "+
				"Pass %s with a file that has one, or write at CUT", gate.speller.Spell("-f"))
		}

		return cutFromGate("abstain_if", gate.abstainIf, req.ID)
	default:
		if gate.assert == "" {
			return 0, fmt.Sprintf("needs at CUT, since there is no gate to read the cut for '%s' from. "+
				"Pass %s with a file whose assert compares it, or write at CUT", req.ID, gate.speller.Spell("-f"))
		}

		return cutFromGate("assert", gate.assert, req.ID)
	}
}

func cutFromGate(name, source, id string) (float64, string) {
	expr, err := assert.Parse(source)
	if err != nil {
		return 0, fmt.Sprintf("needs at CUT, since the file's %s does not parse: %v", name, err)
	}

	cut, found := assert.Cuts(expr)[id]

	switch {
	case expr.Constant():
		return 0, fmt.Sprintf("needs at CUT, since the file's %s is the constant %s, which gives no cut",
			name, source)
	case !found:
		return 0, fmt.Sprintf("needs at CUT, since the file's %s never names '%s'", name, id)
	case !cut.Readable:
		return 0, fmt.Sprintf("needs at CUT, since the file's %s gives no cut for '%s': %s", name, id, cut.Why)
	case cut.Value < 0 || cut.Value > 1:
		return 0, fmt.Sprintf("needs at CUT, since the file's %s compares %s.value with %s, and a cut lies "+
			"between 0 and 1", name, id, strconv.FormatFloat(cut.Value, 'f', -1, 64))
	}

	return cut.Value, ""
}

// CutsFor is the cuts each question reports, base and every cut a requirement reads, in order.
func CutsFor(base []float64, bound []BoundRequirement, questions int) [][]float64 {
	cuts := make([][]float64, questions)
	for q := range cuts {
		cuts[q] = base
	}

	for _, req := range bound {
		if !req.hasCut || slices.Contains(cuts[req.question], req.cut) {
			continue
		}

		cuts[req.question] = append(slices.Clone(cuts[req.question]), req.cut)
		slices.Sort(cuts[req.question])
	}

	return cuts
}

// MeasureRequirements checks each requirement against the report.
func MeasureRequirements(report Report, bound []BoundRequirement) []Result {
	if len(bound) == 0 {
		return nil
	}

	results := make([]Result, 0, len(bound))
	for _, req := range bound {
		results = append(results, req.Check(report.Questions[req.question], req.cut))
	}

	return results
}

func shapeWords(shape plan.Shape) string {
	if shape == plan.Noul {
		return "yes or no"
	}

	return shape.String()
}
