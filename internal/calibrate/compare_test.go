package calibrate

import (
	"slices"
	"testing"
)

func TestCompareNoul(t *testing.T) {
	t.Parallel()

	separated := func(yes, no int) []NoulCase {
		cases := make([]NoulCase, 0, yes+no)
		for range yes {
			cases = append(cases, NoulCase{Yes: true, Value: 0.9})
		}

		for range no {
			cases = append(cases, NoulCase{Value: 0.1})
		}

		return cases
	}

	ranked := func(yes, no []float64) []NoulCase {
		cases := make([]NoulCase, 0, len(yes)+len(no))
		for _, value := range yes {
			cases = append(cases, NoulCase{Yes: true, Value: value})
		}

		for _, value := range no {
			cases = append(cases, NoulCase{Value: value})
		}

		return cases
	}

	// The wanted values come from a separate implementation of the same formulas, so a slip in either
	// shows as a mismatch.
	intervals := []struct {
		name  string
		cases []NoulCase
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

			got := CompareNoul([][]NoulCase{tc.cases}, nil, nil)[0].AUC
			if got != tc.want {
				t.Errorf("AUC = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("should leave the baseline without a change", func(t *testing.T) {
		t.Parallel()

		scores := CompareNoul([][]NoulCase{separated(3, 3), separated(3, 3)}, nil, nil)
		if scores[0].Change != nil || scores[1].Change == nil {
			t.Errorf("changes = %v and %v, want nil on the baseline and set on the run", scores[0].Change, scores[1].Change)
		}
	})

	changes := []struct {
		name           string
		baseline, runs []NoulCase
		want           Interval
	}{
		{
			name:     "should give a paired change its MOVER interval",
			baseline: ranked([]float64{0.9, 0.8, 0.7, 0.4}, []float64{0.1, 0.3, 0.6, 0.2, 0.5}),
			runs:     ranked([]float64{0.6, 0.85, 0.3, 0.45}, []float64{0.2, 0.5, 0.4, 0.1, 0.7}),
			want:     Interval{Value: -0.2, Low: -0.55, High: 0.1784, Defined: true},
		},
		{
			name:     "should count a yes and a no answered alike as half a win in the correlation",
			baseline: ranked([]float64{0.5, 0.7, 0.3, 0.5}, []float64{0.5, 0.2, 0.3, 0.1, 0.5}),
			runs:     ranked([]float64{0.6, 0.5, 0.4, 0.5}, []float64{0.5, 0.3, 0.4, 0.2, 0.1}),
			want:     Interval{Value: 0.1, Low: -0.2229, High: 0.4336, Defined: true},
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

			got := CompareNoul([][]NoulCase{tc.baseline, tc.runs}, nil, nil)[1].Change
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

			got := CompareNoul([][]NoulCase{budgetCases}, nil, []float64{tc.budget})[0].Budgets[0]
			if got != tc.want {
				t.Errorf("row = %+v, want %+v", got, tc.want)
			}
		})
	}

	shortened := []struct {
		name    string
		yes, no []float64
		budget  float64
		want    float64
	}{
		{
			name: "should shorten a cut to two places when no answer lies between", yes: []float64{0.4434, 0.4208479098236706},
			no: []float64{0.363040}, budget: 0, want: 0.42,
		},
		{
			name: "should keep more places when the next lower answer is within two places", yes: []float64{0.4208479098236706},
			no: []float64{0.4205}, budget: 0, want: 0.4208,
		},
		{
			name: "should keep a two place answer whose float lies just below it", yes: []float64{0.29},
			no: []float64{0.285}, budget: 0, want: 0.29,
		},
		{
			name: "should shorten the lowest answer, which has no answer below it", yes: []float64{0.9},
			no: []float64{0.0123456}, budget: 1, want: 0.01,
		},
	}

	for _, tc := range shortened {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			score := CompareNoul([][]NoulCase{ranked(tc.yes, tc.no)}, nil, []float64{tc.budget})[0]

			got := score.Budgets[0]
			if !got.Found || got.Cut != tc.want {
				t.Fatalf("cut = %v, found %v, want %v", got.Cut, got.Found, tc.want)
			}

			// The shortened cut must flag the records the exact one does.
			yes, no := slices.Sorted(slices.Values(tc.yes)), slices.Sorted(slices.Values(tc.no))
			if row := cutRow(got.Cut, yes, no); row.Catches != got.Catches || row.FalseAlarms != got.FalseAlarms {
				t.Errorf("at %v catches %+v and false alarms %+v, want %+v and %+v",
					got.Cut, row.Catches, row.FalseAlarms, got.Catches, got.FalseAlarms)
			}
		})
	}

	t.Run("should find no cut when the highest answer is a no beyond the budget", func(t *testing.T) {
		t.Parallel()

		cases := ranked([]float64{0.5}, []float64{0.9, 0.1})

		got := CompareNoul([][]NoulCase{cases}, nil, []float64{0})[0].Budgets[0]
		if got.Found {
			t.Errorf("row = %+v, want none found", got)
		}
	})
}

