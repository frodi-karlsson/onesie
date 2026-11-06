package settings

import (
	"errors"
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

			return firstRefusal(v, modeRefusals(ModeListModels))
		},
	}
}

func dryRunAndRequestRule() Rule {
	return Rule{
		ID: "dry-run-and-request",
		Reads: readsOf([]Name{PrintRequest.Name(), PrintQuestions.Name(), Input.Name()},
			modeRefusals(ModeRequest), modeRefusals(ModePrintRequest), modeRefusals(ModePrintQuestions)),
		Check: func(v View) error {
			// A dry run forwards nothing, writes no response body and makes no request, which is what
			// every output facing reason in the request mode table claims. Its own message is the true
			// one there, so it answers first. Otherwise the request mode rules lead, so a flag -i
			// request refuses outright is named by the message that says so.
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

	return firstRefusal(v, modeRefusals(ModeRequest))
}

func dryRunRefusal(v View) error {
	switch {
	case Get(v.Merged, PrintRequest):
		return firstRefusal(v, modeRefusals(ModePrintRequest))
	case Get(v.Merged, PrintQuestions):
		return firstRefusal(v, modeRefusals(ModePrintQuestions))
	default:
		return nil
	}
}

func firstRefusal(v View, refusals []refusal) error {
	for _, rule := range refusals {
		if rule.given(v) {
			return errors.New(rule.message(v))
		}
	}

	return nil
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
	const mode = "--list-models"

	return []refusal{
		{"", positionalGiven, says("--list-models asks no question. Drop the question argument")},
		{"", anyEvent, func(v View) string {
			return "--list-models asks no question. Drop --" + v.Facts.Events[0].Name
		}},
		{File.Name(), filled(File), says("-f does not apply to --list-models, which asks no question")},
		{Replace.Name(), on(Replace), says("--replace applies to -f, which --list-models does not accept")},
		{State.Name(), typedRow(State.Name()), says("--state does not apply to --list-models, which reads no state")},
		{StateFile.Name(), typedRow(StateFile.Name()), says(
			"--state-file does not apply to --list-models, which reads no state")},
		{Input.Name(), typedRow(Input.Name()), says("-i does not apply to --list-models, which reads no input")},
		{Map.Name(), typedRow(Map.Name()), says("--map does not apply to --list-models, which reads no state")},
		{ID.Name(), typedRow(ID.Name()), says("--id does not apply to --list-models, which reads no input")},
		{Output.Name(), filled(Output), says("-o does not apply to --list-models, which writes a fixed listing")},
		{Raw.Name(), on(Raw), says("-r does not apply to --list-models, which writes a fixed listing")},
		{Quiet.Name(), on(Quiet), says("-q suppresses output, which leaves --list-models nothing to write")},
		{assertName, hasAssert, says("--assert judges an answer, which --list-models does not produce")},
		{abstainIfName, hasAbstainIf, says("--abstain-if judges an answer, which --list-models does not produce")},
		{Usage.Name(), on(Usage), says("--usage reports the tokens a question cost, which --list-models does not ask")},
		{MergeInput.Name(), merging, func(v View) string {
			return mergeName(v) + " needs answers to fold in, which --list-models does not produce"
		}},
		{Jobs.Name(), typedRow(Jobs.Name()), says("-j does not apply to --list-models, which makes one request")},
		{Model.Name(), typedRow(Model.Name()), says("-m names a model to ask, which --list-models does not do")},
		{Unordered.Name(), on(Unordered), says(
			"--unordered applies to streaming input, which --list-models does not read")},
		{StopOnError.Name(), on(StopOnError), says(
			"--stop-on-error applies to streaming input, which --list-models does not read")},
		{StopOnAssert.Name(), on(StopOnAssert), says(
			"--stop-on-assert applies to streaming input, which --list-models does not read")},
		{SkipBlank.Name(), on(SkipBlank), says(
			"--skip-blank applies to streaming input, which --list-models does not read")},
		{PrintRequest.Name(), on(PrintRequest), says(
			"--print-request and --list-models each write a different thing to stdout. Pass one")},
		{PrintQuestions.Name(), on(PrintQuestions), says(
			"--print-questions and --list-models each write a different thing to stdout. Pass one")},
		{Mock.Name(), mocked, func(v View) string {
			return fmt.Sprintf("--list-models asks no question, so %s has nothing to answer. %s",
				mockSpelled(v), mockAdvice(v, mode))
		}},
		{Cache.Name(), typedOn(Cache), says("--list-models asks no question, so --cache has nothing to store. Drop one")},
	}
}

func requestRefusals() []refusal {
	const mode = "-i request"

	return []refusal{
		{"", positionalGiven, says("-i request carries its own questions. Drop the question argument")},
		{"ask", event("ask"), says("-i request carries its own questions. Drop --ask")},
		{File.Name(), filled(File), says("-f does not apply to -i request, which carries its own questions")},
		{"for", event("for"), says("-i request carries its own questions. Drop --for")},
		{Replace.Name(), on(Replace), says("--replace applies to -f, which -i request does not accept")},
		{"pick", event("pick"), says("-i request carries its own questions. Drop --pick")},
		{"rate", event("rate"), says("-i request carries its own questions. Drop --rate")},
		{"box", event("box"), says("-i request carries its own questions. Drop --box")},
		{"desc", event("desc"), says("-i request carries its own questions. Drop --desc")},
		{"sep", event("sep"), says("-i request carries its own questions. Drop --sep")},
		{"threshold", event("threshold"), says("--threshold does not apply to -i request, which carries no policy")},
		{"min_confidence", event("min-confidence"), says(
			"--min-confidence does not apply to -i request, which carries no policy")},
		{"fallback", event("fallback"), says("--fallback does not apply to -i request, which carries no policy")},
		{State.Name(), typedRow(State.Name()), says(
			"--state does not apply to -i request, whose bodies carry their own state")},
		{StateFile.Name(), typedRow(StateFile.Name()), says(
			"--state-file does not apply to -i request, whose bodies carry their own state")},
		{imageName, listed(imageName), says("--image does not apply to -i request, whose bodies carry their own state")},
		{imageFieldName, listed(imageFieldName), says(
			"--image-field does not apply to -i request, whose bodies carry their own state")},
		{Map.Name(), typedRow(Map.Name()), says("--map does not apply to -i request, whose bodies carry their own state")},
		{ID.Name(), typedRow(ID.Name()), says("--id does not apply to -i request, which forwards raw responses")},
		{Output.Name(), filled(Output), says("-o does not apply to -i request, which forwards raw responses")},
		{Raw.Name(), on(Raw), says("-r does not apply to -i request, which forwards raw responses")},
		{Quiet.Name(), on(Quiet), says("-q needs a policy to report, which -i request has none of")},
		{assertName, hasAssert, says("--assert does not apply to -i request, which forwards raw responses")},
		{abstainIfName, hasAbstainIf, says("--abstain-if does not apply to -i request, which forwards raw responses")},
		{Usage.Name(), on(Usage), says("--usage does not apply to -i request, whose response bodies already carry usage")},
		{MergeInput.Name(), merging, func(v View) string {
			return mergeName(v) + " does not apply to -i request, which forwards raw responses"
		}},
		{Model.Name(), typedRow(Model.Name()), says("-m does not apply to -i request, whose bodies carry their own model")},
		{StopOnAssert.Name(), on(StopOnAssert), says(
			"--stop-on-assert does not apply to -i request, whose bodies carry no assertion")},
		{PrintQuestions.Name(), on(PrintQuestions), says(
			"--print-questions needs questions of its own, which -i request does not build")},
		{Mock.Name(), mocked, func(v View) string {
			return fmt.Sprintf("-i request sends each body as written, so %s has nothing to answer. %s",
				mockSpelled(v), mockAdvice(v, mode))
		}},
		{Cache.Name(), typedOn(Cache), says("-i request sends each body as written, so --cache has nothing to store. Drop one")},
	}
}

func printRequestRefusals() []refusal {
	const mode, writes = "--print-request", "a request body"

	return append([]refusal{
		{Mock.Name(), mocked, func(v View) string {
			return fmt.Sprintf("%s answers requests, and --print-request sends none. %s",
				mockSpelled(v), mockAdvice(v, mode))
		}},
		{PrintQuestions.Name(), on(PrintQuestions), says(
			"--print-request and --print-questions each write a different thing to stdout. Pass one")},
		{Output.Name(), filled(Output), says("-o does not apply to " + mode + ", which writes " + writes)},
		{Raw.Name(), on(Raw), says("-r does not apply to " + mode + ", which writes " + writes)},
		{Quiet.Name(), on(Quiet), says("-q suppresses output, which leaves " + mode + " nothing to write")},
		// A gate only the file carries is left alone, so a gated file can be dry run as it stands.
		{assertName, listed(assertName), func(v View) string {
			return assertFlag(v) + " judges an answer, which --print-request does not produce"
		}},
		{abstainIfName, listed(abstainIfName), func(v View) string {
			return abstainFlag(v) + " judges an answer, which --print-request does not produce"
		}},
		// The same holds for a policy: a file's is left alone, and a typed one would have no effect.
		{"threshold", event("threshold"), says("--threshold sets a policy for an answer, which " + mode + " does not produce")},
		{"min_confidence", event("min-confidence"), says(
			"--min-confidence sets a policy for an answer, which " + mode + " does not produce")},
		{"fallback", event("fallback"), says("--fallback sets a policy for an answer, which " + mode + " does not produce")},
		{Cache.Name(), typedOn(Cache), says("--cache stores answers, which " + mode + " does not produce")},
		{Usage.Name(), on(Usage), says("--usage reports the tokens a question cost, which " + mode + " does not ask")},
		{MergeInput.Name(), merging, func(v View) string {
			return mergeName(v) + " needs answers to fold in, which " + mode + " does not produce"
		}},
		// --stop-on-error and --unordered are absent because a dry run stream honours both. Only the
		// assertion is a judgment a dry run never makes, so only its stop condition is refused.
		{StopOnAssert.Name(), on(StopOnAssert), says(
			"--stop-on-assert ends a stream on a false assertion, which --print-request does not produce")},
	}, statsRefusal(mode))
}

func printQuestionsRefusals() []refusal {
	const mode, writes = "--print-questions", "a question file"

	return append([]refusal{
		{State.Name(), typedRow(State.Name()), says("--state does not apply to " + mode + ", which reads no state")},
		{StateFile.Name(), typedRow(StateFile.Name()), says(
			"--state-file does not apply to " + mode + ", which reads no state")},
		{Input.Name(), typedRow(Input.Name()), says("-i does not apply to " + mode + ", which reads no input")},
		{Map.Name(), typedRow(Map.Name()), says("--map does not apply to " + mode + ", which reads no state")},
		{ID.Name(), typedRow(ID.Name()), says("--id does not apply to " + mode + ", which reads no input")},
		{Output.Name(), filled(Output), says("-o does not apply to " + mode + ", which writes " + writes)},
		{Raw.Name(), on(Raw), says("-r does not apply to " + mode + ", which writes " + writes)},
		{Quiet.Name(), on(Quiet), says("-q suppresses output, which leaves " + mode + " nothing to write")},
		{Usage.Name(), on(Usage), says("--usage reports the tokens a question cost, which " + mode + " does not ask")},
		{MergeInput.Name(), merging, func(v View) string {
			return mergeName(v) + " needs answers to fold in, which " + mode + " does not produce"
		}},
		{Jobs.Name(), typedRow(Jobs.Name()), says("-j does not apply to " + mode + ", which makes no request")},
		{Model.Name(), typedRow(Model.Name()), says("-m names a model to ask, which " + mode + " does not do")},
	}, statsRefusal(mode))
}

func statsRefusal(mode string) refusal {
	return refusal{Stats.Name(), on(Stats), says("--stats has nothing to report with " + mode + ", which makes no request")}
}

type refusal struct {
	setting Name
	given   func(View) bool
	message func(View) string
}

func says(message string) func(View) string {
	return func(View) string { return message }
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
