package calibrate_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestWriteTable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		questions []calibrate.QuestionReport
		want      []string
	}{
		{
			name:      "should render a yes/no question with its cut rows and worst misses",
			questions: []calibrate.QuestionReport{urgentReport([]float64{0.5, 0.7})},
			want: []string{
				"urgent, yes/no: labelled 6, 3 yes, 3 no, 1 failed. AUC 0.89",
				"flagged means urgent.value >= cut",
				"",
				"  cut   flagged  catches         false alarms   right when flagged",
				"  0.50        3  2/3 67% 21-94%  1/3 33% 6-79%  2/3  67% 21-94%",
				"  0.70        2  2/3 67% 21-94%  0/3  0% 0-56%  2/2 100% 34-100%",
				"",
				"worst misses",
				"  T-3  labelled yes  answered 0.30",
				"  T-5  labelled no   answered 0.60",
				"  T-2  labelled yes  answered 0.80",
				"  T-6  labelled no   answered 0.20",
				"  T-1  labelled yes  answered 0.90",
			},
		},
		{
			name: "should say AUC needs both labels, print an undefined share as 0/0 with a dash and align counts of any width",
			questions: []calibrate.QuestionReport{yesNoReport("urgent", calibrate.ScoreYesNo(
				namedYesNo(
					"yes", 0.9, "yes", 0.9, "yes", 0.9, "yes", 0.9, "yes", 0.9,
					"yes", 0.9, "yes", 0.9, "yes", 0.9, "yes", 0.9, "yes", 0.9, "yes", 0.3,
				), 0, []float64{0.5, 0.95},
			))},
			want: []string{
				"urgent, yes/no: labelled 11, 11 yes, 0 no, 0 failed. AUC needs both yes and no labels",
				"flagged means urgent.value >= cut",
				"",
				"  cut   flagged  catches           false alarms  right when flagged",
				"  0.50       10  10/11 91% 62-98%  0/0 -         10/10 100% 72-100%",
				"  0.95        0   0/11  0%  0-26%  0/0 -          0/0     -",
				"",
				"worst misses",
				"  T-11  labelled yes  answered 0.30",
				"  T-1   labelled yes  answered 0.90",
				"  T-2   labelled yes  answered 0.90",
				"  T-3   labelled yes  answered 0.90",
				"  T-4   labelled yes  answered 0.90",
			},
		},
		{
			name:      "should print a cut in its shortest form with at least two places",
			questions: []calibrate.QuestionReport{urgentReport([]float64{0.9, 0.995, 1})},
			want: []string{
				"urgent, yes/no: labelled 6, 3 yes, 3 no, 1 failed. AUC 0.89",
				"flagged means urgent.value >= cut",
				"",
				"  cut    flagged  catches        false alarms  right when flagged",
				"  0.90         1  1/3 33% 6-79%  0/3 0% 0-56%  1/1 100% 21-100%",
				"  0.995        0  0/3  0% 0-56%  0/3 0% 0-56%  0/0    -",
				"  1.00         0  0/3  0% 0-56%  0/3 0% 0-56%  0/0    -",
				"",
				"worst misses",
				"  T-3  labelled yes  answered 0.30",
				"  T-5  labelled no   answered 0.60",
				"  T-2  labelled yes  answered 0.80",
				"  T-6  labelled no   answered 0.20",
				"  T-1  labelled yes  answered 0.90",
			},
		},
		{
			name:      "should render a pick question with its options, grid and confidence table",
			questions: []calibrate.QuestionReport{teamReport()},
			want: []string{
				"team, pick: labelled 6, 2 failed. agreement 67% 30-90%",
				"",
				"  option     labelled  picked  found             right when picked",
				"  billing           3       3  2/3  67% 21-94%   2/3  67% 21-94%",
				"  shipping          2       2  1/2  50%  9-91%   1/2  50%  9-91%",
				"  technical         1       1  1/1 100% 21-100%  1/1 100% 21-100%",
				"",
				`  labelled \ picked  billing  shipping  technical`,
				"  billing                  2         1          0",
				"  shipping                 1         1          0",
				"  technical                0         0          1",
				"",
				"  confidence  answered          agreement",
				"  0.50        6/6 100% 61-100%  4/6 67% 30-90%",
				"  0.80        3/6  50% 19-81%   2/3 67% 21-94%",
				"",
				"worst misses",
				"  T-3  labelled billing   picked shipping  confidence 0.95",
				"  T-5  labelled shipping  picked billing   confidence 0.70",
			},
		},
		{
			name: "should add an other column to the grid when an answer falls outside the options",
			questions: []calibrate.QuestionReport{pickReport("team", calibrate.ScorePick(
				namedChoice("billing", "billing", 0.9, "billing", "refunds", 0.8),
				[]string{"billing", "shipping"}, 0, []float64{0.5},
			))},
			want: []string{
				"team, pick: labelled 2, 0 failed. agreement 50% 9-91%",
				"",
				"  option    labelled  picked  found          right when picked",
				"  billing          2       1  1/2 50% 9-91%  1/1 100% 21-100%",
				"  shipping         0       0  0/0   -        0/0    -",
				"",
				`  labelled \ picked  billing  shipping  other`,
				"  billing                  1         0      1",
				"  shipping                 0         0      0",
				"",
				"  confidence  answered          agreement",
				"  0.50        2/2 100% 34-100%  1/2 50% 9-91%",
				"",
				"worst misses",
				"  T-2  labelled billing  picked refunds  confidence 0.80",
			},
		},
		{
			name:      "should render a rate question with within one level and mean distance, in level order",
			questions: []calibrate.QuestionReport{starsReport()},
			want: []string{
				"stars, rate: labelled 4, 0 failed. agreement 50% 15-85%",
				"within one level 75% 30-95%",
				"mean distance 0.75 levels",
				"",
				"  level  labelled  picked  found             right when picked",
				"  low           1       2  1/1 100% 21-100%  1/2  50%  9-91%",
				"  mid           2       1  1/2  50%  9-91%   1/1 100% 21-100%",
				"  high          1       1  0/1   0%  0-79%   0/1   0%  0-79%",
				"",
				`  labelled \ picked  low  mid  high`,
				"  low                  1    0     0",
				"  mid                  0    1     1",
				"  high                 1    0     0",
				"",
				"  confidence  answered          agreement",
				"  0.50        4/4 100% 51-100%  2/4 50% 15-85%",
				"",
				"worst misses",
				"  T-3  labelled high  picked low   confidence 0.60",
				"  T-2  labelled mid   picked high  confidence 0.80",
			},
		},
		{
			name: "should print each question in plan order with a blank line between them",
			questions: []calibrate.QuestionReport{
				failedReport("first"),
				pickReport("second", calibrate.ScorePick(nil, []string{"a", "b"}, 1, []float64{0.5})),
			},
			want: []string{
				"first, yes/no: labelled 0, 0 yes, 0 no, 3 failed. AUC needs both yes and no labels",
				"",
				"second, pick: labelled 0, 1 failed. agreement -",
			},
		},
		{
			name: "should print only the headline for a rate question whose records all failed",
			questions: []calibrate.QuestionReport{rateReport("stars", calibrate.ScoreRate(
				nil, []string{"low", "high"}, 2, []float64{0.5},
			))},
			want: []string{
				"stars, rate: labelled 0, 2 failed. agreement -",
			},
		},
		{
			name: "should name a miss by its line without --id",
			questions: []calibrate.QuestionReport{yesNoReport("urgent", calibrate.ScoreYesNo(
				[]calibrate.YesNoCase{{Line: 12, Yes: true, Value: 0.1}, {Line: 3, Yes: false, Value: 0.2}},
				0, []float64{0.5},
			))},
			want: []string{
				"urgent, yes/no: labelled 2, 1 yes, 1 no, 0 failed. AUC 0.00",
				"flagged means urgent.value >= cut",
				"",
				"  cut   flagged  catches       false alarms  right when flagged",
				"  0.50        0  0/1 0% 0-79%  0/1 0% 0-79%  0/0 -",
				"",
				"worst misses",
				"  line 12  labelled yes  answered 0.10",
				"  line 3   labelled no   answered 0.20",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out strings.Builder
			if err := calibrate.WriteTable(&out, calibrate.Report{Questions: tc.questions}); err != nil {
				t.Fatalf("WriteTable: %v", err)
			}

			want := strings.Join(tc.want, "\n") + "\n"
			if got := out.String(); got != want {
				t.Errorf("table =\n%s\nwant\n%s", got, want)
			}
		})
	}

	t.Run("should refuse a question with no score for its shape", func(t *testing.T) {
		t.Parallel()

		var out strings.Builder

		err := calibrate.WriteTable(&out, calibrate.Report{Questions: []calibrate.QuestionReport{{ID: "q", Shape: plan.Pick}}})
		if err == nil || !strings.Contains(err.Error(), "'q'") {
			t.Errorf("err = %v, want one naming 'q'", err)
		}
	})
}