func TestCompareChoices(t *testing.T) {
	t.Parallel()

	picked := func(labels, picks []string) []ChoiceCase {
		cases := make([]ChoiceCase, len(labels))
		for i := range labels {
			cases[i] = ChoiceCase{Label: labels[i], Picked: picks[i], Confidence: 0.9}
		}

		return cases
	}

	labels := []string{"a", "b", "c", "a", "b", "c", "a", "b"}
	levels := []string{"1", "2", "3", "4", "5"}
	rated := []string{"1", "3", "5", "2", "4", "3"}

	// The wanted values come from a separate implementation of the same formulas.
	tests := []struct {
		name                  string
		runs                  [][]ChoiceCase
		names                 []string
		rate                  bool
		wantAgreement, wantIn *Interval
	}{
		{
			name: "should shrink a positive phi by n/2, as Newcombe's method 10 does over method 8",
			runs: [][]ChoiceCase{
				picked(labels, []string{"a", "b", "c", "a", "c", "c", "b", "b"}),
				picked(labels, []string{"a", "a", "c", "a", "b", "c", "b", "c"}),
			},
			names:         []string{"a", "b", "c"},
			wantAgreement: &Interval{Value: -0.125, Low: -0.4908, High: 0.2907, Defined: true},
		},
		{
			name: "should give a rate question a change within one level beside its agreement",
			runs: [][]ChoiceCase{
				picked(rated, []string{"1", "3", "3", "2", "5", "4"}),
				picked(rated, []string{"2", "5", "5", "4", "4", "1"}),
			},
			names: levels, rate: true,
			wantAgreement: &Interval{Value: -0.1667, Low: -0.6746, High: 0.4611, Defined: true},
			wantIn:        &Interval{Value: -0.3333, Low: -0.7263, High: 0.2716, Defined: true},
		},
		{
			name: "should keep width between two runs that agree on every record",
			runs: [][]ChoiceCase{
				picked(slices.Repeat([]string{"a"}, 16), slices.Repeat([]string{"a"}, 16)),
				picked(slices.Repeat([]string{"a"}, 16), slices.Repeat([]string{"a"}, 16)),
			},
			names:         []string{"a", "b"},
			wantAgreement: &Interval{Value: 0, Low: -0.1936, High: 0.1936, Defined: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			scores := CompareChoices(tc.runs, tc.names, tc.rate, nil)
			if scores[0].AgreementChange != nil || scores[0].WithinOneChange != nil {
				t.Errorf("baseline changes = %v and %v, want nil", scores[0].AgreementChange, scores[0].WithinOneChange)
			}

			got := scores[1]
			if got.AgreementChange == nil || *got.AgreementChange != *tc.wantAgreement {
				t.Errorf("agreement change = %+v, want %+v", got.AgreementChange, tc.wantAgreement)
			}

			if (tc.wantIn == nil) != (got.WithinOneChange == nil) || (tc.wantIn != nil && *got.WithinOneChange != *tc.wantIn) {
				t.Errorf("within one change = %+v, want %+v", got.WithinOneChange, tc.wantIn)
			}

			if (got.Rate != nil) != tc.rate {
				t.Errorf("rate score = %v, want one only for a rate question", got.Rate)
			}
		})
	}

	t.Run("should leave the change undefined over no records", func(t *testing.T) {
		t.Parallel()

		got := CompareChoices([][]ChoiceCase{{}, {}}, []string{"a"}, false, nil)[1].AgreementChange
		if got == nil || got.Defined {
			t.Errorf("change = %+v, want an undefined interval", got)
		}
	})
}
