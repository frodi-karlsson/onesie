package assert

import (
	"strings"
	"testing"
)

func TestCuts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   map[string]float64
		absent map[string]string
	}{
		{
			name:   "should read a cut from each side of an and",
			source: "d.value < 0.3 and s.value < 0.45",
			want:   map[string]float64{"d": 0.3, "s": 0.45},
		},
		{name: "should read a cut from at least", source: "d.value >= 0.5", want: map[string]float64{"d": 0.5}},
		{name: "should read a cut under not", source: "not (d.value < 0.3)", want: map[string]float64{"d": 0.3}},
		{
			name:   "should read a cut from each side of an or",
			source: "d.value < 0.3 or x.value < 0.2",
			want:   map[string]float64{"d": 0.3, "x": 0.2},
		},
		{name: "should read no cut from greater", source: "d.value > 0.3", absent: map[string]string{"d": ">"}},
		{name: "should read no cut from at most", source: "d.value <= 0.3", absent: map[string]string{"d": "<="}},
		{
			name:   "should read no cut through a function",
			source: "max(d.value, c.value) < 0.2",
			absent: map[string]string{"d": "max()", "c": "max()"},
		},
		{name: "should read no cut from a reversed comparison", source: "0.3 > d.value", absent: map[string]string{"d": "right"}},
		{
			name:   "should read no cut from two different cuts",
			source: "d.value < 0.3 and d.value < 0.4",
			absent: map[string]string{"d": "0.3 and 0.4"},
		},
		{name: "should read no cut from another field", source: "d.p < 0.3", absent: map[string]string{"d": "never compares"}},
		{name: "should read no cut from an in test", source: "d.value in [0.1, 0.2]", absent: map[string]string{"d": "in"}},
		{
			name:   "should read no cut beside an in test",
			source: "d.value < 0.3 and d.value in [0.1]",
			absent: map[string]string{"d": "in"},
		},
		{name: "should read one cut given twice", source: "d.value < 0.3 or d.value < 0.3", want: map[string]float64{"d": 0.3}},
		{name: "should name a bracketed id", source: `["x y"].value < 0.2`, want: map[string]float64{"x y": 0.2}},
		{name: "should compare a value with a string", source: `d.value == "a"`, absent: map[string]string{"d": "other than a number"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			expr, err := Parse(tc.source)
			if err != nil {
				t.Fatalf("Parse(%q) error = %v", tc.source, err)
			}

			got := Cuts(expr)
			if len(got) != len(tc.want)+len(tc.absent) {
				t.Errorf("Cuts(%q) = %+v, want %d ids", tc.source, got, len(tc.want)+len(tc.absent))
			}

			for id, value := range tc.want {
				if cut := got[id]; !cut.Readable || cut.Value != value || cut.Why != "" {
					t.Errorf("Cuts(%q)[%q] = %+v, want the readable cut %v", tc.source, id, cut, value)
				}
			}

			for id, why := range tc.absent {
				cut, found := got[id]
				if !found || cut.Readable || !strings.Contains(cut.Why, why) {
					t.Errorf("Cuts(%q)[%q] = %+v, %v, want no cut and a reason containing %q", tc.source, id, cut, found, why)
				}
			}
		})
	}

	t.Run("should read nothing from a nil expression", func(t *testing.T) {
		t.Parallel()

		if got := Cuts(nil); len(got) != 0 {
			t.Errorf("Cuts(nil) = %+v, want none", got)
		}
	})
}
