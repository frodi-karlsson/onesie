package calibrate

import (
	"testing"
)

func TestMoveYesNo(t *testing.T) {
	t.Parallel()

	yesNo := func(line int, yes bool, value float64) YesNoCase {
		return YesNoCase{Line: line, Yes: yes, Value: value}
	}

	baseline := []YesNoCase{
		yesNo(1, true, 0.9), yesNo(2, true, 0.52), yesNo(3, false, 0.1), yesNo(4, false, 0.3),
		yesNo(5, true, 0.6), yesNo(6, false, 0.2),
	}
	run := []YesNoCase{
		yesNo(1, true, 0.2), yesNo(2, true, 0.48), yesNo(3, false, 0.6), yesNo(4, false, 0.32),
		yesNo(5, true, 0.95), yesNo(6, false, 0.45),
	}

	t.Run("should list crossed records first, then by size, and count the small moves", func(t *testing.T) {
		t.Parallel()

		got := MoveYesNo(baseline, run, 0.5, 0.5, CutFromAssert)

		wantWorse := []int{1, 3, 2, 6}
		if lines := movedLines(got.Worse); !equalInts(lines, wantWorse) {
			t.Errorf("worse lines = %v, want %v", lines, wantWorse)
		}

		if lines := movedLines(got.Better); !equalInts(lines, []int{5}) || got.Small != 1 {
			t.Errorf("better lines = %v and small = %d, want [5] and 1", lines, got.Small)
		}

		if small := got.Worse[2]; !small.Crossed || !small.Small || small.CutMoved || small.Move != 0.04 {
			t.Errorf("third worse = %+v, want a crossing under the small move of 0.04 at a shared cut", small)
		}
	})

	t.Run("should read each run at its own cut", func(t *testing.T) {
		t.Parallel()

		got := MoveYesNo(baseline[1:2], []YesNoCase{yesNo(2, true, 0.52)}, 0.5, 0.55, CutFromBudget)
		if len(got.Worse) != 1 || !got.Worse[0].Crossed || !got.Worse[0].CutMoved || got.Worse[0].Move != 0 {
			t.Errorf("worse = %+v, want one crossing with no move, marked as the cut moving", got.Worse)
		}
	})

	t.Run("should give the mean move in value of each label, so a shift across a label shows", func(t *testing.T) {
		t.Parallel()

		got := MoveYesNo(baseline, run, 0.5, 0.5, CutFromAssert)

		// Yes moves -0.7, -0.04 and +0.35, no moves +0.5, +0.02 and +0.25.
		if got.Yes != 3 || got.No != 3 || got.MeanYes != -0.13 || got.MeanNo != 0.2567 {
			t.Errorf("moves = %d yes at %v and %d no at %v, want 3 at -0.13 and 3 at 0.2567", got.Yes, got.MeanYes, got.No, got.MeanNo)
		}
	})

	t.Run("should not blame the cut for jitter across two cuts that nearly agree", func(t *testing.T) {
		t.Parallel()

		got := MoveYesNo([]YesNoCase{yesNo(1, true, 0.515)}, []YesNoCase{yesNo(1, true, 0.505)}, 0.51, 0.52, CutFromBudget)
		if len(got.Worse) != 1 || !got.Worse[0].Crossed || got.Worse[0].CutMoved {
			t.Errorf("worse = %+v, want a crossing that also crosses the baseline's cut", got.Worse)
		}
	})

	t.Run("should blame the cut for a large move that crosses only because the cut rose", func(t *testing.T) {
		t.Parallel()

		got := MoveYesNo([]YesNoCase{yesNo(1, true, 0.9)}, []YesNoCase{yesNo(1, true, 0.62)}, 0.5, 0.65, CutFromBudget)
		if len(got.Worse) != 1 || !got.Worse[0].Crossed || got.Worse[0].Small || !got.Worse[0].CutMoved {
			t.Errorf("worse = %+v, want a large crossing marked as the cut moving", got.Worse)
		}
	})

	t.Run("should cross nothing without a cut", func(t *testing.T) {
		t.Parallel()

		got := MoveYesNo(baseline, run, 0, 0, CutFromNone)
		for _, move := range append(got.Worse, got.Better...) {
			if move.Crossed {
				t.Errorf("move %+v crossed with no cut", move)
			}
		}

		if got.Small != 2 {
			t.Errorf("small = %d, want the 2 small moves counted", got.Small)
		}
	})
}

func movedLines(moves []YesNoMove) []int {
	lines := make([]int, len(moves))
	for i, move := range moves {
		lines[i] = move.Case.Line
	}

	return lines
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func TestMoveChoices(t *testing.T) {
	t.Parallel()

	t.Run("should list records turned wrong and turned right, and count the rest", func(t *testing.T) {
		t.Parallel()

		baseline := []ChoiceCase{
			{Line: 1, Label: "a", Picked: "a"}, {Line: 2, Label: "b", Picked: "a"}, {Line: 3, Label: "a", Picked: "a"},
		}
		run := []ChoiceCase{
			{Line: 1, Label: "a", Picked: "b"}, {Line: 2, Label: "b", Picked: "b"}, {Line: 3, Label: "a", Picked: "a"},
		}

		got := MoveChoices(baseline, run)
		if len(got.Worse) != 1 || got.Worse[0].Case.Line != 1 || got.Worse[0].BeforePick != "a" ||
			len(got.Better) != 1 || got.Better[0].Case.Line != 2 || got.Unchanged != 1 {
			t.Errorf("moves = %+v", got)
		}
	})
}
