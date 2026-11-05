package settings

import (
	"errors"
	"fmt"
)

func stateAndStateFileRule() Rule {
	return Rule{
		ID:    "state-and-state-file",
		Reads: []Name{State.Name(), StateFile.Name()},
		Check: func(v View) error {
			if typed(v, State.Name()) && typed(v, StateFile.Name()) {
				return errors.New("--state and --state-file are mutually exclusive")
			}

			return nil
		},
	}
}

func bodyStateRule() Rule {
	return Rule{
		ID:    "one-state",
		Reads: []Name{State.Name(), StateFile.Name(), imageName, File.Name()},
		Check: func(v View) error {
			if !v.Facts.BodyState || v.Facts.Command == CommandCalibrate {
				return nil
			}

			var flag string

			switch {
			case typed(v, State.Name()):
				flag = "--state"
			case typed(v, StateFile.Name()):
				flag = "--state-file"
			case len(listOf(v, imageName)) > 0:
				flag = "--image"
			default:
				return nil
			}

			return fmt.Errorf("%s and the state in %s both give a state. Drop one", flag, Get(v.Merged, File))
		},
	}
}
