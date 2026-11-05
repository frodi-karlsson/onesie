package settings

import "testing"

func TestModeRefusals(t *testing.T) {
	t.Parallel()

	modes := []Mode{ModeListModels, ModeRequest, ModePrintRequest, ModePrintQuestions}

	tests := []struct {
		name  string
		check func(t *testing.T, mode Mode, refusals []refusal)
	}{
		{
			name: "should refuse only settings whose row is outside the mode",
			check: func(t *testing.T, mode Mode, refusals []refusal) {
				t.Helper()

				for _, rule := range refusals {
					if rule.setting == "" {
						continue
					}

					row, found := Lookup(rule.setting)
					if !found {
						t.Errorf("mode %d refuses %s, which no row is", mode, rule.setting)

						continue
					}

					if row.Modes.Has(mode) {
						t.Errorf("mode %d refuses %s, whose row has an effect in it", mode, rule.setting)
					}
				}
			},
		},
		{
			name: "should list refusals for the mode",
			check: func(t *testing.T, mode Mode, refusals []refusal) {
				t.Helper()

				if len(refusals) == 0 {
					t.Errorf("mode %d refuses nothing", mode)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, mode := range modes {
				tc.check(t, mode, modeRefusals(mode))
			}
		})
	}
}
