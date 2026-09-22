package assert

import (
	"maps"
	"slices"
	"strconv"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/answer"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/output"
	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

func TestCheck(t *testing.T) {
	t.Parallel()

	built := &plan.Plan{
		Questions: []plan.Question{
			{ID: "urgent", Shape: plan.Noul},
			{ID: "severity", Shape: plan.Rate, Labelled: true, Levels: []plan.Level{
				{Label: "low"}, {Label: "high"},
			}},
			{ID: "team", Shape: plan.Pick, Options: []plan.Option{
				{Name: "billing"}, {Name: "technical"}, {Name: "sales"},
			}},
			{
				ID: "decided", Shape: plan.Pick, Options: []plan.Option{{Name: "a"}, {Name: "b"}},
				Policy: plan.Policy{MinConfidence: ptr(0.7), Fallback: &plan.Fallback{Text: "b"}},
			},
			{ID: "gated", Shape: plan.Noul, Policy: plan.Policy{Threshold: ptr(0.6)}},
			{ID: "indexed", Shape: plan.Rate, Levels: []plan.Level{{}, {}, {}}},
			{ID: "a.b", Shape: plan.Noul},
		},
	}

	questions := "urgent, severity, team, decided, gated, indexed, a.b"

	tests := []struct {
		name      string
		input     string
		second    string
		wantErr   string
		wantParse string
	}{
		{
			name:  "should accept a yes/no value against a number",
			input: `urgent.value < 0.5`,
		},
		{
			name:  "should accept a pick value against a string",
			input: `team.value == "billing"`,
		},
		{
			name:  "should accept a probability by option name",
			input: `team.p.billing > 0.2`,
		},
		{
			name:  "should accept a probability by bracket",
			input: `team.p["billing"] > 0.2`,
		},
		{
			name:  "should accept a rate score and norm",
			input: `severity.score > 1 and severity.norm < 0.9`,
		},
		{
			name:  "should accept a rate value as a string",
			input: `severity.value == "low"`,
		},
		{
			name:  "should accept a probability by rate label",
			input: `severity.p.low > 0.1 and severity.p["high"] < 0.9`,
		},
		{
			name:  "should accept an unlabelled level by its index",
			input: `indexed.p["0"] > 0.1`,
		},
		{
			name:  "should accept model",
			input: `model == "jev-1.13.0"`,
		},
		{
			name:  "should accept a bracketed head naming a dotted question id",
			input: `["a.b"].value < 0.5`,
		},
		{
			name:  "should accept a yes/no decision against a boolean",
			input: `gated.decision == true`,
		},
		{
			name:  "should accept decision with min-confidence",
			input: `decided.decision == "a"`,
		},
		{
			name:  "should accept fallback on a question with a policy",
			input: `decided.fallback == ""`,
		},
		{
			name:  "should accept a list of the operand's own type",
			input: `team.value in ["billing", "sales"]`,
		},
		{
			name:  "should accept a nested expression over several questions",
			input: `not urgent.value < 0.5 or (team.value == "billing" and severity.norm > 0.5)`,
		},
		{
			name:    "should reject an unknown question",
			input:   `sevrity.value < 1`,
			wantErr: "unknown question 'sevrity'. Questions: " + questions,
		},
		{
			name:    "should reject confidence on a yes/no question",
			input:   `urgent.confidence > 0.5`,
			wantErr: "'urgent' is a yes/no question and has no 'confidence'",
		},
		{
			name:    "should reject a score on a yes/no question",
			input:   `urgent.score > 1`,
			wantErr: "'urgent' is a yes/no question and has no 'score'",
		},
		{
			name:    "should reject a probability on a yes/no question",
			input:   `urgent.p["yes"] > 0.5`,
			wantErr: "'urgent' is a yes/no question and has no 'p'",
		},
		{
			name:    "should reject a score on a pick question",
			input:   `team.score > 1`,
			wantErr: "'team' is a pick question and has no 'score'",
		},
		{
			name:    "should reject an unknown option",
			input:   `team.p.bilingl > 0.2`,
			wantErr: "'team' has no option 'bilingl'. --pick has: billing, technical, sales",
		},
		{
			name:    "should reject an unknown rate label",
			input:   `severity.p.lo > 0.2`,
			wantErr: "'severity' has no label 'lo'. --rate has: low, high",
		},
		{
			name:    "should reject an index an unlabelled rate does not have",
			input:   `indexed.p["9"] > 0.2`,
			wantErr: "'indexed' has no level '9'. Its levels are numbered 0 to 2",
		},
		{
			name:    "should reject a label on an unlabelled rate",
			input:   `indexed.p.low > 0.2`,
			wantErr: "'indexed' has no level 'low'. Its levels are numbered 0 to 2",
		},
		{
			name:    "should reject p without a key",
			input:   `team.p > 0.2`,
			wantErr: "'team.p' needs an option name",
		},
		{
			name:    "should reject p without a label",
			input:   `severity.p > 0.2`,
			wantErr: "'severity.p' needs a level label",
		},
		{
			name:    "should reject p without an index",
			input:   `indexed.p > 0.2`,
			wantErr: "'indexed.p' needs a level index",
		},
		{
			name:    "should reject decision without a policy",
			input:   `team.decision == "billing"`,
			wantErr: "'team.decision' needs --threshold or --min-confidence on 'team'",
		},
		{
			name:    "should reject fallback without a policy",
			input:   `team.fallback == ""`,
			wantErr: "'team.fallback' needs --threshold, --min-confidence or --fallback on 'team'",
		},
		{
			name:    "should reject comparing a string with a number",
			input:   `team.value < 0.5`,
			wantErr: "cannot compare 'team.value', a string, with 0.5, a number",
		},
		{
			name:    "should reject comparing a yes/no decision with a string",
			input:   `gated.decision == "yes"`,
			wantErr: `cannot compare 'gated.decision', a boolean, with "yes", a string`,
		},
		{
			name:    "should reject comparing two paths of different types",
			input:   `team.value == urgent.value`,
			wantErr: "cannot compare 'team.value', a string, with 'urgent.value', a number",
		},
		{
			name:    "should reject an unknown field",
			input:   `urgent.velue < 0.5`,
			wantErr: "'urgent' has no field 'velue'",
		},
		{
			name:    "should reject a field below a resolved one",
			input:   `team.value.x == "a"`,
			wantErr: "'team.value' has no field 'x'",
		},
		{
			name:    "should reject a field below a probability",
			input:   `team.p.billing.x > 0.2`,
			wantErr: "'team.p.billing' has no field 'x'",
		},
		{
			name:    "should reject a field under model",
			input:   `model.name == "x"`,
			wantErr: "'model' has no field 'name'",
		},
		{
			name:    "should reject a question with no field at all",
			input:   `team == "billing"`,
			wantErr: "'team' needs a field. 'team' has: value, confidence, p",
		},
		{
			name:    "should list the fields a policy adds",
			input:   `decided == "a"`,
			wantErr: "'decided' needs a field. 'decided' has: value, confidence, p, decision, fallback",
		},
		{
			name:    "should reject an ordering comparison on strings",
			input:   `team.value < "billing"`,
			wantErr: `cannot order 'team.value', a string. '<' takes two numbers`,
		},
		{
			name:    "should reject an ordering comparison on booleans",
			input:   `gated.decision >= true`,
			wantErr: `cannot order 'gated.decision', a boolean. '>=' takes two numbers`,
		},
		{
			name:    "should reject a mixed list",
			input:   `team.value in ["billing", 1]`,
			wantErr: `a list holds one type. Cannot compare 'team.value', a string, with 1, a number`,
		},
		{
			name:    "should reject a list of another type than the operand",
			input:   `urgent.value in ["billing"]`,
			wantErr: `a list holds one type. Cannot compare 'urgent.value', a number, with "billing", a string`,
		},
		{
			name:    "should report an unknown question inside a list",
			input:   `team.value in ["billing", sevrity.value]`,
			wantErr: "unknown question 'sevrity'. Questions: " + questions,
		},
		{
			name:    "should report the leftmost of two errors",
			input:   `team.velue == "x" and sevrity.value < 1`,
			wantErr: "'team' has no field 'velue'",
		},
		{
			name:    "should report the leftmost of two errors whichever way round they are",
			input:   `sevrity.value < 1 and team.velue == "x"`,
			wantErr: "unknown question 'sevrity'. Questions: " + questions,
		},
		{
			name:    "should report the leftmost error below a not",
			input:   `not (team.velue == "x") or sevrity.value < 1`,
			wantErr: "'team' has no field 'velue'",
		},
		{
			name:    "should report the first assert flag's error before a later one",
			input:   `urgent.value < 1 and team.velue == "x"`,
			second:  `sevrity.value < 1`,
			wantErr: "'team' has no field 'velue'",
		},
		{
			name:      "should leave and over a non boolean to the parser",
			input:     `urgent.value and team.value == "billing"`,
			wantParse: "parse error at column 14, expected a comparison",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expr, err := parseAll(tc.input, tc.second)
			if tc.wantParse != "" {
				if err == nil {
					t.Fatalf("Parse(%q) = %q, want error %q", tc.input, expr.source, tc.wantParse)
				}
				if err.Error() != tc.wantParse {
					t.Errorf("Parse(%q) error = %q, want %q", tc.input, err.Error(), tc.wantParse)
				}

				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) error = %v, want no error", tc.input, err)
			}

			got := Check(expr, built)
			if tc.wantErr == "" {
				if got != nil {
					t.Fatalf("Check(%q) error = %v, want no error", expr.source, got)
				}

				return
			}
			if got == nil {
				t.Fatalf("Check(%q) = nil, want error %q", expr.source, tc.wantErr)
			}

			if got.Error() != tc.wantErr {
				t.Errorf("Check(%q) error = %q, want %q", expr.source, got.Error(), tc.wantErr)
			}
		})
	}

	t.Run("should accept nothing to check", func(t *testing.T) {
		t.Parallel()

		if err := Check(nil, built); err != nil {
			t.Errorf("Check(nil) error = %v, want no error", err)
		}
	})
}

