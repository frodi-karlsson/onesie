package calibrate

import (
	"testing"
)

func TestCompareYesNo(t *testing.T) {
	t.Parallel()

	separated := func(yes, no int) []YesNoCase {
		cases := make([]YesNoCase, 0, yes+no)
		for range yes {
			cases = append(cases, YesNoCase{Yes: true, Value: 0.9})
		}

		for range no {
			cases = append(cases, YesNoCase{Value: 0.1})
		}

		return cases
	}

	ranked := func(yes, no []float64) []YesNoCase {
		cases := make([]YesNoCase, 0, len(yes)+len(no))
		for _, value := range yes {
			cases = append(cases, YesNoCase{Yes: true, Value: value})
		}

		for _, value := range no {
			cases = append(cases, YesNoCase{Value: value})
		}

		return cases
	}

	// The wanted values come from a separate implementation of the same formulas, so a slip in either
	// shows as a mismatch.
	intervals := []struct {
		name  string
		cases []YesNoCase
		want  Interval
	}{
		{
			name: "should keep width below a perfect separation of 16 yes and 16 no", cases: separated(16, 16),
			want: Interval{Value: 1, Low: 0.8732, High: 1, Defined: true},
		},
		{
			name: "should widen a perfect separation on fewer records", cases: separated(5, 5),
			want: Interval{Value: 1, Low: 0.6427, High: 1, Defined: true},
		},
		{
			name:  "should bound an AUC of 0.9 on both sides",
			cases: ranked([]float64{0.9, 0.8, 0.7, 0.4}, []float64{0.1, 0.3, 0.6, 0.2, 0.5}),
			want:  Interval{Value: 0.9, Low: 0.4999, High: 0.986, Defined: true},
		},
		{
			name: "should keep width above a run that ranks every no over every yes",
			cases: ranked([]float64{0.1, 0.1, 0.1, 0.1, 0.1, 0.1},
				[]float64{0.9, 0.9, 0.9, 0.9, 0.9, 0.9}),
			want: Interval{Value: 0, Low: 0, High: 0.3108, Defined: true},
		},
		{name: "should leave the interval undefined with no no label", cases: separated(4, 0)},
	}

	for _, tc := range intervals {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := CompareYesNo([][]YesNoCase{tc.cases}, nil, nil)[0].AUC
			if got != tc.want {
				t.Errorf("AUC = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("should leave the baseline without a change", func(t *testing.T) {
		t.Parallel()

		scores := CompareYesNo([][]YesNoCase{separated(3, 3), separated(3, 3)}, nil, nil)
		if scores[0].Change != nil || scores[1].Change == nil {
			t.Errorf("changes = %v and %v, want nil on the baseline and set on the run", scores[0].Change, scores[1].Change)
		}
	})

	changes := []struct {
		name           string
		baseline, runs []YesNoCase
		want           Interval
	}{
		{
			name:     "should give a paired change its MOVER interval",
			baseline: ranked([]float64{0.9, 0.8, 0.7, 0.4}, []float64{0.1, 0.3, 0.6, 0.2, 0.5}),
			runs:     ranked([]float64{0.6, 0.85, 0.3, 0.45}, []float64{0.2, 0.5, 0.4, 0.1, 0.7}),
			want:     Interval{Value: -0.2, Low: -0.55, High: 0.1784, Defined: true},
		},
		{
			name:     "should keep width between two perfect runs, whose DeLong variance is zero",
			baseline: separated(18, 22), runs: separated(18, 22),
			want: Interval{Value: 0, Low: -0.1023, High: 0.1023, Defined: true},
		},
	}

	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := CompareYesNo([][]YesNoCase{tc.baseline, tc.runs}, nil, nil)[1].Change
			if got == nil || *got != tc.want {
				t.Errorf("change = %+v, want %+v", got, tc.want)
			}
		})
	}

	budgetCases := ranked([]float64{0.9, 0.8, 0.6, 0.3}, []float64{0.1, 0.2, 0.4, 0.5, 0.7, 0.2, 0.1, 0.1, 0.3, 0.2})
	budgets := []struct {
		name   string
		budget float64
		want   BudgetRow
	}{
		{
			name: "should read catches at the lowest value whose false alarms fit the budget", budget: 0.1,
			want: BudgetRow{Budget: 0.1, Cut: 0.6, Found: true, Catches: wilson(3, 4), FalseAlarms: wilson(1, 10)},
		},
		{
			name: "should count a budget that lands on a whole record as met", budget: 0.3,
			want: BudgetRow{Budget: 0.3, Cut: 0.4, Found: true, Catches: wilson(3, 4), FalseAlarms: wilson(3, 10)},
		},
		{
			name: "should read a budget of zero above every no", budget: 0,
			want: BudgetRow{Budget: 0, Cut: 0.8, Found: true, Catches: wilson(2, 4), FalseAlarms: wilson(0, 10)},
		},
	}

	for _, tc := range budgets {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := CompareYesNo([][]YesNoCase{budgetCases}, nil, []float64{tc.budget})[0].Budgets[0]
			if got != tc.want {
				t.Errorf("row = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("should find no cut when the highest answer is a no beyond the budget", func(t *testing.T) {
		t.Parallel()

		cases := ranked([]float64{0.5}, []float64{0.9, 0.1})

		got := CompareYesNo([][]YesNoCase{cases}, nil, []float64{0})[0].Budgets[0]
		if got.Found {
			t.Errorf("row = %+v, want none found", got)
		}
	})
}
