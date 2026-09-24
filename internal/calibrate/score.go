package calibrate

import (
	"cmp"
	"math"
	"slices"
)

// ScoreYesNo scores yes/no answers against their labels. A record is flagged at a cut when its
// value is at or above the cut, and failed counts labelled records whose request failed.
func ScoreYesNo(cases []YesNoCase, failed int, cuts []float64) YesNoScore {
	var yes, no []float64

	for _, c := range cases {
		if c.Yes {
			yes = append(yes, c.Value)
		} else {
			no = append(no, c.Value)
		}
	}

	slices.Sort(yes)
	slices.Sort(no)

	score := YesNoScore{Labelled: len(cases), Yes: len(yes), No: len(no), Failed: failed}
	score.AUC, score.HasAUC = AUC(cases)

	for _, cut := range cuts {
		score.Cuts = append(score.Cuts, cutRow(cut, yes, no))
	}

	for _, value := range distinct(yes, no) {
		score.Values = append(score.Values, cutRow(value, yes, no))
	}

	score.Misses = slices.Clone(cases)
	slices.SortStableFunc(score.Misses, func(a, b YesNoCase) int {
		return cmp.Compare(gap(b), gap(a))
	})

	return score
}

// YesNoScore is how well a yes/no question's answers agree with its labels. Values holds a row at
// every distinct value answered, and Misses holds every case, widest gap first.
type YesNoScore struct {
	Labelled, Yes, No, Failed int
	AUC                       float64
	HasAUC                    bool
	Cuts, Values              []CutRow
	Misses                    []YesNoCase
}

// YesNoCase is one answered record of a yes/no question, with its label and the value answered.
type YesNoCase struct {
	Name  string
	Yes   bool
	Value float64
}

// CutRow is what a gate at Cut would do. Catches is over yes labels, FalseAlarms over no labels,
// and RightWhenFlagged over the flagged records.
type CutRow struct {
	Cut                                    float64
	Flagged                                int
	Catches, FalseAlarms, RightWhenFlagged Share
}

func cutRow(cut float64, sortedYes, sortedNo []float64) CutRow {
	caught := atOrAbove(sortedYes, cut)
	alarmed := atOrAbove(sortedNo, cut)
	flagged := caught + alarmed

	return CutRow{
		Cut:              cut,
		Flagged:          flagged,
		Catches:          Wilson(caught, len(sortedYes)),
		FalseAlarms:      Wilson(alarmed, len(sortedNo)),
		RightWhenFlagged: Wilson(caught, flagged),
	}
}

func atOrAbove(sorted []float64, cut float64) int {
	first, _ := slices.BinarySearch(sorted, cut)

	return len(sorted) - first
}

func distinct(sortedYes, sortedNo []float64) []float64 {
	values := slices.Concat(sortedYes, sortedNo)
	slices.Sort(values)

	return slices.Compact(values)
}

func gap(c YesNoCase) float64 {
	if c.Yes {
		return 1 - c.Value
	}

	return c.Value
}

// AUC is the chance that a random yes case has a higher value than a random no case, a tie counting
// half. It is the Mann Whitney U over ranks, and is undefined unless both labels occur.
func AUC(cases []YesNoCase) (float64, bool) {
	order := make([]int, len(cases))
	for i := range order {
		order[i] = i
	}

	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Compare(cases[a].Value, cases[b].Value)
	})

	yesCount := 0
	yesRanks := 0.0

	for start := 0; start < len(order); {
		end := start
		for end < len(order) && cases[order[end]].Value == cases[order[start]].Value {
			end++
		}

		rank := float64(start+1+end) / 2
		for _, i := range order[start:end] {
			if cases[i].Yes {
				yesCount++
				yesRanks += rank
			}
		}

		start = end
	}

	noCount := len(cases) - yesCount
	if yesCount == 0 || noCount == 0 {
		return 0, false
	}

	y := float64(yesCount)

	return (yesRanks - y*(y+1)/2) / (y * float64(noCount)), true
}

// ScoreRate scores rate answers against their labels as ScorePick does, over levels in order, and
// adds agreement within one level and the mean distance in levels.
func ScoreRate(cases []ChoiceCase, levels []string, failed int, cuts []float64) RateScore {
	score := RateScore{PickScore: ScorePick(cases, levels, failed, cuts)}

	distances := make([]int, len(cases))
	within, total := 0, 0

	for i, c := range cases {
		d, inside := distance(c, levels)
		distances[i] = d
		total += d

		if inside && d <= 1 {
			within++
		}
	}

	score.WithinOne = Wilson(within, len(cases))
	if len(cases) > 0 {
		score.MeanDistance = float64(total) / float64(len(cases))
	}

	score.Misses = rateMisses(cases, distances)

	return score
}