// TestCheckedPaths is §17.3's promise as a test: every path the table allows is accepted with the
// type the table gives it and is present in a normalized record, and everything else is rejected.
func TestCheckedPaths(t *testing.T) {
	t.Parallel()

	options := []plan.Option{{Name: "billing"}, {Name: "technical"}}
	levels := []plan.Level{{Label: "low"}, {Label: "high"}, {Label: "critical"}}

	built := &plan.Plan{
		Questions: []plan.Question{
			{ID: "urgent", Shape: plan.Noul},
			{ID: "gated", Shape: plan.Noul, Policy: plan.Policy{Threshold: ptr(0.6)}},
			{
				ID: "guessed", Shape: plan.Noul,
				Policy: plan.Policy{Fallback: &plan.Fallback{Text: "no"}},
			},
			{ID: "team", Shape: plan.Pick, Options: options},
			{ID: "routed", Shape: plan.Pick, Options: options, Policy: plan.Policy{
				MinConfidence: ptr(0.7), Fallback: &plan.Fallback{Text: "human"},
			}},
			// A pair §11 rejects on its own. answer.Apply has nothing to substitute without a
			// fallback and leaves decision unset, so Check cannot promise the path either.
			{
				ID: "unsure", Shape: plan.Pick, Options: options,
				Policy: plan.Policy{MinConfidence: ptr(0.7)},
			},
			{ID: "severity", Shape: plan.Rate, Labelled: true, Levels: levels},
			{ID: "graded", Shape: plan.Rate, Labelled: true, Levels: levels, Policy: plan.Policy{
				MinConfidence: ptr(0.7), Fallback: &plan.Fallback{Text: "critical"},
			}},
			{ID: "indexed", Shape: plan.Rate, Levels: []plan.Level{{}, {}, {}}},
		},
	}

	// Both sides of the min-confidence branch in answer.Apply, so a decision is read back whether
	// the model's answer stood or the fallback replaced it.
	records := map[string]output.Record{
		"a confident record":      fullRecord(t, built, 0.9),
		"a low confidence record": fullRecord(t, built, 0.5),
	}

	// Every field name the record could carry, including ones belonging to another question, so a
	// path that is not listed as allowed below is asserted to be rejected.
	universe := []string{
		"value", "confidence", "score", "norm", "p", "decision", "fallback", "legend",
		"p.billing", "p.low", `p["0"]`, "p.nope", "value.x",
	}

	tests := []struct {
		name    string
		id      string
		allowed map[string]valueType
	}{
		{
			name: "should carry only a number value on a yes/no question",
			id:   "urgent",
			allowed: map[string]valueType{
				"value": typeNumber,
			},
		},
		{
			name: "should carry a boolean decision on a yes/no question with a threshold",
			id:   "gated",
			allowed: map[string]valueType{
				"value": typeNumber, "decision": typeBoolean, "fallback": typeString,
			},
		},
		{
			name: "should carry a fallback but no decision on a yes/no question with only a fallback",
			id:   "guessed",
			allowed: map[string]valueType{
				"value": typeNumber, "fallback": typeString,
			},
		},
		{
			name: "should carry a string value and probabilities on a pick question",
			id:   "team",
			allowed: map[string]valueType{
				"value": typeString, "confidence": typeNumber,
				"p.billing": typeNumber, "p.technical": typeNumber,
			},
		},
		{
			name: "should carry a string decision on a pick question with min-confidence",
			id:   "routed",
			allowed: map[string]valueType{
				"value": typeString, "confidence": typeNumber,
				"p.billing": typeNumber, "p.technical": typeNumber,
				"decision": typeString, "fallback": typeString,
			},
		},
		{
			name: "should carry no decision on a pick question with min-confidence and no fallback",
			id:   "unsure",
			allowed: map[string]valueType{
				"value": typeString, "confidence": typeNumber,
				"p.billing": typeNumber, "p.technical": typeNumber,
				"fallback": typeString,
			},
		},
		{
			name: "should carry a score and a norm on a labelled rate question",
			id:   "severity",
			allowed: map[string]valueType{
				"value": typeString, "confidence": typeNumber,
				"score": typeNumber, "norm": typeNumber,
				"p.low": typeNumber, "p.high": typeNumber, "p.critical": typeNumber,
			},
		},
		{
			name: "should carry a string decision on a rate question with min-confidence",
			id:   "graded",
			allowed: map[string]valueType{
				"value": typeString, "confidence": typeNumber,
				"score": typeNumber, "norm": typeNumber,
				"p.low": typeNumber, "p.high": typeNumber, "p.critical": typeNumber,
				"decision": typeString, "fallback": typeString,
			},
		},
		{
			name: "should carry probabilities by index on an unlabelled rate question",
			id:   "indexed",
			allowed: map[string]valueType{
				"value": typeString, "confidence": typeNumber,
				"score": typeNumber, "norm": typeNumber,
				`p["0"]`: typeNumber, `p["1"]`: typeNumber, `p["2"]`: typeNumber,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, field := range slices.Sorted(maps.Keys(tc.allowed)) {
				path := tc.id + "." + field
				want := tc.allowed[field]

				if got, ok := probe(t, built, path); !ok || got != want {
					t.Errorf("Check over %q typed it %v, %v, want %v, true", path, got, ok, want)

					continue
				}

				for name, rec := range records {
					value, ok := lookup(rec, pathSegments(t, path))
					if !ok {
						t.Errorf("%q is absent from %s, want a %v", path, name, want)

						continue
					}

					if got, known := goType(value); !known || got != want {
						t.Errorf("%q is a %T in %s, want a %v", path, value, name, want)
					}
				}
			}

			for _, field := range universe {
				if _, ok := tc.allowed[field]; ok {
					continue
				}

				path := tc.id + "." + field
				if got, ok := probe(t, built, path); ok {
					t.Errorf("Check over %q typed it %v, want it rejected", path, got)
				}
			}

			if got, ok := probe(t, built, tc.id); ok {
				t.Errorf("Check over %q typed it %v, want it rejected", tc.id, got)
			}
		})
	}

	t.Run("should carry the model as a string", func(t *testing.T) {
		t.Parallel()

		if got, ok := probe(t, built, "model"); !ok || got != typeString {
			t.Errorf("Check over %q typed it %v, %v, want string, true", "model", got, ok)
		}

		for name, rec := range records {
			value, ok := lookup(rec, []string{"model"})
			if !ok {
				t.Fatalf("'model' is absent from %s, want a string", name)
			}

			if got, known := goType(value); !known || got != typeString {
				t.Errorf("'model' is a %T in %s, want a string", value, name)
			}
		}
	})

	t.Run("should reject a question the plan does not have", func(t *testing.T) {
		t.Parallel()

		for _, path := range []string{"nothing", "nothing.value", "model.value", `["team"].value.x`} {
			if got, ok := probe(t, built, path); ok {
				t.Errorf("Check over %q typed it %v, want it rejected", path, got)
			}
		}
	})
}

