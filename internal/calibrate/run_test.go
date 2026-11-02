package calibrate

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/planbuild"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

func TestReadLabelled(t *testing.T) {
	t.Parallel()

	t.Run("should give each labelled record its position among the input records", func(t *testing.T) {
		t.Parallel()

		mapper, err := planbuild.Expr("--map", ".body")
		if err != nil {
			t.Fatal(err)
		}

		label, err := planbuild.Expr("--label u", ".u")
		if err != nil {
			t.Fatal(err)
		}

		config := Config{
			Input:     strings.NewReader("{\"body\":\"a\",\"u\":true}\n{\"body\":\"b\"}\n\n{\"body\":\"c\",\"u\":1}\n"),
			InputMode: input.JSONL,
			SkipBlank: true,
			Mapper:    mapper,
		}
		labels := []QuestionLabel{{ID: "u", Shape: plan.Noul, Expr: label}}

		set, err := ReadLabelled(t.Context(), config, labels)
		if err != nil {
			t.Fatal(err)
		}

		got := make([][2]int, 0, len(set.Records))
		for _, rec := range set.Records {
			got = append(got, [2]int{rec.position, rec.line})
		}

		want := [][2]int{{1, 1}, {3, 4}}
		if !slices.Equal(got, want) {
			t.Errorf("position and line = %v, want %v", got, want)
		}
	})

	t.Run("should return a ReadError carrying the read count and the header", func(t *testing.T) {
		t.Parallel()

		namer, err := planbuild.Expr("--id", ".id")
		if err != nil {
			t.Fatal(err)
		}

		config := Config{Input: strings.NewReader("id\tbody\tu\n1\ta\ttrue\n"), InputMode: input.CSV, Namer: namer}

		_, err = ReadLabelled(t.Context(), config, urgentLabel(t))

		var unread *ReadError
		if !errors.As(err, &unread) || unread.Read != 1 || !slices.Equal(unread.Header, []string{"id\tbody\tu"}) ||
			!errors.Is(err, jq.ErrNullID) {
			t.Errorf("ReadLabelled error = %#v, want a ReadError after one record under its one column header", err)
		}
	})

	t.Run("should name the label setting as a tool argument for that speller", func(t *testing.T) {
		t.Parallel()

		config := Config{
			Input: strings.NewReader("{\"u\":\"maybe\"}\n"), InputMode: input.JSONL, Speller: plan.Arguments,
		}

		_, err := ReadLabelled(t.Context(), config, urgentLabel(t))
		if err == nil || !strings.HasPrefix(err.Error(), "label u: line 1: ") {
			t.Errorf("ReadLabelled error = %v, want it to name the label setting as label", err)
		}
	})
}

func urgentLabel(t *testing.T) []QuestionLabel {
	t.Helper()

	label, err := planbuild.Expr("--label u", ".u")
	if err != nil {
		t.Fatal(err)
	}

	return []QuestionLabel{{ID: "u", Shape: plan.Noul, Expr: label}}
}

func TestScopeOf(t *testing.T) {
	t.Parallel()

	built := &plan.Plan{Questions: []plan.Question{{ID: "urgent", Shape: plan.Noul}}}

	tests := []struct {
		name    string
		specs   []string
		speller plan.Speller
		want    string
	}{
		{
			name: "should name the label setting as a tool argument for that speller", speller: plan.Arguments,
			want: "question 'urgent' has no label, so asking it would cost requests and report nothing. " +
				"Pass label urgent=EXPR",
		},
		{
			name: "should name the label in a compile error the way the speller does", speller: plan.Arguments,
			specs: []string{"urgent=.u |"}, want: "label urgent: ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := ScopeOf(built, tc.specs, tc.speller); err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Errorf("ScopeOf error = %v, want one starting %q", err, tc.want)
			}
		})
	}
}

