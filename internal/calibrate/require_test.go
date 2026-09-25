package calibrate_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestParseRequirement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		want    calibrate.Requirement
		wantErr string
	}{
		{
			name: "should read a point requirement",
			text: "instructs.catches >= 0.95",
			want: calibrate.Requirement{
				Source: "instructs.catches >= 0.95", ID: "instructs", Bound: calibrate.Point,
				Measure: calibrate.Catches, Op: calibrate.OpGE, Threshold: 0.95,
			},
		},
		{
			name: "should read a lower bound",
			text: "lower(instructs.catches) > 0.8",
			want: calibrate.Requirement{
				Source: "lower(instructs.catches) > 0.8", ID: "instructs", Bound: calibrate.Lower,
				Measure: calibrate.Catches, Op: calibrate.OpGT, Threshold: 0.8,
			},
		},
		{
			name: "should read an upper bound at a cut",
			text: "upper(overrides.false_alarms) <= 0.25 at 0.9",
			want: calibrate.Requirement{
				Source: "upper(overrides.false_alarms) <= 0.25 at 0.9", ID: "overrides", Bound: calibrate.Upper,
				Measure: calibrate.FalseAlarms, Op: calibrate.OpLE, Threshold: 0.25,
				At: calibrate.At{Kind: calibrate.AtNumber, Value: 0.9},
			},
		},
		{
			name: "should read a requirement written without spaces",
			text: "tone.within_one>=0.9",
			want: calibrate.Requirement{
				Source: "tone.within_one>=0.9", ID: "tone", Bound: calibrate.Point,
				Measure: calibrate.WithinOne, Op: calibrate.OpGE, Threshold: 0.9,
			},
		},
		{
			name: "should read auc",
			text: "urgent.auc >= 0.9",
			want: calibrate.Requirement{
				Source: "urgent.auc >= 0.9", ID: "urgent", Bound: calibrate.Point,
				Measure: calibrate.AreaUnderCurve, Op: calibrate.OpGE, Threshold: 0.9,
			},
		},
		{
			name: "should take the id as everything before the last dot",
			text: "x.y.catches >= 0.5",
			want: calibrate.Requirement{
				Source: "x.y.catches >= 0.5", ID: "x.y", Bound: calibrate.Point,
				Measure: calibrate.Catches, Op: calibrate.OpGE, Threshold: 0.5,
			},
		},
		{
			name: "should read at abstain",
			text: "urgent.catches >= 0.9 at abstain",
			want: calibrate.Requirement{
				Source: "urgent.catches >= 0.9 at abstain", ID: "urgent", Bound: calibrate.Point,
				Measure: calibrate.Catches, Op: calibrate.OpGE, Threshold: 0.9,
				At: calibrate.At{Kind: calibrate.AtAbstain},
			},
		},
		{name: "should refuse a percentage", text: "urgent.catches >= 95", wantErr: "write 0.95, not 95"},
		{name: "should refuse lower with at most", text: "lower(urgent.catches) <= 0.2", wantErr: "lower takes >= or >"},
		{name: "should refuse upper with at least", text: "upper(urgent.catches) >= 0.2", wantErr: "upper takes <= or <"},
		{name: "should refuse an interval on auc", text: "lower(urgent.auc) >= 0.8", wantErr: "auc has no interval"},
		{name: "should refuse a cut on auc", text: "urgent.auc >= 0.8 at 0.5", wantErr: "auc covers every cut"},
		{
			name: "should refuse an unknown measure and list the measures", text: "urgent.recall >= 0.9",
			wantErr: "catches, false_alarms, right_when_flagged, auc, agreement or within_one",
		},
		{name: "should refuse equality", text: "urgent.catches == 0.9", wantErr: "takes >=, >, <= or <"},
		{name: "should refuse a negative number", text: "urgent.catches >= -0.1", wantErr: "between 0 and 1"},
		{name: "should refuse a cut above 1", text: "urgent.catches >= 0.9 at 1.5", wantErr: "write 0.015, not 1.5"},
		{name: "should refuse at with no cut", text: "urgent.catches >= 0.9 at", wantErr: "at needs a cut"},
		{name: "should refuse an empty text", text: "  ", wantErr: "is empty"},
		{name: "should refuse text after the cut", text: "urgent.catches >= 0.9 at 0.5 please", wantErr: "'please'"},
		{name: "should refuse a cut on agreement", text: "tone.agreement >= 0.9 at 0.5", wantErr: "at applies only"},
		{name: "should refuse a missing measure", text: "urgent >= 0.9", wantErr: "ID.MEASURE"},
		{name: "should refuse an id with a space", text: "my q.catches >= 0.9", wantErr: "'my q'"},
		{name: "should refuse NaN", text: "urgent.catches >= NaN", wantErr: "between 0 and 1"},
		{name: "should refuse a space before the bound's parenthesis", text: "lower (urgent.catches) >= 0.9", wantErr: "no space before ("},
		{name: "should quote a number above 100", text: "urgent.catches >= 150", wantErr: "between 0 and 1, got '150'"},
		{name: "should refuse an unclosed bound", text: "lower(urgent.catches >= 0.9", wantErr: "closing )"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := calibrate.ParseRequirement(tc.text)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseRequirement(%q) = %+v, %v, want an error containing %q", tc.text, got, err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("ParseRequirement(%q) error = %v", tc.text, err)
			}

			if got != tc.want {
				t.Errorf("ParseRequirement(%q) = %+v, want %+v", tc.text, got, tc.want)
			}
		})
	}
}

