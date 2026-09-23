package assert

import (
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/answer"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/output"
	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

func TestEval(t *testing.T) {
	t.Parallel()

	built := evalPlan()

	// Both sides of the min-confidence branch in answer.Apply on 'team'. §17.5 composes them, so
	// decision names the fallback exactly when it fired and fallback says why.
	accepted := evalRecord(t, built, 0.8)
	refused := evalRecord(t, built, 0.5)

	tests := []struct {
		name    string
		input   string
		second  string
		refused bool
		want    bool
	}{
		{name: "should compare a number", input: `urgent.value > 0.5`, want: true},
		{name: "should compare a number the other way", input: `urgent.value < 0.5`, want: false},
		{name: "should compare equal numbers", input: `urgent.value == 0.9`, want: true},
		{name: "should compare unequal numbers", input: `urgent.value != 0.9`, want: false},
		{name: "should compare at or below", input: `severity.norm <= 0.5`, want: true},
		{name: "should compare below an equal number", input: `severity.norm < 0.5`, want: false},
		{name: "should compare above an equal number", input: `severity.norm > 0.5`, want: false},
		{name: "should compare at or above an equal number", input: `severity.norm >= 0.5`, want: true},
		{name: "should compare at or above", input: `severity.norm >= 0.6`, want: false},
		{name: "should compare a string", input: `team.value == "billing"`, want: true},
		{name: "should compare a string the other way", input: `team.value != "billing"`, want: false},
		{name: "should read a probability", input: `team.p.billing > 0.5`, want: true},
		{name: "should read a probability by its bracket", input: `severity.p["low"] < 0.2`, want: true},
		{name: "should read a confidence", input: `team.confidence > 0.7`, want: true},
		{name: "should read a score", input: `severity.score == 1`, want: true},
		{name: "should read a norm", input: `severity.norm == 0.5`, want: true},
		{name: "should read a rate value as its label", input: `severity.value == "critical"`, want: true},
		{name: "should read a decision", input: `team.decision == "billing"`, want: true},
		{name: "should read a yes/no decision as a boolean", input: `gated.decision == true`, want: true},
		{name: "should read a yes/no decision the other way", input: `gated.decision == false`, want: false},
		{name: "should read an absent fallback as empty", input: `team.fallback == ""`, want: true},
		{name: "should read the model", input: `model == "jev-1.13.0"`, want: true},
		{name: "should and", input: `urgent.value > 0.5 and team.value == "billing"`, want: true},
		{name: "should and a false right side", input: `urgent.value > 0.5 and team.value == "x"`, want: false},
		{name: "should short circuit and", input: `urgent.value < 0.1 and team.value == "x"`, want: false},
		{name: "should or", input: `urgent.value < 0.1 or team.value == "billing"`, want: true},
		{name: "should short circuit or", input: `urgent.value > 0.5 or team.value == "x"`, want: true},
		{name: "should or two false sides", input: `urgent.value < 0.1 or team.value == "x"`, want: false},
		{name: "should not", input: `not urgent.value < 0.5`, want: true},
		{name: "should not a true comparison", input: `not urgent.value > 0.5`, want: false},
		{name: "should not a group", input: `not (urgent.value > 0.5 and team.value == "billing")`, want: false},
		{name: "should group ahead of and", input: `(urgent.value < 0.1 or team.value == "billing") and severity.norm == 0.5`, want: true},
		{name: "should test membership", input: `team.value in ["billing", "sales"]`, want: true},
		{name: "should test absence", input: `team.value in ["sales"]`, want: false},
		{name: "should test membership of numbers", input: `urgent.value in [0.1, 0.9]`, want: true},
		{name: "should test membership of booleans", input: `gated.decision in [true]`, want: true},
		{name: "should join two assertions", input: `urgent.value > 0.5`, second: `team.value == "billing"`, want: true},
		{name: "should fail a joined assertion on its second half", input: `urgent.value > 0.5`, second: `team.value == "x"`, want: false},
		{
			name:    "should name the fallback in the decision once it fired",
			input:   `team.decision == "human"`,
			refused: true,
			want:    true,
		},
		{
			name:    "should say why the fallback fired",
			input:   `team.fallback == "low_confidence"`,
			refused: true,
			want:    true,
		},
		{
			name:    "should not read the model's answer as the decision once the fallback fired",
			input:   `team.decision == "billing"`,
			refused: true,
			want:    false,
		},
		{
			name:    "should still read the model's own answer once the fallback fired",
			input:   `team.value == "billing"`,
			refused: true,
			want:    true,
		},
		{
			name:    "should not read an empty fallback once it fired",
			input:   `team.fallback == ""`,
			refused: true,
			want:    false,
		},
		{
			name:    "should compose the decision and the reason",
			input:   `team.decision == "human" and team.fallback == "low_confidence"`,
			refused: true,
			want:    true,
		},
		{
			name:  "should name no fallback while the answer stood",
			input: `team.decision == "human" or team.fallback != ""`,
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expr, err := parseAll(tc.input, tc.second)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v, want no error", tc.input, err)
			}

			// Eval promises nothing about an expression Check rejects, so every case here is one
			// Check accepts against the plan the record came from.
			if checked := Check(expr, built); checked != nil {
				t.Fatalf("Check(%q) error = %v, want no error", expr.source, checked)
			}

			record := accepted
			if tc.refused {
				record = refused
			}

			if got := Eval(expr, record); got != tc.want {
				t.Errorf("Eval(%q) = %v, want %v", expr.source, got, tc.want)
			}
		})
	}

	t.Run("should hold when there is nothing to evaluate", func(t *testing.T) {
		t.Parallel()

		if !Eval(nil, accepted) {
			t.Error("Eval(nil) = false, want true")
		}
	})

	t.Run("should answer rather than panic on an expression Check would reject", func(t *testing.T) {
		t.Parallel()

		// Check is what gives an answer its meaning, so nothing here is a promise about these
		// results. It is a promise that a caller who skips Check gets one and not a crash.
		for _, source := range []string{`team.p > 1`, `team.nope == 1`, `urgent.decision == true`} {
			if Eval(mustParse(t, source), accepted) {
				t.Errorf("Eval(%q) = true, want false", source)
			}
		}
	})

	t.Run("should read what an incomplete answer does not carry as no value", func(t *testing.T) {
		t.Parallel()

		// The API is free to leave an option out of its probabilities, so a key Check proved
		// against the plan can still be missing from the record it names.
		partial := output.Record{Answers: []output.Named{
			{ID: "team", Answer: &answer.Answer{Value: "billing", P: &answer.Probabilities{
				Keys: []string{"billing", "technical"}, Values: map[string]float64{"billing": 1},
			}}},
			{ID: "severity", Answer: &answer.Answer{}},
		}}

		for _, source := range []string{
			`team.p.technical > 0`, `team.p.technical == 0`, `team.confidence > 0`,
			`severity.p.low > 0`, `severity.score > 0`, `severity.value == ""`,
		} {
			if Eval(mustParse(t, source), partial) {
				t.Errorf("Eval(%q) over a partial record = true, want false", source)
			}
		}
	})

	t.Run("should read a missing answer as no answer rather than panicking", func(t *testing.T) {
		t.Parallel()

		empty := output.Record{Answers: []output.Named{{ID: "team"}}}

		for _, source := range []string{
			`urgent.value > 0.5`, `urgent.value == 0`, `team.value == "billing"`,
			`team.p.billing > 0.5`, `team.confidence > 0`, `model == "jev-1.13.0"`,
		} {
			if Eval(mustParse(t, source), empty) {
				t.Errorf("Eval(%q) over an empty record = true, want false", source)
			}
		}
	})
}