func parseAll(sources ...string) (*Expr, error) {
	exprs := make([]*Expr, 0, len(sources))

	for _, source := range sources {
		if source == "" {
			continue
		}

		expr, err := Parse(source)
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, expr)
	}

	return Combine(exprs...), nil
}

func probe(t *testing.T, built *plan.Plan, path string) (valueType, bool) {
	t.Helper()

	literals := map[valueType]string{typeNumber: "1", typeString: `""`, typeBoolean: "true"}

	var (
		found    valueType
		accepted int
	)

	for want, literal := range literals {
		source := path + " == " + literal

		expr, err := Parse(source)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v, want no error", source, err)
		}

		if Check(expr, built) == nil {
			found = want
			accepted++
		}
	}

	if accepted > 1 {
		t.Fatalf("Check over %q accepted %d of the three types, want at most one", path, accepted)
	}

	return found, accepted == 1
}

func pathSegments(t *testing.T, path string) []string {
	t.Helper()

	expr, err := Parse(path + " == 1")
	if err != nil {
		t.Fatalf("Parse(%q) error = %v, want no error", path, err)
	}

	comparison, ok := expr.root.(*comparisonNode)
	if !ok {
		t.Fatalf("Parse(%q) root = %T, want a comparison", path, expr.root)
	}

	operand, ok := comparison.left.(*pathNode)
	if !ok {
		t.Fatalf("Parse(%q) left = %T, want a path", path, comparison.left)
	}

	return operand.segments
}

