package settings

import (
	"errors"
	"fmt"
)

func abstainNeedsAssertRule() Rule {
	return Rule{
		ID:    "abstain-needs-assert",
		Reads: []Name{abstainIfName, assertName},
		Check: func(v View) error {
			if !hasAbstainIf(v) || hasAssert(v) {
				return nil
			}

			return fmt.Errorf("%s needs --assert, since without one every record is a yes", abstainFlag(v))
		},
	}
}

func stopOnAssertNeedsAssertRule() Rule {
	return Rule{
		ID:    "stop-on-assert-needs-assert",
		Reads: []Name{StopOnAssert.Name(), assertName},
		Check: func(v View) error {
			if Get(v.Merged, StopOnAssert) && !hasAssert(v) {
				return errors.New("--stop-on-assert needs --assert, since without one no assertion is false")
			}

			return nil
		},
	}
}
