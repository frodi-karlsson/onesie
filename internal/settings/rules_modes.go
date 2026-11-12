package settings

import (
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie/internal/input"
)

func listModelsRule() Rule {
	return Rule{
		ID: "list-models-alone",
		Check: func(v View) error {
			if !Get(v.Merged, ListModels) {
				return nil
			}

			return firstRefusal(v, settingrow.ModeListModels)
		},
	}
}

func dryRunAndRequestRule() Rule {
	return Rule{
		ID: "dry-run-and-request",
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

	return firstRefusal(v, settingrow.ModeRequest)
}

func dryRunRefusal(v View) error {
	switch {
	case Get(v.Merged, PrintRequest):
		return firstRefusal(v, settingrow.ModePrintRequest)
	case Get(v.Merged, PrintQuestions):
		return firstRefusal(v, settingrow.ModePrintQuestions)
	default:
		return nil
	}
}

func firstRefusal(v View, mode settingrow.Mode) error {
	name, reason := modeWording(mode)

	for _, rule := range modeRefusals(mode) {
		if !rule.given(v) {
			continue
		}

		// A judgment the dry run cannot show has a run that can, so the refusal names it.
		if mode == settingrow.ModePrintRequest && judging(rule.setting) {
			return fmt.Errorf("%s does not apply to %s, which %s. %s", rule.spelled(v), name, reason, rehearse)
		}

		return fmt.Errorf("%s does not apply to %s, which %s", rule.spelled(v), name, reason)
	}

	return nil
}

func modeWording(mode settingrow.Mode) (name, reason string) {
	switch mode {
	case settingrow.ModeListModels:
		return "--list-models", "only lists the models a provider serves"
	case settingrow.ModeRequest:
		return "-i request", "sends each body as written and prints each raw response"
	case settingrow.ModePrintRequest:
		return "--print-request", "writes the request bodies and sends none"
	case settingrow.ModePrintQuestions:
		return "--print-questions", "reads no input and writes the questions as a question file"
	default:
		return "", ""
	}
}

const rehearse = "Rehearse the whole command with --mock, or see the questions with --print-questions"

func modeRefusals(mode settingrow.Mode) []refusal {
	switch mode {
	case settingrow.ModeListModels, settingrow.ModeRequest, settingrow.ModePrintRequest, settingrow.ModePrintQuestions:
	default:
		return nil
	}

	var rules []refusal
	if mode == settingrow.ModeListModels || mode == settingrow.ModeRequest {
		rules = append(rules, refusal{"", positionalGiven, spelled("the question argument")})
	}
	if mode == settingrow.ModeListModels {
		rules = append(rules, refusal{"", anyEvent, func(v View) string { return "--" + v.Facts.Events[0].Name }})
	}

	rows := settingrow.Table()
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

func refusalOrder(mode settingrow.Mode) []settingrow.Name {
	if mode == settingrow.ModePrintRequest {
		return []settingrow.Name{
			"mock", "print_questions", "output", "quiet", "assert", "abstain_if",
			"threshold", "min_confidence", "fallback", "cache", "usage", "merge", "stop_on", "stats",
		}
	}

	return []settingrow.Name{
		"ask", "file", "for", "replace", "pick", "rate", "box", "desc", "sep", "threshold",
		"min_confidence", "fallback", "state", "input", "image", "image_field", "map", "id",
		"output", "quiet", "assert", "abstain_if", "usage", "merge", "jobs", "model", "unordered",
		"stop_on", "skip_blank", "print_request", "print_questions", "stats", "mock", "cache",
	}
}

func appendModeRefusal(rules []refusal, row settingrow.Row, mode settingrow.Mode) []refusal {
	bound, registered := row.FlagOn(settingrow.CommandRoot)

	// --stop-on=error works under -i request and --print-request, but neither asks a gate, so no record
	// there can fail or be unsure.
	if registered && row.Name == StopOn.Name() && row.Modes.Has(mode) {
		return append(rules, refusal{row.Name, stopsOnGate, gateStopSpelled})
	}

	if !registered || row.Modes.Has(mode) && (mode != settingrow.ModeRequest || row.Name != Model.Name()) {
		return rules
	}

	given, name := modeException(row, bound, mode)
	return append(rules, refusal{row.Name, func(v View) bool {
		return typed(v, row.Name) || given(v)
	}, name})
}

func stopsOnGate(v View) bool {
	return StopsOn(v.Merged, settingrow.StopOnFail) || StopsOn(v.Merged, settingrow.StopOnUnsure)
}

func gateStopSpelled(v View) string {
	if StopsOn(v.Merged, settingrow.StopOnFail) {
		return "--stop-on=" + settingrow.StopOnFail
	}

	return "--stop-on=" + settingrow.StopOnUnsure
}

func modeException(row settingrow.Row, bound settingrow.BoundFlag, mode settingrow.Mode) (func(View) bool, func(View) string) {
	name := spelled(spelling(bound))
	if row.Flag.Group {
		return event(bound.Long), name
	}

	switch row.Name {
	case File.Name(), Output.Name(), StopOn.Name():
		return func(v View) bool { return Get(v.Merged, Key[string]{name: row.Name}) != "" }, name
	case Replace.Name(), Quiet.Name(), Usage.Name(), Unordered.Name(),
		SkipBlank.Name(), PrintRequest.Name(), PrintQuestions.Name(), Stats.Name():
		return func(v View) bool { return Get(v.Merged, Key[bool]{name: row.Name}) }, name
	case assertName, abstainIfName:
		if mode == settingrow.ModePrintRequest {
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
	default:
		return func(View) bool { return false }, name
	}
}

func judging(setting settingrow.Name) bool {
	switch setting {
	case assertName, abstainIfName, "threshold", "min_confidence", "fallback":
		return true
	default:
		return false
	}
}

type refusal struct {
	setting settingrow.Name
	given   func(View) bool
	spelled func(View) string
}

func spelled(flag string) func(View) string {
	return func(View) string { return flag }
}

func listed(name settingrow.Name) func(View) bool {
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
