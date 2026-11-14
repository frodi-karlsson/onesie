package settings

import (
	"errors"

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
			case unsure && !hasAssert(v):
				return errors.New("--stop-on=unsure needs --assert, since without one no record can be unsure")
			case unsure && !hasAbstainIf(v):
				return errors.New("--stop-on=unsure needs --abstain-if, since without one no record is unsure")
			default:
				return nil
			}
		},
	}
}
