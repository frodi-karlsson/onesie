package calibrate

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/num"
	"github.com/frodi-karlsson/onesie/internal/wording"
)

const (
	// SmallMove is how far an answer can move between runs and still count as noise. Repeat runs of
	// one model moved by at most 0.05 on 200 records, and this is twice that.
	SmallMove   = 0.1
	listedMoves = 10
)

// The places the cut of a record list comes from.
const (
	CutFromNone CutSource = iota
	CutFromAssert
	CutFromBudget
)

// CutSource says where the cut of a record list comes from: the question file's assert, which
// both runs share, or each run's cut at the first false alarm budget.
type CutSource int

// MoveNoul lists how each record's answer moved from the baseline to a run. A record crossed the
// cut when it is flagged on one side and not the other, with baseCut on the baseline and runCut on
// the run.
func MoveNoul(baseline, run []NoulCase, baseCut, runCut float64, from CutSource) NoulMoves {
	moves := NoulMoves{BaseCut: baseCut, RunCut: runCut, From: from, Worse: []NoulMove{}, Better: []NoulMove{}}

	var yesTotal, noTotal float64

	for i, before := range baseline {
		after := run[i]
		move := NoulMove{Case: after, Before: before.Value, After: after.Value}

		// Positive is toward the wrong side of the label, so a yes falling and a no rising both got worse.
		move.Move = num.Rounded(after.Value - before.Value)
		if before.Yes {
			move.Move = -move.Move
		}

		flaggedBefore, flaggedAfter := before.Value >= baseCut, after.Value >= runCut
		move.Crossed = from != CutFromNone && flaggedBefore != flaggedAfter
		move.Small = math.Abs(move.Move) < SmallMove
		// The cut moved when the record would not have crossed at the baseline's cut alone.
		move.CutMoved = move.Crossed && flaggedBefore == (after.Value >= baseCut)

		if before.Yes {
			moves.Yes++
			yesTotal += after.Value - before.Value
		} else {
			moves.No++
			noTotal += after.Value - before.Value
		}

		worse := move.Move > 0
		if move.Crossed {
			worse = flaggedBefore == before.Yes
		}

		switch {
		case move.Small && !move.Crossed:
			moves.Small++
		case worse:
			moves.Worse = append(moves.Worse, move)
		default:
			moves.Better = append(moves.Better, move)
		}
	}

	// Jitter averages out, so a mean far from zero shows a shift across a whole label.
	if moves.Yes > 0 {
		moves.MeanYes = num.Rounded(yesTotal / float64(moves.Yes))
	}

	if moves.No > 0 {
		moves.MeanNo = num.Rounded(noTotal / float64(moves.No))
	}

	for _, list := range [][]NoulMove{moves.Worse, moves.Better} {
		slices.SortStableFunc(list, func(a, b NoulMove) int {
			return cmp.Or(
				cmp.Compare(crossedFirst(b.Crossed), crossedFirst(a.Crossed)),
				cmp.Compare(math.Abs(b.Move), math.Abs(a.Move)),
				cmp.Compare(a.Case.Line, b.Case.Line),
			)
		})
	}

	return moves
}

// NoulMoves is one run against the baseline. Worse and Better hold the records that crossed the
// cut or moved by SmallMove or more, crossed records first, then by size. Small counts the rest.
// MeanYes and MeanNo are the mean move in value over the Yes and No labelled records.
type NoulMoves struct {
	BaseCut, RunCut float64
	From            CutSource
	Worse, Better   []NoulMove
	Small           int
	Yes, No         int
	MeanYes, MeanNo float64
}

// NoulMove is one record's answer on the baseline and on the run. Case is the run's case, and Move
// is positive toward the wrong side of its label. CutMoved marks a crossing that happens only
// because the two runs read different cuts.
type NoulMove struct {
	Case                     NoulCase
	Before, After, Move      float64
	Crossed, Small, CutMoved bool
}

func crossedFirst(crossed bool) int {
	if crossed {
		return 1
	}

	return 0
}

// MoveChoices lists the records a run gets wrong that the baseline got right, and the reverse.
func MoveChoices(baseline, run []ChoiceCase) ChoiceMoves {
	moves := ChoiceMoves{Worse: []ChoiceMove{}, Better: []ChoiceMove{}}

	for i, before := range baseline {
		after := run[i]
		move := ChoiceMove{Case: after, BeforePick: before.Picked, BeforeConfidence: before.Confidence}

		switch {
		case before.agrees() && !after.agrees():
			moves.Worse = append(moves.Worse, move)
		case !before.agrees() && after.agrees():
			moves.Better = append(moves.Better, move)
		default:
			moves.Unchanged++
		}
	}

	return moves
}

// ChoiceMoves is one run of a pick or rate question against the baseline. Unchanged counts the
// records both runs get right or both get wrong.
type ChoiceMoves struct {
	Worse, Better []ChoiceMove
	Unchanged     int
}

// ChoiceMove is one record whose answer turned right or wrong. Case is the run's case.
type ChoiceMove struct {
	Case             ChoiceCase
	BeforePick       string
	BeforeConfidence float64
}

