package answer_test

import (
	"encoding/json"
	"errors"
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

			got, err := answer.Normalize(tc.question, tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshalling: %v", err)
			}

			if string(encoded) != tc.want {
				t.Errorf("got  %s\nwant %s", encoded, tc.want)
			}
		})
	}

	t.Run("should refuse an answer that does not fit the question asked", func(t *testing.T) {
		t.Parallel()

		const prefix = "200 response could not be used: "

		team := plan.Question{
			ID: "team", Shape: plan.Pick,
			Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
		}
		severity := plan.Question{
			ID: "severity", Shape: plan.Rate, Labelled: true,
			Levels: []plan.Level{{Label: "low"}, {Label: "mid"}, {Label: "high"}},
		}
		choice := func(picked string, confidence float64, p map[string]float64) onesie.Answer {
			return &onesie.ChoiceAnswer{Choice: picked, Confidence: confidence, Probabilities: p}
		}
		score := func(value, confidence float64, p map[string]float64) onesie.Answer {
			return &onesie.ScoreAnswer{Score: value, Confidence: confidence, Probabilities: p}
		}
		even := map[string]float64{"billing": 0.6, "technical": 0.4}
		levels := map[string]float64{"0": 0.2, "1": 0.3, "2": 0.5}

		tests := []struct {
			name     string
			question plan.Question
			raw      onesie.Answer
			want     string
		}{
			{
				name: "should refuse a noul above 1", question: plan.Question{ID: "urgent", Shape: plan.Noul},
				raw: &onesie.NoulAnswer{Noul: 1.5}, want: "question 'urgent' answered 1.5, which lies outside [0,1]",
			},
			{
				name: "should refuse a negative noul", question: plan.Question{ID: "urgent", Shape: plan.Noul},
				raw: &onesie.NoulAnswer{Noul: -0.1}, want: "question 'urgent' answered -0.1, which lies outside [0,1]",
			},
			{
				name: "should refuse a choice that is not an option", question: team,
				raw:  choice("sales", 0.6, even),
				want: "question 'team' picked 'sales', which is not one of its options: billing, technical",
			},
			{
				name: "should refuse a missing option in p", question: team,
				raw:  choice("billing", 0.6, map[string]float64{"billing": 1}),
				want: "question 'team' has no p for 'technical'",
			},
			{
				name: "should refuse an extra key in p", question: team,
				raw:  choice("billing", 0.6, map[string]float64{"billing": 0.6, "technical": 0.4, "sales": 0}),
				want: "question 'team' has p for 'sales', which is not one of its options: billing, technical",
			},
			{
				name: "should refuse a negative p", question: team,
				raw:  choice("billing", 0.6, map[string]float64{"billing": 1.1, "technical": -0.1}),
				want: "question 'team' has p for 'billing' of 1.1, which lies outside [0,1]",
			},
			{
				name: "should refuse p that does not add up to 1", question: team,
				raw:  choice("billing", 0.6, map[string]float64{"billing": 0.6, "technical": 0.2}),
				want: "question 'team' has p that adds up to 0.8, not 1",
			},
			{
				name: "should refuse a confidence above 1", question: team,
				raw:  choice("billing", 1.2, even),
				want: "question 'team' has confidence 1.2, which lies outside [0,1]",
			},
			{
				name: "should refuse a score outside its levels", question: severity,
				raw:  score(3, 0.5, levels),
				want: "question 'severity' has score 3, which lies outside [0,2]",
			},
			{
				name: "should refuse p keyed by label rather than by index", question: severity,
				raw:  score(1, 0.5, map[string]float64{"low": 0.2, "mid": 0.3, "high": 0.5}),
				want: "question 'severity' has p for 'high', which is not a level index from 0 to 2",
			},
			{
				name: "should refuse a missing level in p", question: severity,
				raw:  score(1, 0.5, map[string]float64{"0": 0.5, "1": 0.5}),
				want: "question 'severity' has no p for level 2",
			},
			{
				name: "should refuse a score to a question with no levels", question: plan.Question{ID: "bare", Shape: plan.Rate},
				raw:  score(0, 1, map[string]float64{}),
				want: "question 'bare' has no levels to score against",
			},
			{
				name: "should refuse a score confidence below 0", question: severity,
				raw:  score(1, -0.5, levels),
				want: "question 'severity' has confidence -0.5, which lies outside [0,1]",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				_, err := answer.Normalize(tc.question, tc.raw)
				if err == nil || err.Error() != prefix+tc.want {
					t.Fatalf("error = %v, want %q", err, prefix+tc.want)
				}

				var unusable *answer.UnusableError
				if !errors.As(err, &unusable) {
					t.Errorf("error = %T, want *answer.UnusableError", err)
				}
			})
		}
	})

	t.Run("should accept p that two place rounding leaves just off 1", func(t *testing.T) {
		t.Parallel()

		thirds := plan.Question{
			ID: "team", Shape: plan.Pick, Options: []plan.Option{{Name: "a"}, {Name: "b"}, {Name: "c"}},
		}

		raw := &onesie.ChoiceAnswer{
			Choice: "a", Confidence: 0.1, Probabilities: map[string]float64{"a": 0.33, "b": 0.33, "c": 0.33},
		}

		if _, err := answer.Normalize(thirds, raw); err != nil {
			t.Errorf("Normalize = %v, want no error", err)
		}
	})

	t.Run("should reject an answer whose shape does not match the question", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			question plan.Question
			raw      onesie.Answer
			wantErr  string
		}{
			{
				name:     "should reject a nil answer rather than panicking",
				question: plan.Question{ID: "urgent", Shape: plan.Noul},
				raw:      nil,
			},
			{
				name:     "should reject a choice answer to a yes/no question",
				question: plan.Question{ID: "urgent", Shape: plan.Noul},
				raw:      &onesie.ChoiceAnswer{Choice: "x", Confidence: 0.5},
				wantErr:  "200 response could not be used: question 'urgent' expects a noul answer, got choice",
			},
			{
				name: "should reject a noul answer to a pick question",
				question: plan.Question{
					ID:      "team",
					Shape:   plan.Pick,
					Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
				},
				raw:     &onesie.NoulAnswer{Noul: 0.4},
				wantErr: "200 response could not be used: question 'team' expects a choice answer, got noul",
			},
			{
				name: "should reject a choice answer to a rate question",
				question: plan.Question{
					ID:     "severity",
					Shape:  plan.Rate,
					Levels: []plan.Level{{Label: "low"}, {Label: "high"}},
				},
				raw:     &onesie.ChoiceAnswer{Choice: "low", Confidence: 0.5},
				wantErr: "200 response could not be used: question 'severity' expects a score answer, got choice",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				_, err := answer.Normalize(tc.question, tc.raw)
				if err == nil {
					t.Fatal("expected an error, got none")
				}

				if tc.wantErr != "" && err.Error() != tc.wantErr {
					t.Errorf("error = %q, want %q", err.Error(), tc.wantErr)
				}

				// A shape the question did not ask for comes back on every retry, so it is a 200
				// onesie could not use and not a transport error a pipeline retries.
				var unusable *answer.UnusableError
				if !errors.As(err, &unusable) {
					t.Fatalf("error = %T, want *answer.UnusableError", err)
				}
			})
		}
	})

	t.Run("should flatten a box into its four edges and confidence", func(t *testing.T) {
		t.Parallel()

		got, err := answer.Normalize(plan.Question{ID: "mug", Shape: plan.Box}, &onesie.BoxAnswer{
			Edges:      onesie.Edges{Top: 0.25, Bottom: 0.82, Left: 0.44, Right: 0.84},
			Confidence: 0.92,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		encoded, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if want := `{"left":0.44,"top":0.25,"right":0.84,"bottom":0.82,"confidence":0.92}`; string(encoded) != want {
			t.Errorf("json = %s, want %s", encoded, want)
		}
	})

	for _, bad := range []struct {
		name  string
		edges onesie.Edges
	}{
		{name: "should refuse an edge above 1", edges: onesie.Edges{Top: 0, Bottom: 1.2, Left: 0, Right: 1}},
		{name: "should refuse a left edge right of the right edge", edges: onesie.Edges{Top: 0, Bottom: 1, Left: 0.6, Right: 0.4}},
		{name: "should refuse a top edge below the bottom edge", edges: onesie.Edges{Top: 0.9, Bottom: 0.1, Left: 0, Right: 1}},
	} {
		t.Run(bad.name, func(t *testing.T) {
			t.Parallel()

			_, err := answer.Normalize(plan.Question{ID: "mug", Shape: plan.Box}, &onesie.BoxAnswer{Edges: bad.edges, Confidence: 0.5})

			var unusable *answer.UnusableError
			if !errors.As(err, &unusable) {
				t.Fatalf("error = %v, want *answer.UnusableError", err)
			}
		})
	}
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

		got, err := answer.NormalizeAll(questions, result)
		if err != nil {
			t.Fatal(err)
		}

		if len(got) != 2 || got[0].Value != 0.9 || got[1].Value != 0.2 {
			t.Errorf("NormalizeAll = %+v, want a then b", got)
		}
	})

	t.Run("should refuse the first answer that does not fit", func(t *testing.T) {
		t.Parallel()

		result := &onesie.Result{Answers: map[string]onesie.Answer{"a": &onesie.NoulAnswer{Noul: 0.9}}}

		if _, err := answer.NormalizeAll(questions, result); err == nil || err.Error() !=
			"200 response could not be used: question 'b' has no answer" {
			t.Errorf("NormalizeAll = %v, want the missing answer to b", err)
		}
	})
}

