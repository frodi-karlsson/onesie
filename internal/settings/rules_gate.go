package settings

import (
	"errors"
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func stopOnVerdictNeedsGateRule() Rule {
	return Rule{
		ID: "stop-on-verdict-needs-gate",
		Check: func(v View) error {
			fail, unsure := StopsOn(v.Merged, settingrow.VerdictFail), StopsOn(v.Merged, settingrow.VerdictUnsure)

			switch {
			// --keep judges one question alone, which can fail and never be unsure.
			case fail && !hasAssert(v) && Get(v.Merged, Keep) == "":
				return errors.New("--stop-on=fail needs --assert or --keep, since without one no record can fail")
			case unsure:
				return unsureRefusal(v, "--stop-on=unsure")
			default:
				return nil
			}
		},
	}
}

func unsureRefusal(v View, flag string) error {
	switch {
	case !hasAssert(v):
		return fmt.Errorf("%s needs --assert, since without one no record can be unsure", flag)
	case !hasAbstainIf(v):
		return fmt.Errorf("%s needs --abstain-if, since without one no record is unsure", flag)
	default:
		return nil
	}
}