func lookup(rec output.Record, segments []string) (any, bool) {
	if segments[0] == "model" {
		return rec.Model, len(segments) == 1
	}

	at := slices.IndexFunc(rec.Answers, func(named output.Named) bool {
		return named.ID == segments[0]
	})
	if at < 0 || rec.Answers[at].Answer == nil || len(segments) < 2 {
		return nil, false
	}

	a := rec.Answers[at].Answer

	switch segments[1] {
	case "value":
		return a.Value, len(segments) == 2 && a.Value != nil
	case "confidence":
		return number(a.Confidence, len(segments) == 2)
	case "score":
		return number(a.Score, len(segments) == 2)
	case "norm":
		return number(a.Norm, len(segments) == 2)
	case "p":
		if a.P == nil || len(segments) != 3 || !slices.Contains(a.P.Keys, segments[2]) {
			return nil, false
		}

		value, ok := a.P.Values[segments[2]]

		return value, ok
	case "decision":
		return a.Decision, len(segments) == 2 && a.Decided
	case "fallback":
		return a.Fallback, len(segments) == 2
	default:
		return nil, false
	}
}

func number(value *float64, ok bool) (any, bool) {
	if value == nil || !ok {
		return nil, false
	}

	return *value, true
}

func goType(value any) (valueType, bool) {
	switch value.(type) {
	case float64:
		return typeNumber, true
	case string:
		return typeString, true
	case bool:
		return typeBoolean, true
	default:
		return typeNumber, false
	}
}

