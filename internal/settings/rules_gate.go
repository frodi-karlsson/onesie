package settings

import (
	"errors"
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func abstainNeedsAssertRule() Rule {
	return Rule{
		ID: "abstain-needs-assert",
		Check: func(v View) error {
			if !hasAbstainIf(v) || hasAssert(v) {
				return nil
			}

			return fmt.Errorf("%s needs --assert, since without one every record is a yes", abstainFlag(v))
		},
	}
}

func stopOnVerdictNeedsGateRule() Rule {
	return Rule{
		ID: "stop-on-verdict-needs-gate",
		Check: func(v View) error {
			fail, unsure := StopsOn(v.Merged, settingrow.StopOnFail), StopsOn(v.Merged, settingrow.StopOnUnsure)

			switch {
			case fail && !hasAssert(v):
				return errors.New("--stop-on=fail needs --assert, since without one no record can fail")
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
