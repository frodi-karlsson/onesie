package calibrate

import (
	"math"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/num"
)

func TestWilson(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		hits, of   int
		wantLow    float64
		wantHigh   float64
		closedForm bool
	}{
		{name: "should give 0 and 0.4899 for 0 of 4", hits: 0, of: 4, wantLow: 0, wantHigh: 0.4899},
		{name: "should give 0.5101 and 1 for 4 of 4", hits: 4, of: 4, wantLow: 0.5101, wantHigh: 1},
		{name: "should match the closed form for 45 of 47", hits: 45, of: 47, closedForm: true},
		{name: "should match the closed form for 16 of 165", hits: 16, of: 165, closedForm: true},
		{name: "should match the closed form for 1 of 1", hits: 1, of: 1, closedForm: true},
		{name: "should round the rate and interval of 2 of 3 to four places", hits: 2, of: 3, wantLow: 0.2077, wantHigh: 0.9385},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := wilson(tc.hits, tc.of)

			wantLow, wantHigh := tc.wantLow, tc.wantHigh
			if tc.closedForm {
				wantLow, wantHigh = closedForm(tc.hits, tc.of)
				wantLow, wantHigh = num.Rounded(wantLow), num.Rounded(wantHigh)
			}

			if !got.Defined || got.Hits != tc.hits || got.Of != tc.of {
				t.Fatalf("wilson(%d, %d) = %+v, want a defined share of %d of %d", tc.hits, tc.of, got, tc.hits, tc.of)
			}

			if want := num.Rounded(float64(tc.hits) / float64(tc.of)); got.Rate != want {
				t.Errorf("Rate = %v, want %v", got.Rate, want)
			}

			if got.Low != wantLow || got.High != wantHigh {
				t.Errorf("interval = %v to %v, want %v to %v", got.Low, got.High, wantLow, wantHigh)
			}
		})
	}

	t.Run("should never round a rate or interval end that is not 0 or 1 to 0 or 1", func(t *testing.T) {
		t.Parallel()

		near := wilson(29999, 30000)
		if near.Rate != 0.9999 || near.High != 0.9999 {
			t.Errorf("wilson(29999, 30000) = %+v, want a rate and high of 0.9999", near)
		}

		far := wilson(1, 30000)
		if far.Rate != 0.0001 || far.Low != 0.0001 {
			t.Errorf("wilson(1, 30000) = %+v, want a rate and low of 0.0001", far)
		}
	})

	t.Run("should leave 0 of 0 undefined", func(t *testing.T) {
		t.Parallel()

		got := wilson(0, 0)
		if got.Defined {
			t.Errorf("wilson(0, 0) = %+v, want Defined false", got)
		}
	})

	t.Run("should give exactly 0 at no hits and exactly 1 at all hits", func(t *testing.T) {
		t.Parallel()

		for of := 1; of <= 500; of++ {
			if low := wilson(0, of).Low; low != 0 {
				t.Errorf("wilson(0, %d).Low = %v, want exactly 0", of, low)
			}

			if high := wilson(of, of).High; high != 1 {
				t.Errorf("wilson(%d, %d).High = %v, want exactly 1", of, of, high)
			}
		}
	})

	t.Run("should keep low and high inside 0 and 1", func(t *testing.T) {
		t.Parallel()

		for of := 1; of <= 200; of++ {
			for hits := 0; hits <= of; hits++ {
				got := wilson(hits, of)
				if got.Low < 0 || got.High > 1 || got.Low > got.Rate || got.High < got.Rate {
					t.Fatalf("wilson(%d, %d) = %+v, want 0 <= low <= rate <= high <= 1", hits, of, got)
				}
			}
		}
	})
}

func closedForm(hits, of int) (low, high float64) {
	n := float64(of)
	p := float64(hits) / n
	z2 := z95 * z95
	root := z95 * math.Sqrt(z2+4*n*p*(1-p))

	return (2*n*p + z2 - root) / (2 * (n + z2)), (2*n*p + z2 + root) / (2 * (n + z2))
}
