package calibrate_test

import (
	"math"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
)

func TestAUC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cases       []calibrate.YesNoCase
		want        float64
		wantDefined bool
	}{
		{
			name:        "should give 1 for perfect separation",
			cases:       yesNoCases("yes", 0.9, "yes", 0.8, "no", 0.2, "no", 0.1),
			want:        1,
			wantDefined: true,
		},
		{
			name:        "should give 0 for reversed separation",
			cases:       yesNoCases("yes", 0.1, "yes", 0.2, "no", 0.8, "no", 0.9),
			want:        0,
			wantDefined: true,
		},
		{
			name:        "should count a tie as half",
			cases:       yesNoCases("yes", 0.9, "yes", 0.5, "no", 0.5, "no", 0.1),
			want:        0.875,
			wantDefined: true,
		},
		{
			name:        "should give 0.5 when every value is tied",
			cases:       yesNoCases("yes", 0.5, "no", 0.5, "yes", 0.5, "no", 0.5, "no", 0.5),
			want:        0.5,
			wantDefined: true,
		},
		{name: "should leave only yes labels undefined", cases: yesNoCases("yes", 0.9, "yes", 0.1)},
		{name: "should leave only no labels undefined", cases: yesNoCases("no", 0.9, "no", 0.1)},
		{name: "should leave no cases undefined"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, defined := calibrate.AUC(tc.cases)
			if defined != tc.wantDefined || defined && math.Abs(got-tc.want) > 1e-12 {
				t.Errorf("AUC = %v, %v, want %v, %v", got, defined, tc.want, tc.wantDefined)
			}
		})
	}

	t.Run("should finish when a value is NaN", func(t *testing.T) {
		t.Parallel()

		cases := yesNoCases("yes", math.NaN(), "no", 0.5, "yes", math.NaN(), "no", math.NaN(), "yes", 0.9)

		finishes(t, func() {
			calibrate.AUC(cases)
			calibrate.ScoreYesNo(cases, 0, calibrate.DefaultCuts())
		})
	})

	t.Run("should rank 100000 cases by sorting rather than by pairs", func(t *testing.T) {
		t.Parallel()

		const n = 100000

		cases := make([]calibrate.YesNoCase, n)
		var yes, no [1000]float64

		for i := range cases {
			bucket := (i * 7919) % 1000
			cases[i] = calibrate.YesNoCase{Yes: i%3 == 0, Value: float64(bucket) / 1000}

			if cases[i].Yes {
				yes[bucket]++
			} else {
				no[bucket]++
			}
		}

		var wins, yesTotal, noTotal float64
		for y := range yes {
			yesTotal += yes[y]
			noTotal += no[y]

			for x := range no {
				switch {
				case y > x:
					wins += yes[y] * no[x]
				case y == x:
					wins += yes[y] * no[x] / 2
				}
			}
		}

		start := time.Now()
		got, defined := calibrate.AUC(cases)
		elapsed := time.Since(start)

		if want := wins / (yesTotal * noTotal); !defined || math.Abs(got-want) > 1e-9 {
			t.Errorf("AUC = %v, %v, want %v, true", got, defined, want)
		}

		if elapsed > 20*time.Second {
			t.Errorf("AUC over %d cases took %v, want well under 20s", n, elapsed)
		}
	})
}