func TestAskLabelled(t *testing.T) {
	t.Parallel()

	built := &plan.Plan{Questions: []plan.Question{{ID: "u", Shape: plan.Noul}}}

	tests := []struct {
		name string
		want float64
	}{
		{name: "should ask a pending record with no images when Images is nil", want: 0.9},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stub := &recordingAnswerer{}
			config := Config{
				Ask:  runner.Ask{Plan: built, Model: "m", Questions: plan.WireAll(built.Questions), Message: errorText},
				Jobs: 1,
			}
			pending := []LabelledRecord{{position: 1, line: 1, sent: json.RawMessage(`"a"`), labels: []*Label{{Yes: true}}}}
			set := ResumedSet{Pending: pending, Answered: make([]output.Record, 1)}
			answerers := func(context.Context, *runner.Collector) (runner.Answerer, error) { return stub, nil }

			outcomes, _, err := AskLabelled(t.Context(), config, nil, set, answerers, &runner.Collector{})
			if err != nil {
				t.Fatalf("AskLabelled: %v", err)
			}

			if got := outcomes[0].Answers[0].Answer.Value; got != tc.want {
				t.Errorf("answer = %v, want %v", got, tc.want)
			}

			if len(stub.requests) != 1 || stub.requests[0].Assets != nil {
				t.Errorf("requests = %+v, want one with no assets", stub.requests)
			}
		})
	}
}

func errorText(err error) string {
	return err.Error()
}

func (a *recordingAnswerer) Answer(_ context.Context, _ runner.RecordKey, req onesie.Request) (runner.Reply, error) {
	a.requests = append(a.requests, req)

	return runner.Reply{Result: &onesie.Result{
		Model: "m", Answers: map[string]onesie.Answer{"u": &onesie.NoulAnswer{Noul: 0.9}},
	}}, nil
}

func (*recordingAnswerer) Salt(runner.RecordKey) (string, bool) {
	return "", true
}

type recordingAnswerer struct {
	requests []onesie.Request
}

func TestReportOf(t *testing.T) {
	t.Parallel()

	yes := &Label{Yes: true}
	billing := &Label{Name: "billing"}

	built := &plan.Plan{Questions: []plan.Question{
		{ID: "urgent", Shape: plan.Noul},
		{ID: "team", Shape: plan.Pick, Options: []plan.Option{{Name: "billing"}, {Name: "shipping"}}},
	}}

	confidence := func(value float64) *float64 {
		return &value
	}

	answeredWith := func(value any, picked any, conf *float64) output.Record {
		return output.Record{Model: "m", Answers: []output.Named{
			{ID: "urgent", Answer: &answer.Answer{Value: value}},
			{ID: "team", Answer: &answer.Answer{Value: picked, Confidence: conf}},
		}}
	}

	tests := []struct {
		name       string
		record     output.Record
		wantFailed int
	}{
		{name: "should score a value and a confidence inside [0,1]", record: answeredWith(0.5, "billing", confidence(0.5))},
		{name: "should fail a yes/no value that is NaN", record: answeredWith(math.NaN(), "billing", confidence(0.5)), wantFailed: 1},
		{name: "should fail a yes/no value above 1", record: answeredWith(1.01, "billing", confidence(0.5)), wantFailed: 1},
		{name: "should fail a yes/no value below 0", record: answeredWith(-0.01, "billing", confidence(0.5)), wantFailed: 1},
		{name: "should fail a yes/no value that is not a number", record: answeredWith("yes", "billing", confidence(0.5)), wantFailed: 1},
		{name: "should fail a confidence that is NaN", record: answeredWith(0.5, "billing", confidence(math.NaN())), wantFailed: 1},
		{name: "should fail a confidence above 1", record: answeredWith(0.5, "billing", confidence(2)), wantFailed: 1},
		{name: "should fail a missing confidence", record: answeredWith(0.5, "billing", nil), wantFailed: 1},
		{name: "should fail a pick that is not a string", record: answeredWith(0.5, 3, confidence(0.5)), wantFailed: 1},
		{
			name:       "should fail a record whose request failed",
			record:     runner.Ask{Plan: built, Message: errorText}.Failure(errors.New("boom")),
			wantFailed: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			set := LabelledSet{
				Records: []LabelledRecord{{line: 1, id: "T-1", labels: []*Label{yes, billing}}},
				Total:   1,
			}

			report := ReportOf(built, set, []output.Record{tc.record}, CutsFor([]float64{0.5}, nil, len(built.Questions)))

			if report.Failed != tc.wantFailed {
				t.Errorf("report failed = %d, want %d", report.Failed, tc.wantFailed)
			}

			urgent, team := report.Questions[0].YesNo, report.Questions[1].Pick
			if urgent.Failed != tc.wantFailed || team.Failed != tc.wantFailed {
				t.Errorf("question failed = %d and %d, want %d each", urgent.Failed, team.Failed, tc.wantFailed)
			}

			if urgent.Labelled != 1-tc.wantFailed || team.Labelled != 1-tc.wantFailed {
				t.Errorf("question labelled = %d and %d, want %d each", urgent.Labelled, team.Labelled,
					1-tc.wantFailed)
			}

			if tc.wantFailed == 0 {
				if miss := urgent.Cuts[0]; miss.Flagged != 1 {
					t.Errorf("flagged at 0.5 = %d, want the value 0.5 flagged", miss.Flagged)
				}

				if !slices.Equal(report.Models, []string{"m"}) {
					t.Errorf("models = %v, want [m]", report.Models)
				}
			}
		})
	}

	t.Run("should name a case by its id text with --id and leave the id nil without one", func(t *testing.T) {
		t.Parallel()

		noul := &plan.Plan{Questions: []plan.Question{{ID: "urgent", Shape: plan.Noul}}}
		no := &Label{Yes: false}
		missed := output.Record{Model: "m", Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 0.9}}}}

		set := LabelledSet{Records: []LabelledRecord{
			{line: 1, id: json.Number("7"), labels: []*Label{no}},
			{line: 2, labels: []*Label{no}},
		}, Total: 2}

		misses := ReportOf(noul, set, []output.Record{missed, missed}, CutsFor([]float64{0.5}, nil, len(noul.Questions))).Questions[0].YesNo.Misses

		if len(misses) != 2 {
			t.Fatalf("misses = %+v, want two", misses)
		}

		if misses[0].Name != "7" || misses[0].ID != json.Number("7") || misses[0].Line != 1 {
			t.Errorf("first miss = %+v, want name 7, id 7, line 1", misses[0])
		}

		if misses[1].Name != "" || misses[1].ID != nil || misses[1].Line != 2 {
			t.Errorf("second miss = %+v, want no name, a nil id and line 2", misses[1])
		}
	})
}

