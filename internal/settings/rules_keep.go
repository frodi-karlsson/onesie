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
					return refusal.reason(flag)
				}
			}

			return keepVerdictRefusal(v)
		},
	}
}

func keepRefusals() []keepRefusal {
	return []keepRefusal{
		{given: quietGiven, reason: func(string) error {
			return errors.New("-q suppresses output, which leaves --keep nothing to print")
		}},
		{given: func(v View) string { return spelledIf(Get(v.Merged, Resume), "--resume") }, reason: func(string) error {
			return errors.New("--keep prints only the records a run asks, and --resume skips the answered ones. " +
				"Pass --cache instead, which asks the answered records again for free and prints the whole kept output")
		}},
		{given: answersWithoutOut, reason: func(flag string) error {
			return fmt.Errorf("--keep prints the input records, which leaves %s nothing to write. "+
				"Pass --out FILE to write the answers there", flag)
		}},
	}
}

type keepRefusal struct {
	given  func(View) string
	reason func(flag string) error
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

	switch {
	case !hasAssert(v):
		return errors.New("--keep unsure needs --assert, since without one no record can be unsure")
	case !hasAbstainIf(v):
		return errors.New("--keep unsure needs --abstain-if, since without one no record is unsure")
	default:
		return nil
	}
}