func TestScoreYesNo(t *testing.T) {
	t.Parallel()

	fixed := yesNoCases(
		"yes", 0.75,
		"yes", 0.5,
		"yes", 0.125,
		"no", 0.5,
		"no", 0.25,
		"no", 0.9375,
		"yes", 0.5,
		"no", 0.0625,
	)

	t.Run("should count the headline and the AUC", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreYesNo(fixed, 3, []float64{0.5})
		if got.Labelled != 8 || got.Yes != 4 || got.No != 4 || got.Failed != 3 {
			t.Errorf("counts = labelled %d, yes %d, no %d, failed %d, want 8, 4, 4, 3",
				got.Labelled, got.Yes, got.No, got.Failed)
		}

		if !got.HasAUC || got.AUC != 0.5625 {
			t.Errorf("AUC = %v, %v, want 0.5625, true", got.AUC, got.HasAUC)
		}
	})

	t.Run("should flag a value equal to the cut at each given cut", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreYesNo(fixed, 0, []float64{0.5, 0.75})
		want := []cutCounts{
			{cut: 0.5, flagged: 5, catches: [2]int{3, 4}, falseAlarms: [2]int{2, 4}, right: [2]int{3, 5}},
			{cut: 0.75, flagged: 2, catches: [2]int{1, 4}, falseAlarms: [2]int{1, 4}, right: [2]int{1, 2}},
		}

		checkCutRows(t, "Cuts", got.Cuts, want)
	})

	t.Run("should give a row at every cut in the list it is handed", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreYesNo(fixed, 0, calibrate.DefaultCuts())

		var cuts []float64
		for _, row := range got.Cuts {
			cuts = append(cuts, row.Cut)
		}

		if !slices.Equal(cuts, calibrate.DefaultCuts()) {
			t.Errorf("cuts = %v, want the defaults", cuts)
		}
	})

	t.Run("should give a row at every distinct value in ascending order", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreYesNo(fixed, 0, []float64{0.5})
		want := []cutCounts{
			{cut: 0.0625, flagged: 8, catches: [2]int{4, 4}, falseAlarms: [2]int{4, 4}, right: [2]int{4, 8}},
			{cut: 0.125, flagged: 7, catches: [2]int{4, 4}, falseAlarms: [2]int{3, 4}, right: [2]int{4, 7}},
			{cut: 0.25, flagged: 6, catches: [2]int{3, 4}, falseAlarms: [2]int{3, 4}, right: [2]int{3, 6}},
			{cut: 0.5, flagged: 5, catches: [2]int{3, 4}, falseAlarms: [2]int{2, 4}, right: [2]int{3, 5}},
			{cut: 0.75, flagged: 2, catches: [2]int{1, 4}, falseAlarms: [2]int{1, 4}, right: [2]int{1, 2}},
			{cut: 0.9375, flagged: 1, catches: [2]int{0, 4}, falseAlarms: [2]int{1, 4}, right: [2]int{0, 1}},
		}

		checkCutRows(t, "Values", got.Values, want)
	})

	t.Run("should order misses by the widest gap with ties in input order", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreYesNo(fixed, 0, nil)

		want := []string{"c6", "c3", "c2", "c4", "c7", "c1", "c5", "c8"}
		if names := yesNoNames(got.Misses); !slices.Equal(names, want) {
			t.Errorf("misses = %v, want %v", names, want)
		}
	})

	t.Run("should keep each case's id and line in its miss", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreYesNo(fixed, 0, nil)

		if first := got.Misses[0]; first.ID != "c6" || first.Line != 6 {
			t.Errorf("first miss id %v line %d, want c6 line 6", first.ID, first.Line)
		}
	})

	t.Run("should keep complementary decimal gaps in input order", func(t *testing.T) {
		t.Parallel()

		cases := yesNoCases(
			"yes", 0.9,
			"no", 0.1,
			"yes", 0.7,
			"no", 0.3,
			"no", 0.2,
			"yes", 0.8,
			"yes", 0.6,
			"no", 0.4,
			"no", 0.35,
			"yes", 0.65,
		)

		got := calibrate.ScoreYesNo(cases, 0, nil)

		want := []string{"c7", "c8", "c9", "c10", "c3", "c4", "c5", "c6", "c1", "c2"}
		if names := yesNoNames(got.Misses); !slices.Equal(names, want) {
			t.Errorf("misses = %v, want %v", names, want)
		}
	})

	t.Run("should give empty lists rather than nil for no cases", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreYesNo(nil, 2, nil)
		if got.Cuts == nil || got.Values == nil || got.Misses == nil {
			t.Errorf("cuts %v, values %v, misses %v, want empty lists", got.Cuts == nil, got.Values == nil, got.Misses == nil)
		}
	})

	t.Run("should leave the AUC and false alarms undefined when every label is yes", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreYesNo(yesNoCases("yes", 0.9, "yes", 0.2), 0, []float64{0.5})
		if got.HasAUC {
			t.Errorf("HasAUC = true, want false")
		}

		row := got.Cuts[0]
		if row.FalseAlarms.Defined || !row.Catches.Defined || row.Catches.Hits != 1 || row.Catches.Of != 2 {
			t.Errorf("row = %+v, want catches 1 of 2 and undefined false alarms", row)
		}
	})
}

