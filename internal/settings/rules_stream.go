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

	for _, refused := range streamOnly() {
		if refused.given(v) {
			return fmt.Errorf("%s applies to streaming input. -i %s reads one record", refused.spelled(v), inputName(v))
		}
	}

	if typed(v, Jobs.Name()) && Get(v.Merged, Jobs) > 1 {
		return fmt.Errorf("-j applies to streaming input. -i %s reads one record. "+
			"Pass -i jsonl, -i lines, -i csv or -i tsv, or drop -j", inputName(v))
	}

	if typed(v, Input.Name()) && len(listOf(v, imageName)) > 0 && !typed(v, State.Name()) {
		return fmt.Errorf("-i %s holds one record, which beside --image reads stdin only through "+
			"--state -. Pass --state - to read the state from stdin", inputName(v))
	}

	return nil
}

func streamOnly() []refusal {
	var refusals []refusal

	for _, row := range settingrow.Table() {
		bound, registered := row.FlagOn(settingrow.CommandRoot)

		// A one record run asks with -j 1, so only a -j above it is refused, below.
		if !registered || row.Name == Jobs.Name() || !row.Modes.Has(settingrow.ModeStream) || row.Modes.Has(settingrow.ModeAsk) {
			continue
		}

		given, name := modeException(row, bound, settingrow.ModeAsk)
		refusals = append(refusals, refusal{row.Name, func(v View) bool { return typed(v, row.Name) || given(v) }, name})
	}

	return refusals
}

func streamStateRule() Rule {
	return Rule{
		ID: "stream-reads-its-own-state",
		Check: func(v View) error {
			mode := inputModeOf(v)
			if !mode.Streaming() {
				return nil
			}

			// The flag the user typed and the mode they gave. Naming jsonl for a lines run sends them to the wrong mode.
			switch {
			case typed(v, State.Name()):
				return fmt.Errorf("--state cannot be combined with -i %s", inputName(v))
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
			// -i request writes the API's own responses and a merged record is the input record, so
			// neither holds a line number.
			mode := inputModeOf(v)
			merged := Get(v.Merged, MergeInput) || Get(v.Merged, MergeKey) != ""

			if v.Plan == nil || !mode.Streaming() || mode == input.Request || merged {
				return nil
			}

			for _, question := range v.Plan.Questions {
				if question.ID == lineField {
					return fmt.Errorf("question id '%s' is reserved under -i %s, "+
						"which writes the input line number as %s", lineField, inputName(v), lineField)
				}
			}

			return nil
		},
	}
}