func evalPlan() *plan.Plan {
	return &plan.Plan{
		Model: "jev-1.13.0",
		Questions: []plan.Question{
			{ID: "urgent", Shape: plan.Noul},
			{ID: "gated", Shape: plan.Noul, Policy: plan.Policy{Threshold: ptr(0.6)}},
			{
				ID: "team", Shape: plan.Pick,
				Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
				Policy: plan.Policy{
					MinConfidence: ptr(0.7), Fallback: &plan.Fallback{Text: "human"},
				},
			},
			{ID: "severity", Shape: plan.Rate, Labelled: true, Levels: []plan.Level{
				{Label: "low"}, {Label: "high"}, {Label: "critical"},
			}},
		},
	}
}

func evalRecord(t *testing.T, built *plan.Plan, confidence float64) output.Record {
	t.Helper()

	record := output.Record{Model: built.Model}

	for _, q := range built.Questions {
		normalized, err := answer.Normalize(q, evalRaw(q, confidence))
		if err != nil {
			t.Fatalf("Normalize(%q) error = %v, want no error", q.ID, err)
		}

		answer.Apply(q, normalized)
		record.Answers = append(record.Answers, output.Named{ID: q.ID, Answer: normalized})
	}

	return record
}

func evalRaw(q plan.Question, confidence float64) jev.Answer {
	switch q.Shape {
	case plan.Pick:
		return &jev.ChoiceAnswer{
			Choice:        "billing",
			Confidence:    confidence,
			Probabilities: map[string]float64{"billing": 0.8, "technical": 0.2},
		}
	case plan.Rate:
		// Held apart from the confidence the pick question varies, so only 'team' moves between
		// the two records.
		return &jev.ScoreAnswer{
			Score:         1,
			Confidence:    0.9,
			Legend:        map[string]string{"0": "low", "1": "high", "2": "critical"},
			Probabilities: map[string]float64{"0": 0.1, "1": 0.3, "2": 0.6},
		}
	default:
		return &jev.NoulAnswer{Noul: 0.9}
	}
}
