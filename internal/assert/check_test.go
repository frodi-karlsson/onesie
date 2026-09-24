package assert

import (
	"maps"
	"slices"
	"strconv"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
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
			// A pair §11 rejects on its own. answer.Apply has nothing to substitute without a
			// fallback and leaves decision unset, so the message names the flag that is missing.
			{
				ID: "unsure", Shape: plan.Pick, Options: []plan.Option{{Name: "a"}, {Name: "b"}},
				Policy: plan.Policy{MinConfidence: ptr(0.7)},
			},
			{ID: "gated", Shape: plan.Noul, Policy: plan.Policy{Threshold: ptr(0.6)}},
			{ID: "indexed", Shape: plan.Rate, Levels: []plan.Level{{}, {}, {}}},
			{ID: "a.b", Shape: plan.Noul},
		},
	}

	questions := "urgent, severity, team, decided, unsure, gated, indexed, a.b"

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
			input: `model == "onesie-1.13.0"`,
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
			name:    "should name the fallback a decision is short rather than the confidence it has",
			input:   `unsure.decision == "a"`,
			wantErr: "'unsure.decision' needs --fallback on 'unsure'",
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

	t.Run("should name no questions when the plan carries none", func(t *testing.T) {
		t.Parallel()

		got := Check(mustParse(t, `x.value > 0`), &plan.Plan{})
		if got == nil {
			t.Fatal("Check over an empty plan = nil, want an error")
		}

		if got.Error() != "unknown question 'x'" {
			t.Errorf("Check over an empty plan error = %q, want %q",
				got.Error(), "unknown question 'x'")
		}
	})
}

// TestCheckSpellsTheOrigin holds assertion messages to plan's rule of spelling the offending thing
// the way the question was written. §17.8 publishes the flag spelling.
func TestCheckSpellsTheOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		question plan.Question
		input    string
		wantErr  string
	}{
		{
			name: "should spell a file's options the way the file keys them",
			question: plan.Question{
				ID: "team", Shape: plan.Pick, Origin: plan.OriginFile,
				Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
			},
			input:   `team.p.bilingl > 0.2`,
			wantErr: "'team' has no option 'bilingl'. 'pick' has: billing, technical",
		},
		{
			name: "should spell a body's options as its criteria",
			question: plan.Question{
				ID: "team", Shape: plan.Pick, Origin: plan.OriginBody,
				Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
			},
			input:   `team.p.bilingl > 0.2`,
			wantErr: "'team' has no option 'bilingl'. 'criteria' has: billing, technical",
		},
		{
			name: "should spell a file's labels the way the file keys them",
			question: plan.Question{
				ID: "severity", Shape: plan.Rate, Origin: plan.OriginFile, Labelled: true,
				Levels: []plan.Level{{Label: "low"}, {Label: "high"}},
			},
			input:   `severity.p.lo > 0.2`,
			wantErr: "'severity' has no label 'lo'. 'rate' has: low, high",
		},
		{
			name: "should spell a file's policy keys in the fallback message",
			question: plan.Question{
				ID: "team", Shape: plan.Pick, Origin: plan.OriginFile,
				Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
			},
			input: `team.fallback == ""`,
			wantErr: "'team.fallback' needs 'threshold', 'min_confidence' or 'fallback' " +
				"on 'team'",
		},
		{
			name: "should spell a file's policy keys in the decision message",
			question: plan.Question{
				ID: "team", Shape: plan.Pick, Origin: plan.OriginFile,
				Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
			},
			input:   `team.decision == "billing"`,
			wantErr: "'team.decision' needs 'threshold' or 'min_confidence' on 'team'",
		},
		{
			name: "should name the file's fallback key a decision is short",
			question: plan.Question{
				ID: "team", Shape: plan.Pick, Origin: plan.OriginFile,
				Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
				Policy:  plan.Policy{MinConfidence: ptr(0.7)},
			},
			input:   `team.decision == "billing"`,
			wantErr: "'team.decision' needs 'fallback' on 'team'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			built := &plan.Plan{Questions: []plan.Question{tc.question}}

			got := Check(mustParse(t, tc.input), built)
			if got == nil {
				t.Fatalf("Check(%q) = nil, want error %q", tc.input, tc.wantErr)
			}

			if got.Error() != tc.wantErr {
				t.Errorf("Check(%q) error = %q, want %q", tc.input, got.Error(), tc.wantErr)
			}
		})
	}
}

