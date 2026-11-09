package settings

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func TestModeRefusals(t *testing.T) {
	t.Parallel()

	modes := []settingrow.Mode{settingrow.ModeListModels, settingrow.ModeRequest, settingrow.ModePrintRequest, settingrow.ModePrintQuestions}

	tests := []struct {
		name  string
		check func(t *testing.T, mode settingrow.Mode, refusals []refusal)
	}{
		{
			name: "should refuse only settings whose row is outside the mode",
			check: func(t *testing.T, mode settingrow.Mode, refusals []refusal) {
				t.Helper()

				for _, rule := range refusals {
					if rule.setting == "" {
						continue
					}

					row, found := settingrow.Lookup(rule.setting)
					if !found {
						t.Errorf("mode %d refuses %s, which no row is", mode, rule.setting)

						continue
					}

					// -i request refuses -m, whose bodies carry their own model, and still sends a body that
					// names none with the model a profile or TYPESAFE_DEFAULT_MODEL gives.
					// --stop-on has an effect under -i request and --print-request, which still refuse its fail and
					// unsure verdicts.
					if row.Modes.Has(mode) && (mode != settingrow.ModeRequest || rule.setting != Model.Name()) &&
						rule.setting != StopOn.Name() {
						t.Errorf("mode %d refuses %s, whose row has an effect in it", mode, rule.setting)
					}
				}
			},
		},
		{
			name: "should refuse every typed root row outside the mode",
			check: func(t *testing.T, mode settingrow.Mode, refusals []refusal) {
				t.Helper()

				for _, row := range settingrow.Table() {
					bound, registered := row.FlagOn(settingrow.CommandRoot)
					if !registered || row.Modes.Has(mode) {
						continue
					}

					t.Run("should refuse "+bound.Long, func(t *testing.T) {
						merged := &Merged{}
						merged.Set(row.Name, row.Kind.Zero(), SourceFlag)
						view := NewView(merged, nil, Facts{Command: settingrow.CommandRoot})
						if err := firstRefusal(view, mode); err == nil {
							t.Errorf("mode %d accepts typed %s outside its row modes", mode, row.Name)
						}
					})
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
