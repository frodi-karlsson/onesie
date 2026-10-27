package calibrate

import "testing"

func TestPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rate float64
		want string
	}{
		{name: "should round 113 of 200 half up to 57", rate: Wilson(113, 200).Rate, want: "57"},
		{name: "should keep a rate just under 1 at 99", rate: Wilson(29999, 30000).Rate, want: "99"},
		{name: "should keep a rate just over 0 at 1", rate: Wilson(1, 30000).Rate, want: "1"},
		{name: "should print 100 for all", rate: 1, want: "100"},
		{name: "should print 0 for none", rate: 0, want: "0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := percent(tc.rate); got != tc.want {
				t.Errorf("percent(%v) = %s, want %s", tc.rate, got, tc.want)
			}
		})
	}
}