func TestScorePick(t *testing.T) {
	t.Parallel()

	names := []string{"billing", "shipping", "technical"}
	fixed := choiceCases(
		"billing", "billing", 0.9,
		"billing", "billing", 0.6,
		"billing", "shipping", 0.95,
		"shipping", "shipping", 0.8,
		"shipping", "billing", 0.7,
		"technical", "technical", 0.5,
		"technical", "refunds", 0.99,
		"shipping", "shipping", 0.4,
	)

	t.Run("should measure agreement and each option", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScorePick(fixed, names, 2, nil)
		if got.Labelled != 8 || got.Failed != 2 {
			t.Errorf("labelled %d, failed %d, want 8, 2", got.Labelled, got.Failed)
		}

		checkShare(t, "agreement", got.Agreement, 5, 8)

		want := []pickCounts{
			{name: "billing", labelled: 3, picked: 3, found: [2]int{2, 3}, right: [2]int{2, 3}},
			{name: "shipping", labelled: 3, picked: 3, found: [2]int{2, 3}, right: [2]int{2, 3}},
			{name: "technical", labelled: 2, picked: 1, found: [2]int{1, 2}, right: [2]int{1, 1}},
		}

		checkPickRows(t, got.Names, want)
	})

	t.Run("should fill the grid in option order with an other column for an outside answer", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScorePick(fixed, names, 0, nil)

		want := [][]int{{2, 1, 0}, {1, 2, 0}, {0, 0, 1}}
		if !slices.EqualFunc(got.Grid, want, slices.Equal) {
			t.Errorf("grid = %v, want %v", got.Grid, want)
		}

		if !slices.Equal(got.Other, []int{0, 0, 1}) {
			t.Errorf("other = %v, want [0 0 1]", got.Other)
		}
	})

	t.Run("should leave the other column out when every answer is an option", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScorePick(fixed[:6], names, 0, nil)
		if got.Other != nil {
			t.Errorf("other = %v, want nil", got.Other)
		}
	})

	t.Run("should give empty lists rather than nil when nothing is wrong", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScorePick(fixed[:2], names, 0, nil)
		if got.Misses == nil || got.Confidence == nil || got.Other != nil {
			t.Errorf("misses %v, confidence %v, other %v, want empty lists and a nil other",
				got.Misses, got.Confidence, got.Other)
		}

		empty := calibrate.ScorePick(nil, nil, 0, nil)
		if empty.Names == nil || empty.Grid == nil || empty.Misses == nil || empty.Confidence == nil {
			t.Errorf("score of nothing = %+v, want empty lists", empty)
		}
	})

	t.Run("should measure agreement over the answers at or above each confidence cut", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScorePick(fixed, names, 0, []float64{0.5, 0.9})
		if len(got.Confidence) != 2 {
			t.Fatalf("confidence rows = %d, want 2", len(got.Confidence))
		}

		checkConfidenceRow(t, got.Confidence[0], 0.5, [2]int{7, 8}, [2]int{4, 7})
		checkConfidenceRow(t, got.Confidence[1], 0.9, [2]int{3, 8}, [2]int{1, 3})
	})

	t.Run("should list the wrong picks by confidence, highest first", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScorePick(fixed, names, 0, nil)

		want := []string{"c7", "c3", "c5"}
		if got := choiceNames(got.Misses); !slices.Equal(got, want) {
			t.Errorf("misses = %v, want %v", got, want)
		}
	})

	t.Run("should keep each case's id and line in its miss", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScorePick(fixed, names, 0, nil)

		if first := got.Misses[0]; first.ID != "c7" || first.Line != 7 {
			t.Errorf("first miss id %v line %d, want c7 line 7", first.ID, first.Line)
		}
	})

	t.Run("should keep wrong picks of equal confidence in input order", func(t *testing.T) {
		t.Parallel()

		cases := choiceCases(
			"billing", "shipping", 0.5,
			"shipping", "billing", 0.8,
			"technical", "billing", 0.5,
			"billing", "technical", 0.8,
		)

		got := calibrate.ScorePick(cases, names, 0, nil)

		want := []string{"c2", "c4", "c1", "c3"}
		if got := choiceNames(got.Misses); !slices.Equal(got, want) {
			t.Errorf("misses = %v, want %v", got, want)
		}
	})
}

