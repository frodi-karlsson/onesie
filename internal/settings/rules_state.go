package settings

import (
	"fmt"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func bodyStateRule() Rule {
	return Rule{
		ID: "one-state",
		Check: func(v View) error {
			if !v.Facts.BodyState || v.Facts.Command == settingrow.CommandCalibrate {
				return nil
			}

			var flag string

			switch {
			case typed(v, State.Name()):
				flag = "--state"
			case len(listOf(v, imageName)) > 0:
				flag = "--image"
			default:
				return nil
			}

			return fmt.Errorf("%s and the state in %s both give a state. Drop one", flag, Get(v.Merged, File))
		},
	}
}
