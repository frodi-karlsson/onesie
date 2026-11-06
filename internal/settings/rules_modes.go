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
		if rule.given(v) {
			return fmt.Errorf("%s does not apply to %s, which %s", rule.spelled(v), name, reason)
		}
	}

	return nil
}

func modeWording(mode Mode) (name, reason string) {
	switch mode {
	case ModeListModels:
		return "--list-models", "only lists the models a provider serves"
	case ModeRequest:
		return "-i request", "sends each body as written"
	case ModePrintRequest:
		return "--print-request", "writes the request bodies and sends none"
	case ModePrintQuestions:
		return "--print-questions", "writes the questions as a question file and sends nothing"
	default:
		return "", ""
	}
}

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
	// In a fixed order, so a command line with several offenders reports a predictable one.
	switch mode {
	case ModeListModels:
		return listModelsRefusals()
	case ModeRequest:
		return requestRefusals()
	case ModePrintRequest:
		return printRequestRefusals()
	case ModePrintQuestions:
		return printQuestionsRefusals()
	default:
		return nil
	}
}

func listModelsRefusals() []refusal {
	return []refusal{
		{"", positionalGiven, spelled("the question argument")},
		{"", anyEvent, func(v View) string { return "--" + v.Facts.Events[0].Name }},
		{File.Name(), filled(File), spelled("-f")},
		{Replace.Name(), on(Replace), spelled("--replace")},
		{State.Name(), typedRow(State.Name()), spelled("--state")},
		{StateFile.Name(), typedRow(StateFile.Name()), spelled("--state-file")},
		{Input.Name(), typedRow(Input.Name()), spelled("-i")},
		{Map.Name(), typedRow(Map.Name()), spelled("--map")},
		{ID.Name(), typedRow(ID.Name()), spelled("--id")},
		{Output.Name(), filled(Output), spelled("-o")},
		{Raw.Name(), on(Raw), spelled("-r")},
		{Quiet.Name(), on(Quiet), spelled("-q")},
		{assertName, hasAssert, spelled("--assert")},
		{abstainIfName, hasAbstainIf, spelled("--abstain-if")},
		{Usage.Name(), on(Usage), spelled("--usage")},
		{MergeInput.Name(), merging, mergeName},
		{Jobs.Name(), typedRow(Jobs.Name()), spelled("-j")},
		{Model.Name(), typedRow(Model.Name()), spelled("-m")},
		{Unordered.Name(), on(Unordered), spelled("--unordered")},
		{StopOnError.Name(), on(StopOnError), spelled("--stop-on-error")},
		{StopOnAssert.Name(), on(StopOnAssert), spelled("--stop-on-assert")},
		{SkipBlank.Name(), on(SkipBlank), spelled("--skip-blank")},
		{PrintRequest.Name(), on(PrintRequest), spelled("--print-request")},
		{PrintQuestions.Name(), on(PrintQuestions), spelled("--print-questions")},
		{Mock.Name(), mocked, mockSpelled},
		{Cache.Name(), typedOn(Cache), spelled("--cache")},
	}
}

