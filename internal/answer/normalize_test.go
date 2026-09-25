package answer_test

import (
	"encoding/json"
	"errors"
	"maps"
	"math"
	"net/http"
	"sort"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		question plan.Question
		raw      jev.Answer
		want     string
	}{
		{
			name:     "should report a yes/no answer as a bare value",
			question: plan.Question{ID: "urgent", Shape: plan.Noul},
			raw:      &jev.NoulAnswer{Noul: 0.92},
			want:     `{"value":0.92}`,
		},
		{
			name:     "should keep a zero probability rather than omitting it",
			question: plan.Question{ID: "urgent", Shape: plan.Noul},
			raw:      &jev.NoulAnswer{Noul: 0},
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
			raw: &jev.ChoiceAnswer{
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
			raw: &jev.ScoreAnswer{
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
			raw: &jev.ScoreAnswer{
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
			raw: &jev.ScoreAnswer{
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
			raw: &jev.ScoreAnswer{
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

	// The record carries a number under every key the question asked about, so a reader of the map
	// answers the same as the printed record.
	t.Run("should fill a key the answer left out with a zero", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			question plan.Question
			raw      jev.Answer
			want     map[string]float64
			wantJSON string
		}{
			{
				name: "should fill an option the answer left out with a zero",
				question: plan.Question{
					ID:    "team",
					Shape: plan.Pick,
					Options: []plan.Option{
						{Name: "billing"}, {Name: "technical"}, {Name: "human"},
					},
				},
				raw: &jev.ChoiceAnswer{
					Choice:        "billing",
					Confidence:    0.9,
					Probabilities: map[string]float64{"billing": 0.7, "technical": 0.3},
				},
				want: map[string]float64{"billing": 0.7, "technical": 0.3, "human": 0},
				wantJSON: `{"value":"billing","confidence":0.9,` +
					`"p":{"billing":0.7,"technical":0.3,"human":0}}`,
			},
			{
				name: "should fill a label the answer left out with a zero",
				question: plan.Question{
					ID:       "severity",
					Shape:    plan.Rate,
					Labelled: true,
					Levels:   []plan.Level{{Label: "low"}, {Label: "high"}},
				},
				raw: &jev.ScoreAnswer{
					Score:         1,
					Confidence:    0.5,
					Probabilities: map[string]float64{"1": 1},
				},
				want:     map[string]float64{"low": 0, "high": 1},
				wantJSON: `{"value":"high","score":1,"norm":1,"confidence":0.5,"p":{"low":0,"high":1}}`,
			},
			{
				name: "should fill an index the answer left out with a zero",
				question: plan.Question{
					ID:     "indexed",
					Shape:  plan.Rate,
					Levels: []plan.Level{{Desc: "calm"}, {Desc: "cross"}, {Desc: "angry"}},
				},
				raw: &jev.ScoreAnswer{
					Score:         1,
					Confidence:    0.5,
					Probabilities: map[string]float64{"1": 1},
				},
				want: map[string]float64{"0": 0, "1": 1, "2": 0},
				wantJSON: `{"value":"1","score":1,"norm":0.5,"confidence":0.5,` +
					`"p":{"0":0,"1":1,"2":0}}`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				got, err := answer.Normalize(tc.question, tc.raw)
				if err != nil {
					t.Fatalf("Normalize(%q) error = %v, want no error", tc.question.ID, err)
				}

				if got.P == nil {
					t.Fatalf("Normalize(%q) carries no probabilities, want some", tc.question.ID)
				}

				if !maps.Equal(got.P.Values, tc.want) {
					t.Errorf("values = %v, want %v", got.P.Values, tc.want)
				}

				encoded, err := json.Marshal(got)
				if err != nil {
					t.Fatalf("marshalling: %v", err)
				}

				if string(encoded) != tc.wantJSON {
					t.Errorf("got  %s\nwant %s", encoded, tc.wantJSON)
				}
			})
		}
	})

	t.Run("should reject an answer whose shape does not match the question", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			question plan.Question
			raw      jev.Answer
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
				raw:      &jev.ChoiceAnswer{Choice: "x", Confidence: 0.5},
				wantErr:  "onesie: question 'urgent' expects a noul answer, got choice",
			},
			{
				name: "should reject a noul answer to a pick question",
				question: plan.Question{
					ID:      "team",
					Shape:   plan.Pick,
					Options: []plan.Option{{Name: "billing"}, {Name: "technical"}},
				},
				raw:     &jev.NoulAnswer{Noul: 0.4},
				wantErr: "onesie: question 'team' expects a choice answer, got noul",
			},
			{
				name: "should reject a choice answer to a rate question",
				question: plan.Question{
					ID:     "severity",
					Shape:  plan.Rate,
					Levels: []plan.Level{{Label: "low"}, {Label: "high"}},
				},
				raw:     &jev.ChoiceAnswer{Choice: "low", Confidence: 0.5},
				wantErr: "onesie: question 'severity' expects a score answer, got choice",
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

				// A shape the question did not ask for is deterministic, so it is a 200 whose body
				// onesie could not use. Left untyped it took the transport kind and exit 5, which tells
				// a pipeline to retry something that will never change.
				var unusable *jev.ResponseError
				if !errors.As(err, &unusable) {
					t.Fatalf("error = %T, want *jev.ResponseError", err)
				}

				if unusable.Status != http.StatusOK {
					t.Errorf("status = %d, want %d", unusable.Status, http.StatusOK)
				}
			})
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
