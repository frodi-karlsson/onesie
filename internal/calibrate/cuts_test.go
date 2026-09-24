package calibrate_test

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
)

func TestDefaultCuts(t *testing.T) {
	t.Parallel()

	t.Run("should run from 0.05 to 0.95 in steps of 0.05", func(t *testing.T) {
		t.Parallel()

		cuts := calibrate.DefaultCuts()
		if len(cuts) != 19 {
			t.Fatalf("len = %d, want 19: %v", len(cuts), cuts)
		}

		for i, cut := range cuts {
			if want := float64(i+1) / 20; cut != want {
				t.Errorf("cut %d = %v, want %v", i, cut, want)
			}
		}

		if cuts[2] != 0.15 {
			t.Errorf("third cut = %v, want exactly 0.15", cuts[2])
		}
	})
}

func TestParseCuts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		want    []float64
		wantErr string
	}{
		{name: "should keep the given values", text: "0.9,0.95,0.97,0.99", want: []float64{0.9, 0.95, 0.97, 0.99}},
		{name: "should sort and deduplicate the values", text: "0.9,0.5,0.9,0", want: []float64{0, 0.5, 0.9}},
		{name: "should accept both ends of the range", text: "0,1", want: []float64{0, 1}},
		{name: "should read negative zero as zero", text: "-0,0.5", want: []float64{0, 0.5}},
		{name: "should refuse a value above 1", text: "0.5,1.2", wantErr: "'1.2'"},
		{name: "should refuse a value below 0", text: "-0.1", wantErr: "'-0.1'"},
		{name: "should refuse NaN", text: "NaN", wantErr: "'NaN'"},
		{name: "should refuse a word", text: "x", wantErr: "'x'"},
		{name: "should refuse an empty list", text: "", wantErr: "got nothing"},
		{name: "should refuse an empty item", text: "0.5,,0.6", wantErr: "empty item"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := calibrate.ParseCuts(tc.text)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseCuts(%q) = %v, %v, want an error containing %q", tc.text, got, err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseCuts(%q) error = %v", tc.text, err)
			}

			// Equal counts -0 as 0, and a report prints it as -0.
			if !slices.Equal(got, tc.want) || slices.ContainsFunc(got, math.Signbit) {
				t.Errorf("ParseCuts(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}