// TestCheckedPaths proves §17.3 end to end: every path the table allows is accepted with its type
// and resolves in a normalized record, and everything else is rejected.
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
	records := []pathRecord{
		{name: "a confident record", record: fullRecord(t, built, 0.9)},
		{name: "a low confidence record", record: fullRecord(t, built, 0.5), low: true},
	}

	tests := []pathCase{
		{
			name: "should carry only a number value on a yes/no question",
			id:   "urgent",
			allowed: map[string]string{
				"value": "0.8",
			},
		},
		{
			name: "should carry a boolean decision on a yes/no question with a threshold",
			id:   "gated",
			allowed: map[string]string{
				"value": "0.8", "decision": "true", "fallback": `""`,
			},
		},
		{
			name: "should carry a fallback but no decision on a yes/no question with only a fallback",
			id:   "guessed",
			allowed: map[string]string{
				"value": "0.8", "fallback": `""`,
			},
		},
		{
			name: "should carry a string value and probabilities on a pick question",
			id:   "team",
			allowed: map[string]string{
				"value": `"technical"`, "confidence": "0.9",
				"p.billing": "0.25", "p.technical": "0.5",
			},
			low: map[string]string{"confidence": "0.5"},
		},
		{
			name: "should carry a string decision on a pick question with min-confidence",
			id:   "routed",
			allowed: map[string]string{
				"value": `"technical"`, "confidence": "0.9",
				"p.billing": "0.25", "p.technical": "0.5",
				"decision": `"technical"`, "fallback": `""`,
			},
			low: map[string]string{
				"confidence": "0.5", "decision": `"human"`, "fallback": `"low_confidence"`,
			},
		},
		{
			name: "should carry no decision on a pick question with min-confidence and no fallback",
			id:   "unsure",
			allowed: map[string]string{
				"value": `"technical"`, "confidence": "0.9",
				"p.billing": "0.25", "p.technical": "0.5",
				"fallback": `""`,
			},
			low: map[string]string{"confidence": "0.5"},
		},
		{
			name: "should carry a score and a norm on a labelled rate question",
			id:   "severity",
			allowed: map[string]string{
				"value": `"critical"`, "confidence": "0.9",
				"score": "1", "norm": "0.5",
				"p.low": "0.16666666666666666", "p.high": "0.3333333333333333",
				"p.critical": "0.5",
			},
			low: map[string]string{"confidence": "0.5"},
		},
		{
			name: "should carry a string decision on a rate question with min-confidence",
			id:   "graded",
			allowed: map[string]string{
				"value": `"critical"`, "confidence": "0.9",
				"score": "1", "norm": "0.5",
				"p.low": "0.16666666666666666", "p.high": "0.3333333333333333",
				"p.critical": "0.5",
				"decision":   `"critical"`, "fallback": `""`,
			},
			// The fallback text repeats the model's own answer, so only fallback tells the two
			// branches apart here. routed is the pair where decision itself changes.
			low: map[string]string{
				"confidence": "0.5", "fallback": `"low_confidence"`,
			},
		},
		{
			name: "should carry probabilities by index on an unlabelled rate question",
			id:   "indexed",
			allowed: map[string]string{
				"value": `"2"`, "confidence": "0.9",
				"score": "1", "norm": "0.5",
				`p["0"]`: "0.16666666666666666", `p["1"]`: "0.3333333333333333",
				`p["2"]`: "0.5",
			},
			low: map[string]string{"confidence": "0.5"},
		},
	}

	runPathCases(t, built, records, tests)

	t.Run("should carry the model as a string", func(t *testing.T) {
		t.Parallel()

		if got, ok := probe(t, built, "model"); !ok || got != typeString {
			t.Errorf("Check over %q typed it %v, %v, want string, true", "model", got, ok)
		}

		for _, rec := range records {
			source := `model == "onesie-1.13.0"`
			if !Eval(mustParse(t, source), rec.record) {
				t.Errorf("Eval(%q) over %s = false, want true", source, rec.name)
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

// TestCheckedPathsOnAssembledPlan holds the same promise over a plan the pipeline accepts, since
// TestCheckedPaths includes a question §11 rejects.
func TestCheckedPathsOnAssembledPlan(t *testing.T) {
	t.Parallel()

	built := assembledPlan(t)

	records := []pathRecord{
		{name: "a confident record", record: fullRecord(t, built, 0.9)},
		{name: "a low confidence record", record: fullRecord(t, built, 0.5), low: true},
	}

	runPathCases(t, built, records, []pathCase{
		{
			name:    "should carry only a number value on a yes/no question",
			id:      "urgent",
			allowed: map[string]string{"value": "0.8"},
		},
		{
			name: "should carry a boolean decision on a yes/no question with a threshold",
			id:   "gated",
			allowed: map[string]string{
				"value": "0.8", "decision": "true", "fallback": `""`,
			},
		},
		{
			name: "should carry a string decision on a pick question with min-confidence",
			id:   "routed",
			allowed: map[string]string{
				"value": `"technical"`, "confidence": "0.9",
				"p.billing": "0.25", "p.technical": "0.5",
				"decision": `"technical"`, "fallback": `""`,
			},
			low: map[string]string{
				"confidence": "0.5", "decision": `"human"`, "fallback": `"low_confidence"`,
			},
		},
		{
			name: "should carry a score and a norm on a labelled rate question",
			id:   "severity",
			allowed: map[string]string{
				"value": `"critical"`, "confidence": "0.9",
				"score": "1", "norm": "0.5",
				"p.low": "0.16666666666666666", "p.high": "0.3333333333333333",
				"p.critical": "0.5",
			},
			low: map[string]string{"confidence": "0.5"},
		},
	})
}

func runPathCases(t *testing.T, built *plan.Plan, records []pathRecord, cases []pathCase) {
	t.Helper()

	// Every field name a record could carry, including ones belonging to another question, so a
	// path a case does not list as allowed is asserted to be rejected.
	universe := []string{
		"value", "confidence", "score", "norm", "p", "decision", "fallback", "legend",
		"p.billing", "p.low", `p["0"]`, "p.nope", "value.x",
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, field := range slices.Sorted(maps.Keys(tc.allowed)) {
				path := tc.id + "." + field
				want := literalType(t, tc.allowed[field])

				if got, ok := probe(t, built, path); !ok || got != want {
					t.Errorf("Check over %q typed it %v, %v, want %v, true", path, got, ok, want)

					continue
				}

				for _, rec := range records {
					literal := tc.allowed[field]
					if override, ok := tc.low[field]; ok && rec.low {
						literal = override
					}

					source := path + " == " + literal
					if !Eval(mustParse(t, source), rec.record) {
						t.Errorf("Eval(%q) over %s = false, want true", source, rec.name)
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
}

type pathRecord struct {
	name   string
	record output.Record
	low    bool
}

type pathCase struct {
	name    string
	id      string
	allowed map[string]string
	low     map[string]string
}

func assembledPlan(t *testing.T) *plan.Plan {
	t.Helper()

	built, err := plan.Assemble(plan.Source{Events: []argv.Event{
		{Name: "ask", Value: "urgent=is this urgent"},
		{Name: "ask", Value: "gated=is this a release blocker"},
		{Name: "threshold", Value: "0.6"},
		{Name: "ask", Value: "routed=which team owns this"},
		{Name: "pick", Value: "billing,technical"},
		{Name: "min-confidence", Value: "0.7"},
		{Name: "fallback", Value: "human"},
		{Name: "ask", Value: "severity=how severe is this"},
		{Name: "rate", Value: "low,high,critical"},
	}})
	if err != nil {
		t.Fatalf("Assemble error = %v, want no error", err)
	}

	if _, err := plan.Validate(built, plan.Config{}); err != nil {
		t.Fatalf("Validate error = %v, want no error", err)
	}

	return built
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
		if Check(mustParse(t, path+" == "+literal), built) == nil {
			found = want
			accepted++
		}
	}

	if accepted > 1 {
		t.Fatalf("Check over %q accepted %d of the three types, want at most one", path, accepted)
	}

	return found, accepted == 1
}

func literalType(t *testing.T, literal string) valueType {
	t.Helper()

	comparison, ok := mustParse(t, literal+" == "+literal).root.(*comparisonNode)
	if !ok {
		t.Fatalf("Parse(%q) is not a comparison, want one", literal)
	}

	switch comparison.left.(type) {
	case *stringNode:
		return typeString
	case *boolNode:
		return typeBoolean
	default:
		return typeNumber
	}
}

func mustParse(t *testing.T, source string) *Expr {
	t.Helper()

	expr, err := Parse(source)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v, want no error", source, err)
	}

	return expr
}

func fullRecord(t *testing.T, built *plan.Plan, confidence float64) output.Record {
	t.Helper()

	rec := output.Record{Model: "onesie-1.13.0"}

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
