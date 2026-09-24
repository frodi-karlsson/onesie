package assert

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		`urgent.value < 0.5`,
		`a.value < 1 or b.value < 1 and c.value < 1`,
		`not not urgent.value > 0.2`,
		`team.value in ["billing", "technical"]`,
		`team.p["needs review"] > 0.2`,
		`severity.norm <= 0.5 or severity.confidence < 0.6`,
		`(urgent.value < 0.5) and (team.p.billing >= 0.1)`,
		`["a.b"].value == 0.5`,
		`decided.decision != "b"`,
		`severity.value == "high"`,
		`team.p["`,
		`((((urgent.value`,
		`urgent.value < 0.5 and`,
		`'billing'`,
		``,
		`1e309 > urgent.value`,
		`urgent.value < -0.5`,
		`max(urgent.value, severity.norm) < 0.5`,
		`avg(min(urgent.value, 0.2), team.p["billing"]) >= sum(severity.confidence)`,
		`team.confidence in [max(urgent.value, 1), min(`,
	} {
		f.Add(seed)
	}

	built := &plan.Plan{Questions: []plan.Question{
		{ID: "urgent", Shape: plan.Noul},
		{ID: "severity", Shape: plan.Rate, Labelled: true, Levels: []plan.Level{{Label: "low"}, {Label: "high"}}},
		{ID: "team", Shape: plan.Pick, Options: []plan.Option{{Name: "billing"}, {Name: "technical"}}},
		{ID: "a.b", Shape: plan.Noul},
	}}

	value := 0.5
	record := output.Record{Answers: []output.Named{
		{ID: "urgent", Answer: &answer.Answer{Value: 0.5}},
		{ID: "severity", Answer: &answer.Answer{Value: "high", Score: &value, Norm: &value, Confidence: &value}},
		{ID: "team", Answer: &answer.Answer{Value: "billing", Confidence: &value}},
	}}

	f.Fuzz(func(t *testing.T, source string) {
		expr, err := Parse(source)
		if err != nil {
			if expr != nil {
				t.Fatalf("Parse(%q) returned a tree beside the error %v", source, err)
			}

			return
		}

		// The combined source is documented to parse back to the combined tree.
		combined := Combine(expr, expr)

		again, err := Parse(combined.Source())
		if err != nil {
			t.Fatalf("Parse(%q) failed on a combined source: %v", combined.Source(), err)
		}

		if again.root.render() != combined.root.render() {
			t.Fatalf("combined source %q parsed to %s, want %s",
				combined.Source(), again.root.render(), combined.root.render())
		}

		if Check(expr, built) != nil {
			return
		}

		// A checked expression is evaluated for real, so it must hold up against any record.
		Eval(expr, record)
		Eval(expr, output.Record{})
	})
}
