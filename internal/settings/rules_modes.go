package settings

import (
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/input"
)

func listModelsRule() Rule {
	return Rule{
		ID:    "list-models-alone",
		Reads: readsOf(append([]Name{ListModels.Name()}, groupNames()...), modeRefusals(ModeListModels)),
		Check: func(v View) error {
			if !Get(v.Merged, ListModels) {
				return nil
			}

			return firstRefusal(v, ModeListModels)
		},
	}
}

func dryRunAndRequestRule() Rule {
	return Rule{
		ID: "dry-run-and-request",
		Reads: readsOf([]Name{PrintRequest.Name(), PrintQuestions.Name(), Input.Name()},
			modeRefusals(ModeRequest), modeRefusals(ModePrintRequest), modeRefusals(ModePrintQuestions)),
		Check: func(v View) error {
			// A dry run writes no response body and makes no request, so beside -i request it decides
			// what the run does, and its refusals answer first. Otherwise the request mode rules lead, so
			// a flag -i request refuses outright is named against -i request.
			checks := []func(View) error{requestModeRefusal, dryRunRefusal}
			if Get(v.Merged, PrintRequest) {
				checks = []func(View) error{dryRunRefusal, requestModeRefusal}
			}

			for _, check := range checks {
				if err := check(v); err != nil {
					return err
				}
			}

			return nil
		},
	}
}

func requestModeRefusal(v View) error {
	if inputModeOf(v) != input.Request {
		return nil
	}

	return firstRefusal(v, ModeRequest)
}

func dryRunRefusal(v View) error {
	switch {
	case Get(v.Merged, PrintRequest):
		return firstRefusal(v, ModePrintRequest)
	case Get(v.Merged, PrintQuestions):
		return firstRefusal(v, ModePrintQuestions)
	default:
		return nil
	}
}

func firstRefusal(v View, mode Mode) error {
	name, reason := modeWording(mode)

	for _, rule := range modeRefusals(mode) {
		if !rule.given(v) {
			continue
		}

		// A judgment the dry run cannot show has a run that can, so the refusal names it.
		if mode == ModePrintRequest && judging(rule.setting) {
			return fmt.Errorf("%s does not apply to %s, which %s. %s", rule.spelled(v), name, reason, rehearse)
		}

		return fmt.Errorf("%s does not apply to %s, which %s", rule.spelled(v), name, reason)
	}

	return nil
}

func modeWording(mode Mode) (name, reason string) {
	switch mode {
	case ModeListModels:
		return "--list-models", "only lists the models a provider serves"
	case ModeRequest:
		return "-i request", "sends each body as written and prints each raw response"
	case ModePrintRequest:
		return "--print-request", "writes the request bodies and sends none"
	case ModePrintQuestions:
		return "--print-questions", "reads no input and writes the questions as a question file"
	default:
		return "", ""
	}
}

const rehearse = "Rehearse the whole command with --mock, or see the questions with --print-questions"

func readsOf(reads []Name, tables ...[]refusal) []Name {
	for _, table := range tables {
		for _, rule := range table {
			switch rule.setting {
			case "":
			case MergeInput.Name():
				reads = append(reads, MergeInput.Name(), MergeKey.Name())
			default:
				reads = append(reads, rule.setting)
			}
		}
	}

	return reads
}

func groupNames() []Name {
	var names []Name

	for _, row := range Table() {
		if row.Flag.Group {
			names = append(names, row.Name)
		}
	}

	return names
}

func modeRefusals(mode Mode) []refusal {
	switch mode {
	case ModeListModels, ModeRequest, ModePrintRequest, ModePrintQuestions:
	default:
		return nil
	}

	var rules []refusal
	if mode == ModeListModels || mode == ModeRequest {
		rules = append(rules, refusal{"", positionalGiven, spelled("the question argument")})
	}
	if mode == ModeListModels {
		rules = append(rules, refusal{"", anyEvent, func(v View) string { return "--" + v.Facts.Events[0].Name }})
	}

	rows := Table()
	for _, name := range refusalOrder(mode) {
		for i, row := range rows {
			if row.Name == name {
				rules = appendModeRefusal(rules, row, mode)
				rows = append(rows[:i], rows[i+1:]...)
				break
			}
		}
	}
	for _, row := range rows {
		rules = appendModeRefusal(rules, row, mode)
	}

	return rules
}

func refusalOrder(mode Mode) []Name {
	if mode == ModePrintRequest {
		return []Name{
			"mock", "print_questions", "output", "raw", "quiet", "assert", "abstain_if",
			"threshold", "min_confidence", "fallback", "cache", "usage", "merge", "stop_on_assert", "stats",
		}
	}

	return []Name{
		"ask", "file", "for", "replace", "pick", "rate", "box", "desc", "sep", "threshold",
		"min_confidence", "fallback", "state", "state_file", "input", "image", "image_field", "map", "id",
		"output", "raw", "quiet", "assert", "abstain_if", "usage", "merge", "jobs", "model", "unordered",
		"stop_on_error", "stop_on_assert", "skip_blank", "print_request", "print_questions", "stats", "mock", "cache",
	}
}

func appendModeRefusal(rules []refusal, row Row, mode Mode) []refusal {
	bound, registered := row.FlagOn(CommandRoot)
	if !registered || row.Modes.Has(mode) && (mode != ModeRequest || row.Name != Model.Name()) {
		return rules
	}

	given, name := modeException(row, bound, mode)
	return append(rules, refusal{row.Name, func(v View) bool {
		return typed(v, row.Name) || given(v)
	}, name})
}

func modeException(row Row, bound BoundFlag, mode Mode) (func(View) bool, func(View) string) {
	name := spelled(spelling(bound))
	if row.Flag.Group {
		return event(bound.Long), name
	}

	switch row.Name {
	case File.Name(), Output.Name():
		return func(v View) bool { return Get(v.Merged, Key[string]{name: row.Name}) != "" }, name
	case Replace.Name(), Raw.Name(), Quiet.Name(), Usage.Name(), Unordered.Name(), StopOnError.Name(),
		StopOnAssert.Name(), SkipBlank.Name(), PrintRequest.Name(), PrintQuestions.Name(), Stats.Name():
		return func(v View) bool { return Get(v.Merged, Key[bool]{name: row.Name}) }, name
	case assertName, abstainIfName:
		if mode == ModePrintRequest {
			return listed(row.Name), name
		}
		return func(v View) bool { return len(listOf(v, row.Name)) > 0 }, name
	case MergeInput.Name():
		return merging, func(v View) string {
			if merging(v) {
				return mergeName(v)
			}
			return spelling(bound)
		}
	case imageName, imageFieldName:
		return listed(row.Name), name
	case Mock.Name():
		return mocked, mockSpelled
	default:
		return func(View) bool { return false }, name
	}
}

func judging(setting Name) bool {
	switch setting {
	case assertName, abstainIfName, "threshold", "min_confidence", "fallback":
		return true
	default:
		return false
	}
}

type refusal struct {
	setting Name
	given   func(View) bool
	spelled func(View) string
}

func spelled(flag string) func(View) string {
	return func(View) string { return flag }
}

func listed(name Name) func(View) bool {
	return func(v View) bool { return len(listOf(v, name)) > 0 && v.Merged.Source(name) != SourceFile }
}

func event(flag string) func(View) bool {
	return func(v View) bool { return eventGiven(v, flag) }
}

func positionalGiven(v View) bool {
	return v.Facts.Positional != ""
}

func anyEvent(v View) bool {
	return len(v.Facts.Events) > 0
}

func mocked(v View) bool {
	return mockSpelled(v) != ""
}
