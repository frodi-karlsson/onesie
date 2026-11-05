package settings

import "errors"

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
