package calibrate_test

import (
	"math"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
)

const z95 = 1.959963984540054

func TestWilson(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		hits, of int
		wantLow  float64
		wantHigh float64
		places   float64
	}{
		{name: "should give 0 and 0.4899 for 0 of 4", hits: 0, of: 4, wantLow: 0, wantHigh: 0.4899, places: 1e-4},
		{name: "should give 0.5101 and 1 for 4 of 4", hits: 4, of: 4, wantLow: 0.5101, wantHigh: 1, places: 1e-4},
		{name: "should match the closed form for 45 of 47", hits: 45, of: 47, places: 1e-9},
		{name: "should match the closed form for 16 of 165", hits: 16, of: 165, places: 1e-9},
		{name: "should match the closed form for 1 of 1", hits: 1, of: 1, places: 1e-9},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := calibrate.Wilson(tc.hits, tc.of)

			wantLow, wantHigh := tc.wantLow, tc.wantHigh
			if tc.places == 1e-9 {
				wantLow, wantHigh = closedForm(tc.hits, tc.of)
			}

			if !got.Defined || got.Hits != tc.hits || got.Of != tc.of {
				t.Fatalf("Wilson(%d, %d) = %+v, want a defined share of %d of %d", tc.hits, tc.of, got, tc.hits, tc.of)
			}

			if want := float64(tc.hits) / float64(tc.of); got.Rate != want {
				t.Errorf("Rate = %v, want %v", got.Rate, want)
			}

			if math.Abs(got.Low-wantLow) > tc.places || math.Abs(got.High-wantHigh) > tc.places {
				t.Errorf("interval = %v to %v, want %v to %v", got.Low, got.High, wantLow, wantHigh)
			}
		})
	}

	t.Run("should leave 0 of 0 undefined", func(t *testing.T) {
		t.Parallel()

		got := calibrate.Wilson(0, 0)
		if got.Defined {
			t.Errorf("Wilson(0, 0) = %+v, want Defined false", got)
		}
	})

	t.Run("should give exactly 0 at no hits and exactly 1 at all hits", func(t *testing.T) {
		t.Parallel()

		for of := 1; of <= 500; of++ {
			if low := calibrate.Wilson(0, of).Low; low != 0 {
				t.Errorf("Wilson(0, %d).Low = %v, want exactly 0", of, low)
			}

			if high := calibrate.Wilson(of, of).High; high != 1 {
				t.Errorf("Wilson(%d, %d).High = %v, want exactly 1", of, of, high)
			}
		}
	})

	t.Run("should keep low and high inside 0 and 1", func(t *testing.T) {
		t.Parallel()

		for of := 1; of <= 200; of++ {
			for hits := 0; hits <= of; hits++ {
				got := calibrate.Wilson(hits, of)
				if got.Low < 0 || got.High > 1 || got.Low > got.Rate || got.High < got.Rate {
					t.Fatalf("Wilson(%d, %d) = %+v, want 0 <= low <= rate <= high <= 1", hits, of, got)
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
