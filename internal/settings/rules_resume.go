package settings

import (
	"errors"
	"fmt"
)

func resumeRule() Rule {
	return Rule{
		ID:    "resume-needs-a-stream",
		Check: checkResume,
	}
}

func checkResume(v View) error {
	if !Get(v.Merged, Resume) {
		return nil
	}

	switch {
	case Get(v.Merged, ListModels):
		return errors.New("--resume applies to streaming input, which --list-models does not read")
	case Get(v.Merged, PrintQuestions):
		return errors.New("--resume applies to streaming input, which --print-questions does not read")
	case Get(v.Merged, PrintRequest):
		return errors.New("--print-request writes request bodies, not answers, " +
			"so --resume has nothing to pick up. Drop --resume")
	case !inputModeOf(v).Streaming():
		return fmt.Errorf("--resume applies to streaming input. -i %s reads one record", inputName(v))
	case Get(v.Merged, Unordered) && !typed(v, ID.Name()):
		return errors.New("--resume relies on input order, which --unordered gives up. " +
			"Pass --id to resume by id")
	case Get(v.Merged, Output) == "raw":
		return errors.New("--resume needs output that keeps each record's outcome, " +
			"which raw lines do not. Use -o values or -o json")
	case markdownOutput(v):
		return errors.New("--resume reads each record's outcome back from --out, " +
			"which a markdown table does not keep. Use -o json, values, csv or tsv")
	default:
		return nil
	}
}
