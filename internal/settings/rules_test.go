package settings_test

import (
	"slices"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	t.Run("should check the settings alone when no plan is built", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			set        values
			typed      []settings.Name
			events     []argv.Event
			positional string
			facts      settings.Facts
			wantErr    string
		}{
			{
				name:    "should refuse --image with -i request",
				set:     values{"image": []string{"photo=photo.png"}, "input": "request", "jobs": 1},
				wantErr: "--image does not apply to -i request, whose bodies carry their own state",
			},
			{
				name:    "should refuse --image-field with -i request",
				set:     values{"input": "request", "jobs": 1, "image_field": []string{"photo"}},
				wantErr: "--image-field does not apply to -i request, whose bodies carry their own state",
			},
			{
				name:    "should refuse --box with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "box", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --box",
			},
			{
				name:       "should reject a positional question with -i request",
				set:        values{"input": "request", "jobs": 1},
				positional: "is it urgent",
				wantErr: "-i request carries its own questions. " +
					"Drop the question argument",
			},
			{
				name:    "should reject --ask with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "ask", Value: "a=x"}},
				wantErr: "-i request carries its own questions. Drop --ask",
			},
			{
				name: "should reject -f with -i request",
				set:  values{"input": "request", "jobs": 1, "file": "q.yaml"},
				wantErr: "-f does not apply to -i request, " +
					"which carries its own questions",
			},
			{
				name:    "should reject --replace with -i request",
				set:     values{"input": "request", "jobs": 1, "replace": true},
				wantErr: "--replace applies to -f, which -i request does not accept",
			},
			{
				name:    "should reject --pick with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "pick", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --pick",
			},
			{
				name:    "should reject --rate with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "rate", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --rate",
			},
			{
				name:    "should reject --desc with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "desc", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --desc",
			},
			{
				name:    "should reject --sep with -i request",
				set:     values{"input": "request", "jobs": 1},
				events:  []argv.Event{{Name: "sep", Value: "x"}},
				wantErr: "-i request carries its own questions. Drop --sep",
			},
			{
				name:   "should reject --threshold with -i request",
				set:    values{"input": "request", "jobs": 1},
				events: []argv.Event{{Name: "threshold", Value: "x"}},
				wantErr: "--threshold does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name:   "should reject --min-confidence with -i request",
				set:    values{"input": "request", "jobs": 1},
				events: []argv.Event{{Name: "min-confidence", Value: "x"}},
				wantErr: "--min-confidence does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name:   "should reject --fallback with -i request",
				set:    values{"input": "request", "jobs": 1},
				events: []argv.Event{{Name: "fallback", Value: "x"}},
				wantErr: "--fallback does not apply to -i request, " +
					"which carries no policy",
			},
			{
				name:  "should reject --state with -i request",
				set:   values{"input": "request", "jobs": 1},
				typed: []settings.Name{"state"},
				wantErr: "--state does not apply to -i request, " +
					"whose bodies carry their own state",
			},
			{
				name:  "should reject --state-file with -i request",
				set:   values{"input": "request", "jobs": 1},
				typed: []settings.Name{"state_file"},
				wantErr: "--state-file does not apply to -i request, " +
					"whose bodies carry their own state",
			},
			{
				name:    "should reject --map with -i request",
				set:     values{"input": "request", "jobs": 1},
				typed:   []settings.Name{"map"},
				wantErr: "--map does not apply to -i request, whose bodies carry their own state",
			},
			{
				name:    "should reject --map with --list-models",
				set:     values{"list_models": true, "input": "text"},
				typed:   []settings.Name{"map"},
				wantErr: "--map does not apply to --list-models, which reads no state",
			},
			{
				name:    "should reject --map with --print-questions",
				set:     values{"print_questions": true, "input": "text"},
				typed:   []settings.Name{"map"},
				wantErr: "--map does not apply to --print-questions, which reads no state",
			},
			{
				name:  "should accept --map with --print-request",
				set:   values{"print_request": true, "input": "jsonl", "jobs": 1},
				typed: []settings.Name{"map"},
			},
			{
				name:    "should reject --id with -i request",
				set:     values{"input": "request", "jobs": 1},
				typed:   []settings.Name{"id"},
				wantErr: "--id does not apply to -i request, which forwards raw responses",
			},
			{
				name:    "should reject --id with --list-models",
				set:     values{"list_models": true, "input": "text"},
				typed:   []settings.Name{"id"},
				wantErr: "--id does not apply to --list-models, which reads no input",
			},
			{
				name:    "should reject --id with --print-questions",
				set:     values{"print_questions": true, "input": "text"},
				typed:   []settings.Name{"id"},
				wantErr: "--id does not apply to --print-questions, which reads no input",
			},
			{
				name:  "should accept --id with --print-request",
				set:   values{"print_request": true, "input": "csv", "jobs": 1},
				typed: []settings.Name{"id"},
			},
			{
				name:    "should reject -o with -i request",
				set:     values{"input": "request", "jobs": 1, "output": "json"},
				wantErr: "-o does not apply to -i request, which forwards raw responses",
			},
			{
				name:    "should reject -r with -i request",
				set:     values{"input": "request", "jobs": 1, "raw": true},
				wantErr: "-r does not apply to -i request, which forwards raw responses",
			},
			{
				name: "should reject --usage with -i request",
				set:  values{"input": "request", "jobs": 1, "usage": true},
				wantErr: "--usage does not apply to -i request, " +
					"whose response bodies already carry usage",
			},
			{
				name:    "should reject --merge with -i request",
				set:     values{"merge": true, "input": "request", "jobs": 1},
				wantErr: "--merge does not apply to -i request, which forwards raw responses",
			},
			{
				name: "should name --merge-key when that is the flag given",
				set:  values{"merge_key": "verdict", "input": "request", "jobs": 1},
				wantErr: "--merge-key does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name:  "should reject -m with -i request",
				set:   values{"input": "request", "jobs": 1},
				typed: []settings.Name{"model"},
				wantErr: "-m does not apply to -i request, " +
					"whose bodies carry their own model",
			},
			{
				name: "should reject --print-questions with -i request",
				set:  values{"input": "request", "jobs": 1, "print_questions": true},
				wantErr: "--print-questions needs questions of its own, " +
					"which -i request does not build",
			},
			{
				name:  "should accept the streaming flags with -i request",
				set:   values{"input": "request", "jobs": 4, "unordered": true, "stop_on_error": true, "skip_blank": true},
				typed: []settings.Name{"jobs"},
			},
			{
				name: "should reject both print flags at once",
				set:  values{"print_request": true, "print_questions": true, "input": "text"},
				wantErr: "--print-request and --print-questions each write a different " +
					"thing to stdout. Pass one",
			},
			{
				name:    "should reject -o with --print-questions",
				set:     values{"print_questions": true, "output": "json", "input": "text"},
				wantErr: "-o does not apply to --print-questions, which writes a question file",
			},
			{
				name:    "should reject -r with --print-request",
				set:     values{"print_request": true, "raw": true, "input": "text"},
				wantErr: "-r does not apply to --print-request, which writes a request body",
			},
			{
				name: "should name --merge-key when that is the spelling given",
				set:  values{"merge_key": "verdict", "print_request": true, "input": "text"},
				wantErr: "--merge-key needs answers to fold in, " +
					"which --print-request does not produce",
			},
			{
				name:    "should reject -q with --print-request",
				set:     values{"print_request": true, "quiet": true, "input": "text"},
				wantErr: "-q suppresses output, which leaves --print-request nothing to write",
			},
			{
				name: "should reject --stats with --print-questions",
				set:  values{"print_questions": true, "stats": true, "input": "text"},
				wantErr: "--stats has nothing to report with --print-questions, " +
					"which makes no request",
			},
			{
				name: "should reject --stats with --print-request under -i request",
				set:  values{"input": "request", "jobs": 1, "print_request": true, "stats": true},
				wantErr: "--stats has nothing to report with --print-request, " +
					"which makes no request",
			},
			{
				name: "should accept --stats on its own under -i request",
				set:  values{"input": "request", "jobs": 1, "stats": true},
			},
			{
				name:    "should blame the dry run rather than the mode for -o under -i request",
				set:     values{"input": "request", "jobs": 1, "print_request": true, "output": "json"},
				wantErr: "-o does not apply to --print-request, which writes a request body",
			},
			{
				name: "should blame the dry run rather than the mode for --usage under -i request",
				set:  values{"input": "request", "jobs": 1, "print_request": true, "usage": true},
				wantErr: "--usage reports the tokens a question cost, " +
					"which --print-request does not ask",
			},
			{
				name:    "should keep the request mode reason for a flag the dry run has no rule for",
				set:     values{"input": "request", "jobs": 1, "print_request": true},
				events:  []argv.Event{{Name: "ask", Value: "a=x"}},
				wantErr: "-i request carries its own questions. Drop --ask",
			},
			{
				name: "should reject --assert with -i request",
				set:  values{"assert": []string{"answer.value > 0.5"}, "input": "request", "jobs": 1},
				wantErr: "--assert does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name:    "should reject --assert with --list-models",
				set:     values{"assert": []string{"answer.value > 0.5"}, "list_models": true, "input": "text"},
				wantErr: "--assert judges an answer, which --list-models does not produce",
			},
			{
				name:    "should reject --assert with --print-request",
				set:     values{"assert": []string{"answer.value > 0.5"}, "print_request": true, "input": "text"},
				wantErr: "--assert judges an answer, which --print-request does not produce",
			},
			{
				name:  "should ignore a file's assertion with --print-request",
				set:   values{"print_request": true, "file": "q.yaml", "input": "text"},
				facts: settings.Facts{FileAssert: "answer.value > 0.5"},
			},
			{
				name:  "should ignore a file's two gates with --print-request",
				set:   values{"print_request": true, "file": "q.yaml", "input": "text"},
				facts: settings.Facts{FileAssert: "answer.value > 0.5", FileAbstainIf: "answer.value > 0.4"},
			},
			{
				name:    "should reject --assert typed beside a file's assertion with --print-request",
				set:     values{"assert": []string{"answer.value > 0.5"}, "print_request": true, "file": "q.yaml", "input": "text"},
				wantErr: "--assert judges an answer, which --print-request does not produce",
			},
			{
				name:    "should reject --abstain-if typed beside a file's gates with --print-request",
				set:     values{"abstain_if": []string{"answer.value > 0.4"}, "print_request": true, "file": "q.yaml", "input": "text"},
				facts:   settings.Facts{FileAssert: "answer.value > 0.5"},
				wantErr: "--abstain-if judges an answer, which --print-request does not produce",
			},
			{
				name: "should accept --assert with --print-questions",
				set:  values{"assert": []string{"answer.value > 0.5"}, "print_questions": true, "input": "text"},
			},
			{
				name: "should reject --stop-on-assert with --print-request",
				set:  values{"print_request": true, "stop_on_assert": true, "input": "lines"},
				wantErr: "--stop-on-assert ends a stream on a false assertion, " +
					"which --print-request does not produce",
			},
			{
				name:    "should reject --abstain-if with --list-models",
				set:     values{"abstain_if": []string{"answer.value > 0.4"}, "list_models": true, "input": "text"},
				wantErr: "--abstain-if judges an answer, which --list-models does not produce",
			},
			{
				name: "should reject --abstain-if with -i request",
				set:  values{"abstain_if": []string{"answer.value > 0.4"}, "input": "request", "jobs": 1},
				wantErr: "--abstain-if does not apply to -i request, " +
					"which forwards raw responses",
			},
			{
				name:    "should name --assert first when --print-request is given both gates",
				set:     values{"assert": []string{"answer.value > 0.5"}, "abstain_if": []string{"answer.value > 0.4"}, "print_request": true, "input": "text"},
				wantErr: "--assert judges an answer, which --print-request does not produce",
			},
			{
				name:    "should reject --abstain-if with --print-request",
				set:     values{"abstain_if": []string{"answer.value > 0.4"}, "print_request": true, "input": "text"},
				wantErr: "--abstain-if judges an answer, which --print-request does not produce",
			},
			{
				name: "should accept --abstain-if with --print-questions",
				set:  values{"assert": []string{"answer.value > 0.5"}, "abstain_if": []string{"answer.value > 0.4"}, "print_questions": true, "input": "text"},
			},
			{
				name: "should accept --stop-on-error with --print-request",
				set:  values{"print_request": true, "stop_on_error": true, "input": "lines"},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				given := run{set: tc.set, typed: tc.typed, events: tc.events, positional: tc.positional, facts: tc.facts}

				warnings, _, err := settings.Validate(given.view(t), settings.Rules())

				if tc.wantErr == "" {
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}

					if len(warnings) != 0 {
						t.Errorf("warnings = %q, want none", warnings)
					}

					return
				}

				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}
			})
		}
	})

	t.Run("should refuse and pass under each rule", func(t *testing.T) {
		t.Parallel()

		for _, tc := range ruleCases() {
			t.Run("should refuse under "+tc.rule, func(t *testing.T) {
				t.Parallel()

				_, _, err := settings.Validate(tc.refused.view(t), []settings.Rule{ruleByID(t, tc.rule)})
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}
			})

			t.Run("should pass under "+tc.rule, func(t *testing.T) {
				t.Parallel()

				if _, _, err := settings.Validate(tc.passed.view(t), []settings.Rule{ruleByID(t, tc.rule)}); err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			})
		}
	})

	t.Run("should cover every rule in Rules", func(t *testing.T) {
		t.Parallel()

		var covered []string
		for _, tc := range ruleCases() {
			covered = append(covered, tc.rule)
		}

		for _, rule := range settings.Rules() {
			if !slices.Contains(covered, rule.ID) {
				t.Errorf("rule %s has no refusing and passing case", rule.ID)
			}
		}
	})
}

func TestRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, rules []settings.Rule)
	}{
		{
			name: "should name real rows in every rule's Reads",
			check: func(t *testing.T, rules []settings.Rule) {
				t.Helper()

				for _, rule := range rules {
					for _, name := range rule.Reads {
						if _, found := settings.Lookup(name); !found {
							t.Errorf("rule %s reads %s, which no row is", rule.ID, name)
						}
					}
				}
			},
		},
		{
			name: "should give every rule its own ID",
			check: func(t *testing.T, rules []settings.Rule) {
				t.Helper()

				seen := map[string]bool{}
				for _, rule := range rules {
					if seen[rule.ID] {
						t.Errorf("rule %s is listed twice", rule.ID)
					}

					seen[rule.ID] = true
				}
			},
		},
		{
			name: "should give every rule something to do",
			check: func(t *testing.T, rules []settings.Rule) {
				t.Helper()

				for _, rule := range rules {
					if rule.Check == nil && rule.Warn == nil && rule.Notice == nil {
						t.Errorf("rule %s has no Check, Warn or Notice", rule.ID)
					}
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, settings.Rules())
		})
	}
}

func ruleCases() []ruleCase {
	return []ruleCase{
		{
			rule:    "list-models-alone",
			refused: run{set: values{"list_models": true}, typed: []settings.Name{"model"}},
			passed:  run{set: values{"list_models": true, "stats": true}},
			wantErr: "-m names a model to ask, which --list-models does not do",
		},
		{
			rule:    "dry-run-and-request",
			refused: run{set: values{"print_request": true, "mock": "answers.jsonl"}, fromEnv: []settings.Name{"mock"}},
			passed:  run{set: values{"print_request": true, "input": "request", "stop_on_error": true}},
			wantErr: "ONESIE_MOCK answers requests, and --print-request sends none. Unset it or drop --print-request",
		},
	}
}