func writeNoulMoves(b *strings.Builder, baseline, file string, moves NoulMoves, budget float64) {
	b.WriteString("\n" + printable(file) + " against " + printable(baseline))

	switch {
	case moves.From == CutFromAssert:
		fmt.Fprintf(b, " at cut %s, the file's assert\n", cutText(moves.BaseCut))
	case moves.From == CutFromBudget && moves.BaseCut == moves.RunCut:
		fmt.Fprintf(b, " at cut %s in both, where each holds false alarms to %s\n", cutText(moves.RunCut), percentText(budget))
	case moves.From == CutFromBudget:
		fmt.Fprintf(b, " at cut %s in %s and %s in %s, where each holds false alarms to %s\n",
			cutText(moves.RunCut), printable(file), cutText(moves.BaseCut), printable(baseline), percentText(budget))
	default:
		fmt.Fprintf(b, " with no cut, since a file has no answered value holding false alarms to %s\n",
			percentText(budget))
	}

	for _, list := range []struct {
		name  string
		moves []NoulMove
	}{{"got worse", moves.Worse}, {"got better", moves.Better}} {
		if len(list.moves) == 0 {
			continue
		}

		crossed := 0
		for _, move := range list.moves {
			if move.Crossed {
				crossed++
			}
		}

		fmt.Fprintf(b, "%s, %s, %d crossed the cut\n", list.name, wording.Plural(len(list.moves), "record", "records"), crossed)
		writeNoulRows(b, list.moves)
	}

	if moves.Small > 0 {
		fmt.Fprintf(b, "%s moved less than %s\n", wording.Plural(moves.Small, "record", "records"), cutText(SmallMove))
	}

	fmt.Fprintf(b, "mean move %s on the %s, %s on the %s\n",
		signedTwoPlaces(moves.MeanYes), wording.Plural(moves.Yes, "yes record", "yes records"),
		signedTwoPlaces(moves.MeanNo), wording.Plural(moves.No, "no record", "no records"))
}

func signedTwoPlaces(value float64) string {
	text := twoPlaces(value)
	if value > 0 && text != "0.00" {
		return "+" + text
	}

	return text
}

func writeNoulRows(b *strings.Builder, moves []NoulMove) {
	shown := moves[:min(len(moves), listedMoves)]
	names, texts := make([]string, 0, len(shown)), make([]string, 0, len(shown))
	labels, values, marks := make([]string, 0, len(shown)), make([]string, 0, len(shown)), make([]string, 0, len(shown))

	for _, move := range shown {
		names = append(names, missName(move.Case.Name, move.Case.Line))
		texts = append(texts, preview(move.Case.Text))
		labels = append(labels, "labelled "+yesNoText(move.Case.Yes))
		values = append(values, twoPlaces(move.Before)+" to "+twoPlaces(move.After))

		switch {
		case move.CutMoved:
			marks = append(marks, "crossed, the cut moved")
		case move.Crossed && move.Small:
			marks = append(marks, "crossed, under "+cutText(SmallMove))
		case move.Crossed:
			marks = append(marks, "crossed")
		default:
			marks = append(marks, "")
		}
	}

	writeColumns(b, missColumns(names, texts, column{cells: labels}, column{cells: values}, column{cells: marks})...)

	if more := len(moves) - len(shown); more > 0 {
		fmt.Fprintf(b, "%sand %d more\n", indent, more)
	}
}

func writeChoiceMoves(b *strings.Builder, baseline, file string, moves ChoiceMoves) {
	b.WriteString("\n" + printable(file) + " against " + printable(baseline) + "\n")

	for _, list := range []struct {
		name  string
		moves []ChoiceMove
	}{{"got worse", moves.Worse}, {"got better", moves.Better}} {
		if len(list.moves) == 0 {
			continue
		}

		fmt.Fprintf(b, "%s, %s\n", list.name, wording.Plural(len(list.moves), "record", "records"))
		writeChoiceRows(b, list.moves)
	}

	if moves.Unchanged > 0 {
		fmt.Fprintf(b, "%s right or wrong in both\n", wording.Plural(moves.Unchanged, "record", "records"))
	}
}

func writeChoiceRows(b *strings.Builder, moves []ChoiceMove) {
	shown := moves[:min(len(moves), listedMoves)]
	names, texts := make([]string, 0, len(shown)), make([]string, 0, len(shown))
	labels, picks, confidences := make([]string, 0, len(shown)), make([]string, 0, len(shown)), make([]string, 0, len(shown))

	for _, move := range shown {
		names = append(names, missName(move.Case.Name, move.Case.Line))
		texts = append(texts, preview(move.Case.Text))
		labels = append(labels, "labelled "+printable(move.Case.Label))
		picks = append(picks, "picked "+printable(move.BeforePick)+" to "+printable(move.Case.Picked))
		confidences = append(confidences, "confidence "+twoPlaces(move.BeforeConfidence)+" to "+twoPlaces(move.Case.Confidence))
	}

	writeColumns(b, missColumns(names, texts, column{cells: labels}, column{cells: picks}, column{cells: confidences})...)

	if more := len(moves) - len(shown); more > 0 {
		fmt.Fprintf(b, "%sand %d more\n", indent, more)
	}
}
