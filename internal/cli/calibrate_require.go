package cli

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func parseRequirements(texts []string) ([]calibrate.Requirement, error) {
	reqs := make([]calibrate.Requirement, 0, len(texts))

	for _, text := range texts {
		req, err := calibrate.ParseRequirement(text)
		if err != nil {
			return nil, fmt.Errorf("onesie: --require '%s' %w", text, err)
		}

		reqs = append(reqs, req)
	}

	return reqs, nil
}

func resolveRequirements(built *plan.Plan, gate fileGate, reqs []calibrate.Requirement) ([]boundRequirement, error) {
	bound := make([]boundRequirement, 0, len(reqs))

	for _, req := range reqs {
		resolved, reason := resolveRequirement(built, gate, req)
		if reason != "" {
			return nil, fmt.Errorf("onesie: --require '%s' %s", req.Source, reason)
		}

		bound = append(bound, resolved)
	}

	return bound, nil
}

type boundRequirement struct {
	calibrate.Requirement

	question int
	cut      float64
	hasCut   bool
}

func resolveRequirement(built *plan.Plan, gate fileGate, req calibrate.Requirement) (boundRequirement, string) {
	index := slices.IndexFunc(built.Questions, func(q plan.Question) bool { return q.ID == req.ID })
	if index < 0 {
		ids := make([]string, 0, len(built.Questions))
		for _, q := range built.Questions {
			ids = append(ids, q.ID)
		}

		return boundRequirement{}, fmt.Sprintf("names an unknown question '%s'. Questions: %s",
			req.ID, strings.Join(ids, ", "))
	}

	question := built.Questions[index]
	if needs := shapeNeeded(req.Measure, question.Shape); needs != "" {
		return boundRequirement{}, fmt.Sprintf("reads %s, which needs %s, and '%s' is a %s question",
			req.Measure, needs, question.ID, calibrate.ShapeName(question.Shape))
	}

	resolved := boundRequirement{Requirement: req, question: index}
	if !req.Measure.NeedsCut() {
		return resolved, ""
	}

	cut, reason := cutOfRequirement(gate, req)
	if reason != "" {
		return boundRequirement{}, reason
	}

	resolved.cut, resolved.hasCut = cut, true

	return resolved, ""
}

func shapeNeeded(measure calibrate.Measure, shape plan.Shape) string {
	switch {
	case measure.YesNo() && shape != plan.Noul:
		return "a yes/no question"
	case measure == calibrate.WithinOne && shape != plan.Rate:
		return "a rate question"
	case !measure.YesNo() && shape == plan.Noul:
		return "a pick or rate question"
	}

	return ""
}

func cutOfRequirement(gate fileGate, req calibrate.Requirement) (float64, string) {
	switch req.At.Kind {
	case calibrate.AtNumber:
		return req.At.Value, ""
	case calibrate.AtAbstain:
		if gate.abstainIf == "" {
			return 0, "reads at abstain, and there is no abstain_if to read the cut from. " +
				"Pass -f with a file that has one, or write at CUT"
		}

		return cutFromGate("abstain_if", gate.abstainIf, req.ID)
	default:
		if gate.assert == "" {
			return 0, fmt.Sprintf("needs at CUT, since there is no gate to read the cut for '%s' from. "+
				"Pass -f with a file whose assert compares it, or write at CUT", req.ID)
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

func cutsFor(base []float64, bound []boundRequirement, questions int) [][]float64 {
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

func measureRequirements(report calibrate.Report, bound []boundRequirement) []calibrate.Result {
	if len(bound) == 0 {
		return nil
	}

	results := make([]calibrate.Result, 0, len(bound))
	for _, req := range bound {
		results = append(results, req.Check(report.Questions[req.question], req.cut))
	}

	return results
}

func writeUnmet(w io.Writer, results []calibrate.Result) (bool, error) {
	unmet := false

	for _, result := range results {
		if result.Held {
			continue
		}

		unmet = true

		if _, err := fmt.Fprintln(w, "onesie: "+result.String()); err != nil {
			return unmet, err
		}
	}

	return unmet, nil
}