func TestRequirement_Check(t *testing.T) {
	t.Parallel()

	// At cut 0.5: 3 of 4 yes flagged, 1 of 4 no flagged, 3 of 4 flagged right.
	yesNo := calibrate.ScoreYesNo(
		yesNoCases("yes", 0.9, "yes", 0.8, "yes", 0.6, "yes", 0.2, "no", 0.7, "no", 0.3, "no", 0.1, "no", 0.05),
		0, []float64{0.5, 0.95})
	yesOnly := calibrate.ScoreYesNo(yesNoCases("yes", 0.9, "yes", 0.8), 0, []float64{0.5})
	pick := calibrate.ScorePick(
		choiceCases("a", "a", 0.9, "b", "b", 0.9, "a", "b", 0.9, "b", "b", 0.9), []string{"a", "b"}, 0, nil)
	rate := calibrate.ScoreRate(
		choiceCases("1", "1", 0.9, "2", "3", 0.9, "3", "1", 0.9, "3", "3", 0.9), []string{"1", "2", "3"}, 0, nil)

	yesNoQ := calibrate.QuestionReport{ID: "x", Shape: plan.Noul, YesNo: &yesNo}
	pickQ := calibrate.QuestionReport{ID: "x", Shape: plan.Pick, Pick: &pick}
	rateQ := calibrate.QuestionReport{ID: "x", Shape: plan.Rate, Rate: &rate}

	tests := []struct {
		name      string
		text      string
		q         calibrate.QuestionReport
		cut       float64
		wantHeld  bool
		wantValue float64
		wantCut   bool
		reason    string
	}{
		{name: "should hold catches at its point value", text: "x.catches >= 0.75", q: yesNoQ, cut: 0.5, wantHeld: true, wantValue: 0.75, wantCut: true},
		{name: "should fail a strict greater at the boundary", text: "x.catches > 0.75", q: yesNoQ, cut: 0.5, wantValue: 0.75, wantCut: true},
		{name: "should hold at most at the boundary", text: "x.false_alarms <= 0.25", q: yesNoQ, cut: 0.5, wantHeld: true, wantValue: 0.25, wantCut: true},
		{name: "should fail a strict less at the boundary", text: "x.false_alarms < 0.25", q: yesNoQ, cut: 0.5, wantValue: 0.25, wantCut: true},
		{name: "should read right when flagged", text: "x.right_when_flagged >= 0.7", q: yesNoQ, cut: 0.5, wantHeld: true, wantValue: 0.75, wantCut: true},
		{name: "should read the cut it is given", text: "x.catches >= 0.75", q: yesNoQ, cut: 0.95, wantCut: true},
		{name: "should read auc", text: "x.auc >= 0.8", q: yesNoQ, cut: 0.5, wantHeld: true, wantValue: 0.8125},
		{name: "should compare the lower end of the interval", text: "lower(x.catches) >= 0.75", q: yesNoQ, cut: 0.5, wantValue: 0.75, wantCut: true},
		{name: "should compare the upper end of the interval", text: "upper(x.false_alarms) <= 0.9", q: yesNoQ, cut: 0.5, wantHeld: true, wantValue: 0.25, wantCut: true},
		{name: "should read agreement on a pick", text: "x.agreement >= 0.75", q: pickQ, wantHeld: true, wantValue: 0.75},
		{name: "should read agreement on a rate", text: "x.agreement < 0.75", q: rateQ, wantHeld: true, wantValue: 0.5},
		{name: "should read within one on a rate", text: "x.within_one >= 0.75", q: rateQ, wantHeld: true, wantValue: 0.75},
		{name: "should fail a measure over no records", text: "x.false_alarms <= 0.5", q: calibrate.QuestionReport{ID: "x", Shape: plan.Noul, YesNo: &yesOnly}, cut: 0.5, wantCut: true, reason: "no record is labelled no"},
		{name: "should fail auc without both labels", text: "x.auc >= 0.5", q: calibrate.QuestionReport{ID: "x", Shape: plan.Noul, YesNo: &yesOnly}, reason: "both yes and no labels"},
		{name: "should fail a cut the report has no row for", text: "x.catches >= 0.5", q: yesNoQ, cut: 0.33, wantCut: true, reason: "no row at cut 0.33"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req, err := calibrate.ParseRequirement(tc.text)
			if err != nil {
				t.Fatalf("ParseRequirement(%q) error = %v", tc.text, err)
			}

			got := req.Check(tc.q, tc.cut)
			if got.Held != tc.wantHeld || got.HasCut != tc.wantCut || got.Reason != "" && !strings.Contains(got.Reason, tc.reason) {
				t.Fatalf("Check = %+v, want held %v, cut %v, reason %q", got, tc.wantHeld, tc.wantCut, tc.reason)
			}

			if tc.reason != "" {
				if got.HasValue || !strings.Contains(got.Reason, tc.reason) {
					t.Errorf("Check = %+v, want no value and a reason containing %q", got, tc.reason)
				}

				return
			}

			if !got.HasValue || got.Value != tc.wantValue {
				t.Errorf("Check value = %v, %v, want %v", got.Value, got.HasValue, tc.wantValue)
			}
		})
	}

	t.Run("should fail 16 of 16 on its lower bound", func(t *testing.T) {
		t.Parallel()

		pairs := make([]any, 0, 32)
		for range 16 {
			pairs = append(pairs, "yes", 0.9)
		}

		score := calibrate.ScoreYesNo(yesNoCases(pairs...), 0, []float64{0.5})
		req, err := calibrate.ParseRequirement("lower(x.catches) >= 0.9")
		if err != nil {
			t.Fatal(err)
		}

		got := req.Check(calibrate.QuestionReport{ID: "x", Shape: plan.Noul, YesNo: &score}, 0.5)
		if got.Held || !got.HasInterval || got.Low < 0.8 || got.Low > 0.82 || got.Value != 1 {
			t.Errorf("Check = %+v, want a failure with a lower bound near 0.81", got)
		}
	})

	t.Run("should render a failing result with its share and cut", func(t *testing.T) {
		t.Parallel()

		pairs := make([]any, 0, 32)
		for i := range 16 {
			value := 0.9
			if i == 0 {
				value = 0.1
			}

			pairs = append(pairs, "yes", value)
		}

		score := calibrate.ScoreYesNo(yesNoCases(pairs...), 0, []float64{0.25})
		req, err := calibrate.ParseRequirement("instructs.catches >= 0.95")
		if err != nil {
			t.Fatal(err)
		}

		got := req.Check(calibrate.QuestionReport{ID: "instructs", Shape: plan.Noul, YesNo: &score}, 0.25).String()
		want := "'instructs.catches >= 0.95' did not hold: 15/16 94% 72-99% at cut 0.25"
		if got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	})
}