func urgentReport(cuts []float64) calibrate.QuestionReport {
	cases := namedYesNo("yes", 0.9, "yes", 0.8, "yes", 0.3, "no", 0.1, "no", 0.6, "no", 0.2)

	return yesNoReport("urgent", calibrate.ScoreYesNo(cases, 1, cuts))
}

func teamReport() calibrate.QuestionReport {
	cases := namedChoice(
		"billing", "billing", 0.9,
		"billing", "billing", 0.6,
		"billing", "shipping", 0.95,
		"shipping", "shipping", 0.8,
		"shipping", "billing", 0.7,
		"technical", "technical", 0.5,
	)

	return pickReport("team", calibrate.ScorePick(cases, []string{"billing", "shipping", "technical"}, 2, []float64{0.5, 0.8}))
}

func starsReport() calibrate.QuestionReport {
	cases := namedChoice(
		"low", "low", 0.9,
		"mid", "high", 0.8,
		"high", "low", 0.6,
		"mid", "mid", 0.7,
	)

	return rateReport("stars", calibrate.ScoreRate(cases, []string{"low", "mid", "high"}, 0, []float64{0.5}))
}

func failedReport(id string) calibrate.QuestionReport {
	return yesNoReport(id, calibrate.ScoreYesNo(nil, 3, []float64{0.5}))
}

func yesNoReport(id string, score calibrate.YesNoScore) calibrate.QuestionReport {
	return calibrate.QuestionReport{ID: id, Shape: plan.Noul, YesNo: &score}
}

func pickReport(id string, score calibrate.PickScore) calibrate.QuestionReport {
	return calibrate.QuestionReport{ID: id, Shape: plan.Pick, Pick: &score}
}

func rateReport(id string, score calibrate.RateScore) calibrate.QuestionReport {
	return calibrate.QuestionReport{ID: id, Shape: plan.Rate, Rate: &score}
}

func namedYesNo(pairs ...any) []calibrate.YesNoCase {
	cases := yesNoCases(pairs...)
	for i := range cases {
		cases[i].Name = recordName(i)
		cases[i].ID = cases[i].Name
	}

	return cases
}

func namedChoice(triples ...any) []calibrate.ChoiceCase {
	cases := choiceCases(triples...)
	for i := range cases {
		cases[i].Name = recordName(i)
		cases[i].ID = cases[i].Name
	}

	return cases
}

func recordName(index int) string {
	return "T-" + caseName(index)[1:]
}
