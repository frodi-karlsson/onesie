package settings

import (
	"errors"
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func keepRule() Rule {
	return Rule{
		ID: "keep-prints-the-records",
		Check: func(v View) error {
			if Get(v.Merged, Keep) == "" {
				return nil
			}

			for _, refusal := range keepRefusals() {
				if flag := refusal.given(v); flag != "" {
					return refusal.reason(v, flag)
				}
			}

			return keepVerdictRefusal(v)
		},
	}
}

func keepRefusals() []keepRefusal {
	return []keepRefusal{
		{given: quietGiven, reason: func(View, string) error {
			return errors.New("-q suppresses output, which leaves --keep nothing to print")
		}},
		{given: func(v View) string { return spelledIf(Get(v.Merged, Resume), "--resume") }, reason: func(v View, _ string) error {
			return resumeRefusal(v)
		}},
		{given: answersWithoutOut, reason: func(_ View, flag string) error {
			return fmt.Errorf("--keep prints the input records, which leaves %s nothing to write. "+
				"Pass --out FILE to write the answers there", flag)
		}},
	}
}

type keepRefusal struct {
	given  func(View) string
	reason func(v View, flag string) error
}

func resumeRefusal(v View) error {
	const skips = "--keep prints only the records a run asks, and --resume skips the answered ones. "

	// --mock reads back only the lines -o json writes for a record, not a merged line or a table row.
	if merging(v) || Derived(v, Output) != "json" {
		return errors.New(skips + "Drop --resume to ask every record again, or pass --cache, which asks the answered " +
			"ones again for free when every run passed it")
	}

	return errors.New(skips + "Finish the --out file with --resume and no --keep, then pass it as --mock FILE beside " +
		"--keep, which replays its answers for free. --cache does the same when every run passed it")
}

func quietGiven(v View) string {
	return spelledIf(Get(v.Merged, Quiet), "-q")
}

func answersWithoutOut(v View) string {
	if Get(v.Merged, Out) != "" {
		return ""
	}

	switch {
	case Get(v.Merged, Output) != "" && Get(v.Merged, Output) != "auto":
		return "-o " + outputTyped(v)
	case merging(v):
		return mergeName(v)
	default:
		return spelledIf(Get(v.Merged, Usage), "--usage")
	}
}

func spelledIf(given bool, flag string) string {
	if given {
		return flag
	}

	return ""
}

func keepVerdictRefusal(v View) error {
	if !Keeps(v.Merged, settingrow.VerdictUnsure) {
		return nil
	}

	return unsureRefusal(v, "--keep unsure")
}
