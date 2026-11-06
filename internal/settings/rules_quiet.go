package settings

import (
	"errors"
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

func quietWithMarkdownRule() Rule {
	return Rule{
		ID:    "quiet-without-markdown",
		Reads: []Name{Quiet.Name(), Output.Name()},
		Check: func(v View) error {
			if Get(v.Merged, Quiet) && markdownOutput(v) {
				return fmt.Errorf("-q suppresses output, which leaves -o %s nothing to write", outputTyped(v))
			}

			return nil
		},
	}
}

func markdownOutput(v View) bool {
	return Get(v.Merged, Output) == "markdown"
}

func outputTyped(v View) string {
	// The merge holds an alias by the mode it names, and a message names -o as it was typed.
	if typed := v.Facts.Given.Values(Output.Name()); len(typed) > 0 {
		return typed[len(typed)-1]
	}

	return Get(v.Merged, Output)
}

func quietWritesNothingRule() Rule {
	return Rule{
		ID:    "quiet-writes-nothing",
		Reads: []Name{Quiet.Name(), Raw.Name(), Output.Name(), Out.Name(), MergeInput.Name(), MergeKey.Name()},
		Check: func(v View) error {
			if !Get(v.Merged, Quiet) {
				return nil
			}

			if dropped := droppedByQuiet(v); dropped != "" {
				return fmt.Errorf("-q suppresses output, which leaves %s nothing to write", dropped)
			}

			return nil
		},
	}
}

func droppedByQuiet(v View) string {
	switch {
	case Get(v.Merged, Raw):
		return "-r"
	case Get(v.Merged, Output) != "":
		return "-o " + outputTyped(v)
	case Get(v.Merged, Out) != "":
		return "--out"
	default:
		return mergeName(v)
	}
}

func quietWithUsageRule() Rule {
	return Rule{
		ID:    "quiet-without-usage",
		Reads: []Name{Quiet.Name(), Usage.Name()},
		Check: func(v View) error {
			if Get(v.Merged, Usage) && Get(v.Merged, Quiet) {
				return errors.New("--usage does not apply to -q, which suppresses output")
			}

			return nil
		},
	}
}

func quietOnStreamRule() Rule {
	return Rule{
		ID:    "quiet-reads-one-record",
		Reads: []Name{Quiet.Name(), Input.Name()},
		Check: func(v View) error {
			if inputModeOf(v).Streaming() && Get(v.Merged, Quiet) {
				return fmt.Errorf("-q reads one record. Drop -i %s or use -o values and filter the stream", inputName(v))
			}

			return nil
		},
	}
}

func quietNeedsAGateRule() Rule {
	return Rule{
		ID:    "quiet-needs-one-question",
		Reads: []Name{Quiet.Name(), assertName, "min_confidence", "fallback"},
		Check: func(v View) error {
			// Beside an assertion -q only silences the output, since the assertion is the gate.
			if v.Plan == nil || !Get(v.Merged, Quiet) || hasAssert(v) {
				return nil
			}

			if len(v.Plan.Questions) != 1 {
				return fmt.Errorf("-q needs a single question, or --assert to gate several. %s were asked",
					quotedIDs(v.Plan.Questions))
			}

			only := v.Plan.Questions[0]
			if only.Shape == plan.Box {
				return fmt.Errorf(
					"-q on '%s' needs %s. A box has no policy that could fail it",
					only.ID, plan.Spelling(only.Origin, "--assert"))
			}

			if only.Shape != plan.Noul && only.Policy.MinConfidence == nil {
				return fmt.Errorf(
					"-q on '%s' needs %s and %s, or --assert. Without one the exit code is always 0",
					only.ID, plan.Spelling(only.Origin, "--min-confidence"), plan.Spelling(only.Origin, "--fallback"))
			}

			return nil
		},
	}
}