func TestCheckAll(t *testing.T) {
	t.Parallel()

	questions := []plan.Question{{ID: "a", Shape: plan.Noul}}

	tests := []struct {
		name    string
		answers map[string]onesie.Answer
		wantErr bool
	}{
		{name: "should accept answers that fit", answers: map[string]onesie.Answer{"a": &onesie.NoulAnswer{Noul: 0.5}}},
		{name: "should refuse an answer outside [0,1]", answers: map[string]onesie.Answer{"a": &onesie.NoulAnswer{Noul: 2}}, wantErr: true},
		{name: "should refuse a missing answer", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := answer.CheckAll(questions, &onesie.Result{Answers: tc.answers})
			if (err != nil) != tc.wantErr {
				t.Errorf("CheckAll = %v, want an error %v", err, tc.wantErr)
			}
		})
	}
}

func TestUnusable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason string
		want   string
	}{
		{
			name: "should name a 200 onesie could not use", reason: "the response is not JSON",
			want: "200 response could not be used: the response is not JSON",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := answer.Unusable(tc.reason)

			var unusable *answer.UnusableError
			if !errors.As(err, &unusable) || err.Error() != tc.want {
				t.Errorf("Unusable = %v, want *answer.UnusableError %q", err, tc.want)
			}
		})
	}
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