func TestScoreRate(t *testing.T) {
	t.Parallel()

	levels := []string{"1", "2", "3", "4", "5"}
	fixed := choiceCases(
		"3", "3", 0.9,
		"1", "2", 0.8,
		"5", "1", 0.6,
		"2", "4", 0.95,
		"4", "4", 0.7,
		"2", "x", 0.5,
		"3", "2", 0.99,
	)

	t.Run("should score everything pick does in level order", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreRate(fixed, levels, 1, []float64{0.9})
		if got.Labelled != 7 || got.Failed != 1 {
			t.Errorf("labelled %d, failed %d, want 7, 1", got.Labelled, got.Failed)
		}

		checkShare(t, "agreement", got.Agreement, 2, 7)

		var order []string
		for _, row := range got.Names {
			order = append(order, row.Name)
		}

		if !slices.Equal(order, levels) {
			t.Errorf("rows = %v, want %v", order, levels)
		}

		want := [][]int{
			{0, 1, 0, 0, 0},
			{0, 0, 0, 1, 0},
			{0, 1, 1, 0, 0},
			{0, 0, 0, 1, 0},
			{1, 0, 0, 0, 0},
		}
		if !slices.EqualFunc(got.Grid, want, slices.Equal) {
			t.Errorf("grid = %v, want %v", got.Grid, want)
		}

		if !slices.Equal(got.Other, []int{0, 1, 0, 0, 0}) {
			t.Errorf("other = %v, want [0 1 0 0 0]", got.Other)
		}

		checkConfidenceRow(t, got.Confidence[0], 0.9, [2]int{3, 7}, [2]int{1, 3})
	})

	t.Run("should measure agreement within one level and the mean distance", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreRate(fixed, levels, 0, nil)

		checkShare(t, "within one", got.WithinOne, 4, 7)

		if want := 12.0 / 7; !got.HasMeanDistance || math.Abs(got.MeanDistance-want) > 1e-12 {
			t.Errorf("mean distance = %v, want %v", got.MeanDistance, want)
		}
	})

	t.Run("should never count an outside answer as within one level", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreRate(choiceCases("1", "x", 0.5), []string{"1", "2"}, 0, nil)

		checkShare(t, "within one", got.WithinOne, 0, 1)

		if got.MeanDistance != 1 {
			t.Errorf("mean distance = %v, want the full span 1", got.MeanDistance)
		}
	})

	t.Run("should leave the mean distance undefined for no cases", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreRate(nil, levels, 3, nil)
		if got.HasMeanDistance || got.WithinOne.Defined {
			t.Errorf("mean distance %v, %v, within one %+v, want both undefined",
				got.MeanDistance, got.HasMeanDistance, got.WithinOne)
		}

		if got.Misses == nil {
			t.Errorf("misses = nil, want an empty list")
		}
	})

	t.Run("should rank misses by distance, then by confidence", func(t *testing.T) {
		t.Parallel()

		got := calibrate.ScoreRate(fixed, levels, 0, nil)

		want := []string{"c3", "c6", "c4", "c7", "c2"}
		if got := choiceNames(got.Misses); !slices.Equal(got, want) {
			t.Errorf("misses = %v, want %v", got, want)
		}
	})
}

