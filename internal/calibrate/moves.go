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

// MoveYesNo lists how each record's answer moved from the baseline to a run. A record crossed the
// cut when it is flagged on one side and not the other, with baseCut on the baseline and runCut on
// the run.
func MoveYesNo(baseline, run []YesNoCase, baseCut, runCut float64, from CutSource) YesNoMoves {
	moves := YesNoMoves{BaseCut: baseCut, RunCut: runCut, From: from, Worse: []YesNoMove{}, Better: []YesNoMove{}}

	for i, before := range baseline {
		after := run[i]
		move := YesNoMove{Case: after, Before: before.Value, After: after.Value}

		// Positive is toward the wrong side of the label, so a yes falling and a no rising both got worse.
		move.Move = num.Rounded(after.Value - before.Value)
		if before.Yes {
			move.Move = -move.Move
		}

		flaggedBefore, flaggedAfter := before.Value >= baseCut, after.Value >= runCut
		move.Crossed = from != CutFromNone && flaggedBefore != flaggedAfter
		move.Small = math.Abs(move.Move) < SmallMove

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

	for _, list := range [][]YesNoMove{moves.Worse, moves.Better} {
		slices.SortStableFunc(list, func(a, b YesNoMove) int {
			return cmp.Or(
				cmp.Compare(crossedFirst(b.Crossed), crossedFirst(a.Crossed)),
				cmp.Compare(math.Abs(b.Move), math.Abs(a.Move)),
				cmp.Compare(a.Case.Line, b.Case.Line),
			)
		})
	}

	return moves
}

// YesNoMoves is one run against the baseline. Worse and Better hold the records that crossed the
// cut or moved by SmallMove or more, crossed records first, then by size. Small counts the rest.
type YesNoMoves struct {
	BaseCut, RunCut float64
	From            CutSource
	Worse, Better   []YesNoMove
	Small           int
}

// YesNoMove is one record's answer on the baseline and on the run. Case is the run's case, and Move
// is positive toward the wrong side of its label.
type YesNoMove struct {
	Case                YesNoCase
	Before, After, Move float64
	Crossed, Small      bool
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

func writeYesNoMoves(b *strings.Builder, baseline, file string, moves YesNoMoves, budget float64) {
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
		moves []YesNoMove
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
		writeYesNoRows(b, list.moves)
	}

	if moves.Small > 0 {
		fmt.Fprintf(b, "%s moved less than %s\n", wording.Plural(moves.Small, "record", "records"), cutText(SmallMove))
	}
}

func writeYesNoRows(b *strings.Builder, moves []YesNoMove) {
	shown := moves[:min(len(moves), listedMoves)]
	names, texts := make([]string, 0, len(shown)), make([]string, 0, len(shown))
	labels, values, marks := make([]string, 0, len(shown)), make([]string, 0, len(shown)), make([]string, 0, len(shown))

	for _, move := range shown {
		names = append(names, missName(move.Case.Name, move.Case.Line))
		texts = append(texts, preview(move.Case.Text))
		labels = append(labels, "labelled "+yesNoText(move.Case.Yes))
		values = append(values, twoPlaces(move.Before)+" to "+twoPlaces(move.After))

		switch {
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
