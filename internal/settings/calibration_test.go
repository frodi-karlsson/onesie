package settings_test

import (
	"reflect"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

func TestNewView(t *testing.T) {
	t.Run("should prepare explicit cuts and ordered requirements independently", func(t *testing.T) {
		texts := []string{"urgent.catches >= 0.9 at 0.3", "urgent.catches >= 0.8 at 0.7"}
		makeView := func(cuts string) settings.View {
			return (run{
				planned: true, positional: "is this urgent",
				facts: settings.Facts{Command: settingrow.CommandCalibrate},
				set:   values{"input": "jsonl", "cuts": cuts, "require": texts},
				typed: []settingrow.Name{"input", "map", "cuts"},
			}).view(t)
		}
		first, second := makeView("0.3,0.7"), makeView("0.2")
		if _, _, err := settings.Validate(first, settings.Rules()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first.Calibration.Cuts, []float64{0.3, 0.7}) {
			t.Fatalf("cuts = %v", first.Calibration.Cuts)
		}
		if len(first.Calibration.Requirements) != len(texts) {
			t.Fatal("missing requirements")
		}
		if len(second.Calibration.Requirements) != len(texts) || len(second.Calibration.Cuts) != 1 {
			t.Fatal("missing second invocation arguments")
		}
		for i, req := range first.Calibration.Requirements {
			if req.Source != texts[i] {
				t.Errorf("source = %q", req.Source)
			}
		}
		first.Calibration.Cuts[0] = 1
		first.Calibration.Requirements[0].Source = "changed"
		if second.Calibration.Cuts[0] != 0.2 || second.Calibration.Requirements[0].Source != texts[0] {
			t.Fatal("invocations share values")
		}
	})
	t.Run("should prepare default cuts independently", func(t *testing.T) {
		r := run{
			planned: true, positional: "is this urgent",
			facts: settings.Facts{Command: settingrow.CommandCalibrate},
			set:   values{"input": "jsonl"}, typed: []settingrow.Name{"input", "map"},
		}
		first, second := r.view(t), r.view(t)
		if _, _, err := settings.Validate(first, settings.Rules()); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first.Calibration.Cuts, calibrate.DefaultCuts()) {
			t.Fatalf("cuts = %v", first.Calibration.Cuts)
		}
		first.Calibration.Cuts[0] = 1
		if !reflect.DeepEqual(second.Calibration.Cuts, calibrate.DefaultCuts()) {
			t.Fatal("defaults share values")
		}
	})
	t.Run("should expose no arguments after either syntax failure", func(t *testing.T) {
		for _, tc := range []struct {
			cuts     string
			requires []string
		}{
			{cuts: "0.5", requires: []string{"urgent.catches >= 0.9", "bad"}},
			{cuts: "bad", requires: []string{"urgent.catches >= 0.9"}},
			{cuts: "bad", requires: []string{"bad"}},
		} {
			view := (run{
				facts: settings.Facts{Command: settingrow.CommandCalibrate},
				set:   values{"cuts": tc.cuts, "require": tc.requires},
				typed: []settingrow.Name{"cuts"},
			}).view(t)
			if view.Calibration.Cuts != nil || view.Calibration.Requirements != nil {
				t.Fatalf("arguments = %v", view.Calibration)
			}
		}
	})
	t.Run("should skip preparation for other commands", func(t *testing.T) {
		view := (run{set: values{"cuts": "bad", "require": []string{"bad"}}}).view(t)
		if view.Calibration.Cuts != nil || view.Calibration.Requirements != nil {
			t.Fatal("prepared root arguments")
		}
	})
}
