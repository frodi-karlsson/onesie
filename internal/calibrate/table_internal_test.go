package calibrate

import "testing"

func TestWholePercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fraction float64
		want     string
	}{
		{name: "should round half up to a whole percent", fraction: 0.565, want: "57"},
		{name: "should round down below the half", fraction: 0.5049, want: "50"},
		{name: "should show none as 1", fraction: 0, want: "1"},
		{name: "should show a share just over none as 1", fraction: 0.0001, want: "1"},
		{name: "should show all as 99", fraction: 1, want: "99"},
		{name: "should show a share just under all as 99", fraction: 0.9999, want: "99"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := wholePercent(tc.fraction); got != tc.want {
				t.Errorf("wholePercent(%v) = %s, want %s", tc.fraction, got, tc.want)
			}
		})
	}
}
