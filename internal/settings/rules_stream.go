package settings

import (
	"errors"
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie/internal/input"
)

const lineField = "line"

func singleRecordRule() Rule {
	return Rule{
		ID:    "streaming-flags-need-a-stream",
		Check: singleRecordRefusal,
	}
}

func singleRecordRefusal(v View) error {
	if inputModeOf(v).Streaming() {
		return nil
	}

	for _, flag := range []struct {
		name string
		set  bool
	}{
		{"--unordered", Get(v.Merged, Unordered)},
		{"--stop-on", Get(v.Merged, StopOn) != ""},
		{"--skip-blank", Get(v.Merged, SkipBlank)},
		{"--no-dedup", Get(v.Merged, NoDedup)},
		{"--id", typed(v, ID.Name())},
		{"--image-field", len(listOf(v, imageFieldName)) > 0},
	} {
		if flag.set {
			return fmt.Errorf("%s applies to streaming input. -i %s reads one record", flag.name, inputName(v))
		}
	}

	if typed(v, Jobs.Name()) && Get(v.Merged, Jobs) > 1 {
		return fmt.Errorf("-j applies to streaming input. -i %s reads one record. "+
			"Pass -i jsonl, -i lines, -i csv or -i tsv, or drop -j", inputName(v))
	}

	if typed(v, Input.Name()) && len(listOf(v, imageName)) > 0 && !typed(v, State.Name()) && !typed(v, StateFile.Name()) {
		return fmt.Errorf("-i %s holds one record, which beside --image reads stdin only through "+
			"--state -. Pass --state - to read the state from stdin", inputName(v))
	}

	return nil
}

func streamStateRule() Rule {
	return Rule{
		ID: "stream-reads-its-own-state",
		Check: func(v View) error {
			mode := inputModeOf(v)
			if !mode.Streaming() {
				return nil
			}

			// The flag the user typed and the mode they gave. Naming --state for a --state-file mistake
			// sends them to the wrong flag, and naming jsonl for a lines run sends them to the wrong mode.
			switch {
			case typed(v, State.Name()):
				return fmt.Errorf("--state cannot be combined with -i %s", inputName(v))
			case typed(v, StateFile.Name()):
				return fmt.Errorf("--state-file cannot be combined with -i %s", inputName(v))
			case mode == input.Lines && len(listOf(v, imageFieldName)) > 1:
				return errors.New("-i lines takes one --image-field, since a line holds one path")
			default:
				return nil
			}
		},
	}
}

func tableOnStreamRule() Rule {
	return Rule{
		ID: "table-reads-one-record",
		Check: func(v View) error {
			// A stream writes one line per input line. A table is several lines with a repeated header,
			// and unlike raw it has no streaming form. Calibrate's -o is its report, which is one table.
			if inputModeOf(v).Streaming() && Get(v.Merged, Output) == "table" && v.Facts.Command != settingrow.CommandCalibrate {
				return fmt.Errorf("-o table reads one record. Drop -i %s or use -o json", inputName(v))
			}

			return nil
		},
	}
}

func lineFieldRule() Rule {
	return Rule{
		ID: "line-field-reserved",
		Check: func(v View) error {
			if v.Plan == nil || inputModeOf(v) != input.Lines {
				return nil
			}

			for _, question := range v.Plan.Questions {
				if question.ID == lineField {
					return fmt.Errorf("question id '%s' is reserved under -i lines, "+
						"which writes the input line number as %s", lineField, lineField)
				}
			}

			return nil
		},
	}
}
