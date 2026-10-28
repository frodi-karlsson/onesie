package answer_test

import (
	"encoding/json"
	"math"
	"sort"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		question plan.Question
		raw      onesie.Answer
		want     string
	}{
		{
			name:     "should report a yes/no answer as a bare value",
			question: plan.Question{ID: "urgent", Shape: plan.Noul},
			raw:      &onesie.NoulAnswer{Noul: 0.92},
			want:     `{"value":0.92}`,
		},
		{
			name:     "should keep a zero probability rather than omitting it",
			question: plan.Question{ID: "urgent", Shape: plan.Noul},
			raw:      &onesie.NoulAnswer{Noul: 0},
			want:     `{"value":0}`,
		},
		{
			name: "should keep pick probabilities in plan order",
			question: plan.Question{
				ID:    "team",
				Shape: plan.Pick,
				Options: []plan.Option{
					{Name: "billing"}, {Name: "technical"}, {Name: "sales"},
				},
			},
			raw: &onesie.ChoiceAnswer{
				Choice:     "technical",
				Confidence: 0.81,
				Probabilities: map[string]float64{
					"sales": 0.02, "billing": 0.10, "technical": 0.88,
				},
			},
			want: `{"value":"technical","confidence":0.81,` +
				`"p":{"billing":0.1,"technical":0.88,"sales":0.02}}`,
		},
		{
			name: "should re-key score probabilities to labels by position",
			question: plan.Question{
				ID:       "frustration",
				Shape:    plan.Rate,
				Labelled: true,
				Levels: []plan.Level{
					{Label: "calm"}, {Label: "frustrated"}, {Label: "angry"},
				},
			},
			raw: &onesie.ScoreAnswer{
				Score:         1.0,
				Confidence:    0.92,
				Legend:        map[string]string{"0": "Calm", "1": "Frustrated", "2": "Angry"},
				Probabilities: map[string]float64{"0": 0.05, "1": 0.90, "2": 0.05},
			},
			want: `{"value":"frustrated","score":1,"norm":0.5,"confidence":0.92,` +
				`"p":{"calm":0.05,"frustrated":0.9,"angry":0.05}}`,
		},
		{
			name: "should break a tie to the lowest level index",
			question: plan.Question{
				ID:       "severity",
				Shape:    plan.Rate,
				Labelled: true,
				Levels:   []plan.Level{{Label: "low"}, {Label: "high"}},
			},
			raw: &onesie.ScoreAnswer{
				Score:         0.5,
				Confidence:    0.5,
				Probabilities: map[string]float64{"0": 0.5, "1": 0.5},
			},
			want: `{"value":"low","score":0.5,"norm":0.5,"confidence":0.5,` +
				`"p":{"low":0.5,"high":0.5}}`,
		},
		{
			name: "should sort indexes numerically rather than lexically",
			question: plan.Question{
				ID:       "severity",
				Shape:    plan.Rate,
				Labelled: true,
				Levels: []plan.Level{
					{Label: "a"},
					{Label: "b"},
					{Label: "c"},
					{Label: "d"},
					{Label: "e"},
					{Label: "f"},
					{Label: "g"},
					{Label: "h"},
					{Label: "i"},
					{Label: "j"},
				},
			},
			raw: &onesie.ScoreAnswer{
				Score:      9,
				Confidence: 0.5,
				Probabilities: map[string]float64{
					"0": 0.1, "1": 0.1, "2": 0.1, "3": 0.1, "4": 0.1,
					"5": 0.1, "6": 0.1, "7": 0.1, "8": 0.1, "9": 0.1,
				},
			},
			want: `{"value":"a","score":9,"norm":1,"confidence":0.5,` +
				`"p":{"a":0.1,"b":0.1,"c":0.1,"d":0.1,"e":0.1,` +
				`"f":0.1,"g":0.1,"h":0.1,"i":0.1,"j":0.1}}`,
		},
		{
			name: "should keep the legend and index keys when labels are absent",
			question: plan.Question{
				ID:       "frustration",
				Shape:    plan.Rate,
				Labelled: false,
				Levels: []plan.Level{
					{Desc: "Calm"}, {Desc: "Frustrated"}, {Desc: "Very angry"},
				},
			},
			raw: &onesie.ScoreAnswer{
				Score:         1.0,
				Confidence:    0.92,
				Legend:        map[string]string{"0": "Calm", "1": "Frustrated", "2": "Very angry"},
				Probabilities: map[string]float64{"0": 0.05, "1": 0.90, "2": 0.05},
			},
			want: `{"value":"1","score":1,"norm":0.5,"confidence":0.92,` +
				`"p":{"0":0.05,"1":0.9,"2":0.05},` +
				`"legend":{"0":"Calm","1":"Frustrated","2":"Very angry"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			encoded, err := json.Marshal(answer.Normalize(tc.question, tc.raw))
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}

			if string(encoded) != tc.want {
				t.Errorf("got  %s\nwant %s", encoded, tc.want)
			}
		})
	}

	t.Run("should panic on an answer that never passed Result.Check", func(t *testing.T) {
		t.Parallel()

		defer func() {
			want := "answer: question 'urgent' has a <nil> answer, so Result.Check did not run before Normalize"
			if got := recover(); got != want {
				t.Errorf("panic = %v, want %q", got, want)
			}
		}()

		answer.Normalize(plan.Question{ID: "urgent", Shape: plan.Noul}, nil)
	})

	t.Run("should flatten a box into its four edges and confidence", func(t *testing.T) {
		t.Parallel()

		got := answer.Normalize(plan.Question{ID: "mug", Shape: plan.Box}, &onesie.BoxAnswer{
			Edges:      onesie.Edges{Top: 0.25, Bottom: 0.82, Left: 0.44, Right: 0.84},
			Confidence: 0.92,
		})

		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if want := `{"left":0.44,"top":0.25,"right":0.84,"bottom":0.82,"confidence":0.92}`; string(encoded) != want {
			t.Errorf("json = %s, want %s", encoded, want)
		}
	})
}

func TestProbabilitiesMarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		values  map[string]float64
		wantErr bool
	}{
		{
			name:   "should encode finite probabilities in key order",
			values: map[string]float64{"b": 0.25, "a": 0.75},
		},
		{
			name:    "should refuse to encode a NaN rather than emitting invalid json",
			values:  map[string]float64{"a": math.NaN()},
			wantErr: true,
		},
		{
			name:    "should refuse to encode an infinity rather than emitting invalid json",
			values:  map[string]float64{"a": math.Inf(1)},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			keys := make([]string, 0, len(tc.values))
			for key := range tc.values {
				keys = append(keys, key)
			}

			sort.Strings(keys)

			_, err := json.Marshal(answer.Probabilities{Keys: keys, Values: tc.values})

			if tc.wantErr && err == nil {
				t.Error("expected an error, got none")
			}

			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestNormalizeAll(t *testing.T) {
	t.Parallel()

	questions := []plan.Question{{ID: "a", Shape: plan.Noul}, {ID: "b", Shape: plan.Noul}}

	t.Run("should normalize every answer in question order", func(t *testing.T) {
		t.Parallel()

		result := &onesie.Result{Answers: map[string]onesie.Answer{
			"b": &onesie.NoulAnswer{Noul: 0.2}, "a": &onesie.NoulAnswer{Noul: 0.9},
		}}

		got := answer.NormalizeAll(questions, result)

		if len(got) != 2 || got[0].Value != 0.9 || got[1].Value != 0.2 {
			t.Errorf("NormalizeAll = %+v, want a then b", got)
		}
	})
}

func TestSumFits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		options int
		sum     float64
		fits    bool
	}{
		{name: "should accept two options summing to 0.98", options: 2, sum: 0.98, fits: true},
		{name: "should accept two options summing to 1.02", options: 2, sum: 1.02, fits: true},
		{name: "should refuse two options summing to 0.97", options: 2, sum: 0.97},
		{name: "should refuse twenty options summing to 0.9", options: 20, sum: 0.9},
		{name: "should refuse twenty options summing to 1.05", options: 20, sum: 1.05},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			probabilities := make([]float64, tc.options)
			for i := range probabilities {
				probabilities[i] = tc.sum / float64(tc.options)
			}

			if _, fits := answer.SumFits(probabilities); fits != tc.fits {
				t.Errorf("SumFits = %v, want %v", fits, tc.fits)
			}
		})
	}
}