// RateScore is a PickScore over levels, plus agreement within one level and the mean distance in
// levels. Its Misses rank by distance, then by confidence.
type RateScore struct {
	PickScore
	WithinOne    Share
	MeanDistance float64
}

func distance(c ChoiceCase, levels []string) (int, bool) {
	label, picked := slices.Index(levels, c.Label), slices.Index(levels, c.Picked)
	if label < 0 || picked < 0 {
		return max(len(levels)-1, 0), false
	}

	if label > picked {
		return label - picked, true
	}

	return picked - label, true
}

func rateMisses(cases []ChoiceCase, distances []int) []ChoiceCase {
	var wrong []int

	for i, c := range cases {
		if c.Picked != c.Label {
			wrong = append(wrong, i)
		}
	}

	slices.SortStableFunc(wrong, func(a, b int) int {
		return cmp.Or(
			cmp.Compare(distances[b], distances[a]),
			cmp.Compare(cases[b].Confidence, cases[a].Confidence),
		)
	})

	misses := make([]ChoiceCase, 0, len(wrong))
	for _, i := range wrong {
		misses = append(misses, cases[i])
	}

	return misses
}

// ScorePick scores pick answers against their labels, over names in declared order. An answer
// outside names counts as wrong and lands in Other.
func ScorePick(cases []ChoiceCase, names []string, failed int, cuts []float64) PickScore {
	score := PickScore{Labelled: len(cases), Failed: failed, Grid: make([][]int, len(names))}
	for i := range score.Grid {
		score.Grid[i] = make([]int, len(names))
	}

	other := make([]int, len(names))
	labelled := make([]int, len(names))
	picked := make([]int, len(names))
	outside := false

	for _, c := range cases {
		label, pick := slices.Index(names, c.Label), slices.Index(names, c.Picked)
		if pick >= 0 {
			picked[pick]++
		}

		switch {
		case label < 0:
		case pick < 0:
			labelled[label]++
			other[label]++
			outside = true
		default:
			labelled[label]++
			score.Grid[label][pick]++
		}
	}

	if outside {
		score.Other = other
	}

	for i, name := range names {
		right := score.Grid[i][i]
		score.Names = append(score.Names, PickRow{
			Name:            name,
			Labelled:        labelled[i],
			Picked:          picked[i],
			Found:           Wilson(right, labelled[i]),
			RightWhenPicked: Wilson(right, picked[i]),
		})
	}

	score.Agreement = Wilson(agreed(cases, math.Inf(-1)), len(cases))

	for _, cut := range cuts {
		score.Confidence = append(score.Confidence, confidenceRow(cases, cut))
	}

	for _, c := range cases {
		if c.Picked != c.Label {
			score.Misses = append(score.Misses, c)
		}
	}

	slices.SortStableFunc(score.Misses, func(a, b ChoiceCase) int {
		return cmp.Compare(b.Confidence, a.Confidence)
	})

	return score
}

// PickScore is how well a pick question's answers agree with its labels. Grid counts labelled
// names by row and picked names by column, and Other is nil unless an answer fell outside the names.
type PickScore struct {
	Labelled, Failed int
	Agreement        Share
	Names            []PickRow
	Grid             [][]int
	Other            []int
	Confidence       []ConfidenceRow
	Misses           []ChoiceCase
}

// ChoiceCase is one answered record of a pick or rate question, with its label, the name picked
// and the confidence given.
type ChoiceCase struct {
	Name, Label, Picked string
	Confidence          float64
}

// PickRow is one option or level. Found is over the records labelled with it, and RightWhenPicked
// over the records picked as it.
type PickRow struct {
	Name                   string
	Labelled, Picked       int
	Found, RightWhenPicked Share
}

// ConfidenceRow is what keeping only answers with confidence at or above Cut would do. Agreement
// is measured over the answers kept.
type ConfidenceRow struct {
	Cut                 float64
	Answered, Agreement Share
}

func confidenceRow(cases []ChoiceCase, cut float64) ConfidenceRow {
	answered := 0
	for _, c := range cases {
		if c.Confidence >= cut {
			answered++
		}
	}

	return ConfidenceRow{
		Cut:       cut,
		Answered:  Wilson(answered, len(cases)),
		Agreement: Wilson(agreed(cases, cut), answered),
	}
}

func agreed(cases []ChoiceCase, cut float64) int {
	count := 0
	for _, c := range cases {
		if c.Confidence >= cut && c.Picked == c.Label {
			count++
		}
	}

	return count
}