type ruleCase struct {
	rule    string
	refused run
	passed  run
	wantErr string
}

func ruleByID(t *testing.T, id string) settings.Rule {
	t.Helper()

	for _, rule := range settings.Rules() {
		if rule.ID == id {
			return rule
		}
	}

	t.Fatalf("no rule %s", id)

	return settings.Rule{}
}

type run struct {
	set        values
	typed      []settings.Name
	fromEnv    []settings.Name
	events     []argv.Event
	positional string
	file       []plan.Question
	facts      settings.Facts
	planned    bool
}

func (r run) view(t *testing.T) settings.View {
	t.Helper()

	merged := &settings.Merged{}

	for name, value := range r.set {
		merged.Set(name, value, r.sourceOf(name))
	}

	for _, name := range slices.Concat(r.typed, r.fromEnv) {
		if _, set := r.set[name]; !set {
			merged.Set(name, nil, r.sourceOf(name))
		}
	}

	facts := r.facts
	facts.Events, facts.Positional = r.events, r.positional

	if !r.planned {
		return settings.NewView(merged, nil, facts)
	}

	built, err := plan.Assemble(plan.Source{
		Events: r.events, File: r.file, Positional: r.positional,
		ReadFile: func(string) ([]byte, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("assembling: %v", err)
	}

	return settings.NewView(merged, built, facts)
}

func (r run) sourceOf(name settings.Name) settings.Source {
	switch {
	case slices.Contains(r.typed, name):
		return settings.SourceFlag
	case slices.Contains(r.fromEnv, name):
		return settings.SourceEnv
	default:
		return settings.SourceDefault
	}
}

type values = map[settings.Name]any
