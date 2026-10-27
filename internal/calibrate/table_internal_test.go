package calibrate

import "testing"

func TestShareTexts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		share           Share
		rate, low, high string
	}{
		{name: "should round 113 of 200 half up to whole percents", share: Wilson(113, 200), rate: "57", low: "50", high: "63"},
		{name: "should show a share just under all with the places that keep it under 100", share: Wilson(29999, 30000), rate: "99.99", low: "99.98", high: "99.99"},
		{name: "should show a share just over none with the places that keep it over 0", share: Wilson(1, 30000), rate: "0.01", low: "0.01", high: "0.02"},
		{name: "should add a place when an interval would read 99-99", share: Wilson(990, 1000), rate: "99.0", low: "98.2", high: "99.5"},
		{name: "should print all and none as whole numbers", share: Wilson(4, 4), rate: "100", low: "51", high: "100"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rate, low, high := shareTexts(tc.share)
			if rate != tc.rate || low != tc.low || high != tc.high {
				t.Errorf("shareTexts(%+v) = %s, %s, %s, want %s, %s, %s", tc.share, rate, low, high, tc.rate, tc.low, tc.high)
			}
		})
	}
}
