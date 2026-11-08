package settingrow

import (
	"maps"
	"testing"
)

func TestFlagOn(t *testing.T) {
	t.Run("should carry help placement without changing command overrides", func(t *testing.T) {
		row := Row{Name: "output", Kind: Text(), Help: "Write answers.", HelpTopic: "output", HelpCommon: map[Command]int{CommandRoot: 10, CommandCalibrate: 13}, HelpValue: "FORMAT", Flag: FlagSpec{Long: "output", Short: "o", Commands: []Command{CommandRoot, CommandCalibrate}, PerCommand: map[Command]CommandFlag{CommandCalibrate: {Help: "Write the report.", Default: "json", NoShort: true}}}}
		for _, command := range []Command{CommandRoot, CommandCalibrate} {
			bound, ok := row.FlagOn(command)
			if !ok || bound.HelpTopic != "output" || bound.HelpValue != "FORMAT" || !maps.Equal(bound.HelpCommon, row.HelpCommon) {
				t.Fatalf("binding = %+v, available = %v", bound, ok)
			}
			if command == CommandCalibrate && (bound.Help != "Write the report." || bound.Short != "" || bound.Default != "json") {
				t.Fatalf("overrides = %+v", bound)
			}
			bound.HelpCommon[CommandRoot] = 99
			if row.HelpCommon[CommandRoot] != 10 {
				t.Fatal("binding shares mutable metadata")
			}
		}
		if _, ok := row.FlagOn(CommandProfileCreate); ok {
			t.Fatal("unavailable binding returned")
		}
	})
	t.Run("should give each common flag a unique positive rank", func(t *testing.T) {
		ranks := map[Command]map[int]Name{}
		for _, row := range Table() {
			for command, rank := range row.HelpCommon {
				if rank <= 0 {
					t.Fatalf("%s rank = %d", row.Name, rank)
				}
				if ranks[command] == nil {
					ranks[command] = map[int]Name{}
				}
				if prior, exists := ranks[command][rank]; exists {
					t.Fatalf("%s and %s share rank %d", row.Name, prior, rank)
				}
				ranks[command][rank] = row.Name
			}
		}
		if len(ranks[CommandRoot]) != 13 || len(ranks[CommandCalibrate]) != 14 {
			t.Fatalf("common ranks = %v", ranks)
		}
	})
}