func fullRecord(t *testing.T, built *plan.Plan, confidence float64) output.Record {
	t.Helper()

	rec := output.Record{Model: "jev-1.13.0"}

	for _, q := range built.Questions {
		normalized, err := answer.Normalize(q, rawAnswer(q, confidence))
		if err != nil {
			t.Fatalf("Normalize(%q) error = %v, want no error", q.ID, err)
		}

		answer.Apply(q, normalized)
		rec.Answers = append(rec.Answers, output.Named{ID: q.ID, Answer: normalized})
	}

	return rec
}

func rawAnswer(q plan.Question, confidence float64) jev.Answer {
	switch q.Shape {
	case plan.Pick:
		probabilities := map[string]float64{}
		for i, option := range q.Options {
			probabilities[option.Name] = float64(i+1) / float64(len(q.Options)*2)
		}

		return &jev.ChoiceAnswer{
			Choice:        q.Options[len(q.Options)-1].Name,
			Confidence:    confidence,
			Probabilities: probabilities,
		}
	case plan.Rate:
		probabilities := map[string]float64{}
		for i := range q.Levels {
			probabilities[strconv.Itoa(i)] = float64(i+1) / float64(len(q.Levels)*2)
		}

		return &jev.ScoreAnswer{
			Score:         1,
			Confidence:    confidence,
			Legend:        map[string]string{"0": "low"},
			Probabilities: probabilities,
		}
	default:
		return &jev.NoulAnswer{Noul: 0.8}
	}
}

func ptr[T any](value T) *T {
	return &value
}