func requestRefusals() []refusal {
	return []refusal{
		{"", positionalGiven, spelled("the question argument")},
		{"ask", event("ask"), spelled("--ask")},
		{File.Name(), filled(File), spelled("-f")},
		{"for", event("for"), spelled("--for")},
		{Replace.Name(), on(Replace), spelled("--replace")},
		{"pick", event("pick"), spelled("--pick")},
		{"rate", event("rate"), spelled("--rate")},
		{"box", event("box"), spelled("--box")},
		{"desc", event("desc"), spelled("--desc")},
		{"sep", event("sep"), spelled("--sep")},
		{"threshold", event("threshold"), spelled("--threshold")},
		{"min_confidence", event("min-confidence"), spelled("--min-confidence")},
		{"fallback", event("fallback"), spelled("--fallback")},
		{State.Name(), typedRow(State.Name()), spelled("--state")},
		{StateFile.Name(), typedRow(StateFile.Name()), spelled("--state-file")},
		{imageName, listed(imageName), spelled("--image")},
		{imageFieldName, listed(imageFieldName), spelled("--image-field")},
		{Map.Name(), typedRow(Map.Name()), spelled("--map")},
		{ID.Name(), typedRow(ID.Name()), spelled("--id")},
		{Output.Name(), filled(Output), spelled("-o")},
		{Raw.Name(), on(Raw), spelled("-r")},
		{Quiet.Name(), on(Quiet), spelled("-q")},
		{assertName, hasAssert, spelled("--assert")},
		{abstainIfName, hasAbstainIf, spelled("--abstain-if")},
		{Usage.Name(), on(Usage), spelled("--usage")},
		{MergeInput.Name(), merging, mergeName},
		{Model.Name(), typedRow(Model.Name()), spelled("-m")},
		{StopOnAssert.Name(), on(StopOnAssert), spelled("--stop-on-assert")},
		{PrintQuestions.Name(), on(PrintQuestions), spelled("--print-questions")},
		{Mock.Name(), mocked, mockSpelled},
		{Cache.Name(), typedOn(Cache), spelled("--cache")},
	}
}

func printRequestRefusals() []refusal {
	return []refusal{
		{Mock.Name(), mocked, mockSpelled},
		{PrintQuestions.Name(), on(PrintQuestions), spelled("--print-questions")},
		{Output.Name(), filled(Output), spelled("-o")},
		{Raw.Name(), on(Raw), spelled("-r")},
		{Quiet.Name(), on(Quiet), spelled("-q")},
		// A gate or a policy only the file carries is left alone, so a gated file can be dry run as it
		// stands. A typed one would have no effect.
		{assertName, listed(assertName), assertFlag},
		{abstainIfName, listed(abstainIfName), abstainFlag},
		{"threshold", event("threshold"), spelled("--threshold")},
		{"min_confidence", event("min-confidence"), spelled("--min-confidence")},
		{"fallback", event("fallback"), spelled("--fallback")},
		{Cache.Name(), typedOn(Cache), spelled("--cache")},
		{Usage.Name(), on(Usage), spelled("--usage")},
		{MergeInput.Name(), merging, mergeName},
		// --stop-on-error and --unordered are absent because a dry run stream honours both. Only the
		// assertion is a judgment a dry run never makes, so only its stop condition is refused.
		{StopOnAssert.Name(), on(StopOnAssert), spelled("--stop-on-assert")},
		{Stats.Name(), on(Stats), spelled("--stats")},
	}
}

func printQuestionsRefusals() []refusal {
	return []refusal{
		{State.Name(), typedRow(State.Name()), spelled("--state")},
		{StateFile.Name(), typedRow(StateFile.Name()), spelled("--state-file")},
		{Input.Name(), typedRow(Input.Name()), spelled("-i")},
		{Map.Name(), typedRow(Map.Name()), spelled("--map")},
		{ID.Name(), typedRow(ID.Name()), spelled("--id")},
		{Output.Name(), filled(Output), spelled("-o")},
		{Raw.Name(), on(Raw), spelled("-r")},
		{Quiet.Name(), on(Quiet), spelled("-q")},
		{Usage.Name(), on(Usage), spelled("--usage")},
		{MergeInput.Name(), merging, mergeName},
		{Jobs.Name(), typedRow(Jobs.Name()), spelled("-j")},
		{Model.Name(), typedRow(Model.Name()), spelled("-m")},
		{Stats.Name(), on(Stats), spelled("--stats")},
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

func on(k Key[bool]) func(View) bool {
	return func(v View) bool { return Get(v.Merged, k) }
}

func typedOn(k Key[bool]) func(View) bool {
	return func(v View) bool { return typed(v, k.Name()) && Get(v.Merged, k) }
}

func filled(k Key[string]) func(View) bool {
	return func(v View) bool { return Get(v.Merged, k) != "" }
}

func typedRow(name Name) func(View) bool {
	return func(v View) bool { return typed(v, name) }
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