func yesNoCases(pairs ...any) []calibrate.YesNoCase {
	cases := make([]calibrate.YesNoCase, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		label, _ := pairs[i].(string)
		value, _ := pairs[i+1].(float64)
		name := caseName(len(cases))
		cases = append(cases, calibrate.YesNoCase{
			Name: name, ID: name, Line: len(cases) + 1, Yes: label == "yes", Value: value,
		})
	}

	return cases
}

func choiceCases(triples ...any) []calibrate.ChoiceCase {
	cases := make([]calibrate.ChoiceCase, 0, len(triples)/3)
	for i := 0; i < len(triples); i += 3 {
		label, _ := triples[i].(string)
		picked, _ := triples[i+1].(string)
		confidence, _ := triples[i+2].(float64)
		name := caseName(len(cases))
		cases = append(cases, calibrate.ChoiceCase{
			Name: name, ID: name, Line: len(cases) + 1, Label: label, Picked: picked, Confidence: confidence,
		})
	}

	return cases
}

func caseName(index int) string {
	return "c" + strconv.Itoa(index+1)
}

func yesNoNames(cases []calibrate.YesNoCase) []string {
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Name)
	}

	return names
}

func choiceNames(cases []calibrate.ChoiceCase) []string {
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		names = append(names, c.Name)
	}

	return names
}

func checkCutRows(t *testing.T, field string, got []calibrate.CutRow, want []cutCounts) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s has %d rows, want %d: %+v", field, len(got), len(want), got)
	}

	for i, row := range got {
		w := want[i]
		if row.Cut != w.cut || row.Flagged != w.flagged {
			t.Errorf("%s row %d = cut %v flagged %d, want cut %v flagged %d", field, i, row.Cut, row.Flagged, w.cut, w.flagged)
		}

		checkShare(t, "catches", row.Catches, w.catches[0], w.catches[1])
		checkShare(t, "false alarms", row.FalseAlarms, w.falseAlarms[0], w.falseAlarms[1])
		checkShare(t, "right when flagged", row.RightWhenFlagged, w.right[0], w.right[1])
	}
}

type cutCounts struct {
	cut                         float64
	flagged                     int
	catches, falseAlarms, right [2]int
}

func checkPickRows(t *testing.T, got []calibrate.PickRow, want []pickCounts) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("rows = %d, want %d: %+v", len(got), len(want), got)
	}

	for i, row := range got {
		w := want[i]
		if row.Name != w.name || row.Labelled != w.labelled || row.Picked != w.picked {
			t.Errorf("row %d = %s labelled %d picked %d, want %s labelled %d picked %d",
				i, row.Name, row.Labelled, row.Picked, w.name, w.labelled, w.picked)
		}

		checkShare(t, row.Name+" found", row.Found, w.found[0], w.found[1])
		checkShare(t, row.Name+" right when picked", row.RightWhenPicked, w.right[0], w.right[1])
	}
}

type pickCounts struct {
	name             string
	labelled, picked int
	found, right     [2]int
}

func checkConfidenceRow(t *testing.T, got calibrate.ConfidenceRow, cut float64, answered, agreement [2]int) {
	t.Helper()

	if got.Cut != cut {
		t.Errorf("cut = %v, want %v", got.Cut, cut)
	}

	checkShare(t, "answered", got.Answered, answered[0], answered[1])
	checkShare(t, "agreement", got.Agreement, agreement[0], agreement[1])
}

func checkShare(t *testing.T, field string, got calibrate.Share, hits, of int) {
	t.Helper()

	if want := calibrate.Wilson(hits, of); got != want {
		t.Errorf("%s = %+v, want %d of %d, %+v", field, got, hits, of, want)
	}
}

func finishes(t *testing.T, run func()) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)
		run()
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("did not finish within 10s")
	}
}