func TestCutsFor(t *testing.T) {
	t.Parallel()

	bound := func(texts ...string) []BoundRequirement {
		out := make([]BoundRequirement, 0, len(texts))

		for _, text := range texts {
			req, err := ParseRequirement(text)
			if err != nil {
				t.Fatal(err)
			}

			out = append(out, BoundRequirement{Requirement: req, question: 0, cut: req.At.Value, hasCut: true})
		}

		return out
	}

	tests := []struct {
		name  string
		base  []float64
		bound []BoundRequirement
		want  [][]float64
	}{
		{
			name:  "should hold a cut once when 0.50, 0.5 and --cuts 0.5 meet",
			base:  []float64{0.5},
			bound: bound("u.catches >= 0.5 at 0.50", "u.false_alarms <= 0.5 at 0.5"),
			want:  [][]float64{{0.5}, {0.5}},
		},
		{
			name:  "should add a new cut in order to its own question only",
			base:  []float64{0.25, 0.75},
			bound: bound("u.catches >= 0.5 at 0.5", "u.catches >= 0.5 at 0.5"),
			want:  [][]float64{{0.25, 0.5, 0.75}, {0.25, 0.75}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := CutsFor(tc.base, tc.bound, 2)
			if !slices.EqualFunc(got, tc.want, slices.Equal[[]float64]) {
				t.Errorf("cutsFor = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCasesOf(t *testing.T) {
	t.Parallel()

	built := &plan.Plan{Questions: []plan.Question{{ID: "urgent", Shape: plan.Noul}}}
	rec := LabelledRecord{line: 1, labels: []*Label{{Yes: true}}}

	tests := []struct {
		name   string
		record output.Record
		want   string
	}{
		{
			name:   "should refuse a record with no answer to a labelled question",
			record: output.Record{},
			want:   "question 'urgent' has no answer",
		},
		{
			name:   "should refuse a yes or no answer outside [0,1]",
			record: output.Record{Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 1.5}}}},
			want:   "question 'urgent' answered 1.5, which lies outside [0,1]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := casesOf(built, rec, tc.record)

			var unusable *onesie.ResponseError
			if err == nil || errors.As(err, &unusable) || err.Error() != tc.want {
				t.Errorf("casesOf error = %v, want a plain error reading %q", err, tc.want)
			}
		})
	}
}
