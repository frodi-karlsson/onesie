package settings

import (
	"fmt"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func needsRule(name settingrow.Name) Rule {
	return Rule{
		ID: NeedsRuleID(name),
		Check: func(v View) error {
			row, bound := flagRow(name, v.Facts.Command)
			if !settingGiven(v, row, bound) {
				return nil
			}

			for _, need := range bound.Needs {
				if need.HelpOnly {
					continue
				}

				if needed, neededBound := flagRow(need.Name, v.Facts.Command); !settingGiven(v, needed, neededBound) {
					return fmt.Errorf("%s needs %s, %s", needingSpelled(v, row, bound), spelling(neededBound), need.Reason)
				}
			}

			return nil
		},
	}
}

// NeedsRuleID names the rule that refuses the flag of name without each setting its row needs.
func NeedsRuleID(name settingrow.Name) string {
	return "needs-" + strings.ReplaceAll(string(name), "_", "-")
}

func flagRow(name settingrow.Name, command settingrow.Command) (settingrow.Row, settingrow.BoundFlag) {
	for _, row := range settingrow.Table() {
		if row.Name != name {
			continue
		}

		// A rule checked with no command, as a test may, reads the flag as the root run binds it.
		if bound, registered := row.FlagOn(command); registered {
			return row, bound
		}

		bound, _ := row.FlagOn(settingrow.CommandRoot)

		return row, bound
	}

	return settingrow.Row{}, settingrow.BoundFlag{}
}

func settingGiven(v View, row settingrow.Row, bound settingrow.BoundFlag) bool {
	if row.Flag.Group {
		return eventGiven(v, bound.Long)
	}

	switch value := v.Merged.Value(row.Name).(type) {
	case bool:
		return value
	case string:
		return value != ""
	case []string:
		return len(value) > 0
	default:
		return v.Merged.Source(row.Name) != SourceDefault
	}
}

func needingSpelled(v View, row settingrow.Row, bound settingrow.BoundFlag) string {
	if v.Merged.Source(row.Name) == SourceFile {
		return plan.Spelling(plan.OriginFile, spelling(bound))
	}

	return spelling(bound)
}
