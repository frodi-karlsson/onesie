package mock

import (
	"math"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestEntryResult(t *testing.T) {
	t.Parallel()

	five := rateQuestion("r", "1", "2", "3", "4", "5")

	tests := []struct {
		name       string
		file       string
		question   plan.Question
		value      any
		confidence float64
		score      float64
		norm       float64
		p          map[string]float64
		wantErr    string
	}{
		{
			name:       "should build a pick whose value is the name and whose confidence is as given",
			file:       `{"t":{"value":"platform","confidence":0.4}}`,
			question:   plan.Question{ID: "t", Shape: plan.Pick, Options: []plan.Option{{Name: "billing"}, {Name: "platform"}, {Name: "sales"}}},
			value:      "platform",
			confidence: 0.4,
			p:          map[string]float64{"platform": 0.4, "billing": 0.3, "sales": 0.3},
		},
		{
			name:       "should build a rate whose value is the level and whose norm is its index over the top",
			file:       `{"r":"4"}`,
			question:   five,
			value:      "4",
			confidence: 1,
			score:      3,
			norm:       0.75,
			p:          map[string]float64{"1": 0, "2": 0, "3": 0, "4": 1, "5": 0},
		},
		{
			name:       "should keep the chosen level at a low confidence",
			file:       `{"r":{"value":"2","confidence":0.1}}`,
			question:   five,
			value:      "2",
			confidence: 0.1,
			score:      1,
			norm:       0.25,
			p:          map[string]float64{"1": 0, "2": 1, "3": 0, "4": 0, "5": 0},
		},
		{
			name:       "should replay a score between levels as the real run printed it",
			file:       `{"r":{"value":"4","score":2.6,"norm":0.65,"confidence":0.8}}`,
			question:   five,
			value:      "4",
			confidence: 0.8,
			score:      2.6,
			norm:       0.65,
		},
		{
			name:     "should refuse a norm the score does not give",
			file:     `{"r":{"value":"4","score":2.6,"norm":0.5}}`,
			question: five,
			wantErr:  "onesie: --mock: question 'r' has norm 0.5, but score 2.6 on 5 levels gives 0.65",
		},
		{
			name:     "should refuse a score outside the levels",
			file:     `{"r":{"value":"4","score":7}}`,
			question: five,
			wantErr:  "onesie: --mock: question 'r' has score 7, which lies outside [0,4]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answers, err := Load(strings.NewReader(tc.file), []plan.Question{tc.question}, false)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error\n got: %v\nwant: %s", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			entry, _ := answers.Lookup(1, nil)

			result, err := entry.Result()
			if err != nil {
				t.Fatalf("Result: %v", err)
			}

			normalized, err := answer.Normalize(tc.question, result.Answers[tc.question.ID])
			if err != nil {
				t.Fatalf("Normalize: %v", err)
			}

			if normalized.Value != tc.value {
				t.Errorf("value %v, want %v", normalized.Value, tc.value)
			}

			if *normalized.Confidence != tc.confidence {
				t.Errorf("confidence %v, want %v", *normalized.Confidence, tc.confidence)
			}

			if tc.question.Shape == plan.Rate {
				if *normalized.Score != tc.score {
					t.Errorf("score %v, want %v", *normalized.Score, tc.score)
				}

				if math.Abs(*normalized.Norm-tc.norm) > 1e-9 {
					t.Errorf("norm %v, want %v", *normalized.Norm, tc.norm)
				}
			}

			for key, want := range tc.p {
				if got := normalized.P.Values[key]; math.Abs(got-want) > 1e-9 {
					t.Errorf("p[%s] = %v, want %v", key, got, want)
				}
			}
		})
	}

	t.Run("should name the model mock and bill nothing", func(t *testing.T) {
		t.Parallel()

		answers, err := Load(strings.NewReader(`{"u":0.3}`), []plan.Question{{ID: "u", Shape: plan.Noul}}, false)
		if err != nil {
			t.Fatal(err)
		}

		entry, _ := answers.Lookup(1, nil)

		result, err := entry.Result()
		if err != nil {
			t.Fatal(err)
		}

		if result.Model != "mock" {
			t.Errorf("model %q, want mock", result.Model)
		}

		if result.Usage != (jev.Usage{}) {
			t.Errorf("usage %+v, want zero", result.Usage)
		}
	})
}

func TestEntryKey(t *testing.T) {
	t.Parallel()

	file := `{"id":"a","u":0.3,"t":"billing","r":"curt"}` + "\n" +
		`{"id":"b","model":"x","u":{"value":0.3},"t":{"value":"billing","confidence":1},"r":"curt"}` + "\n" +
		`{"id":"c","u":0.4,"t":"billing","r":"curt"}` + "\n" +
		`{"id":"d","u":0.3,"t":{"value":"billing","confidence":0.5},"r":"curt"}` + "\n" +
		`{"id":"e","error":503}` + "\n" +
		`{"id":"f","error":{"kind":"http","status":503,"message":"503 busy"}}` + "\n" +
		`{"id":"g","error":401}` + "\n"

	answers, err := Load(strings.NewReader(file), threeQuestions(), true)
	if err != nil {
		t.Fatal(err)
	}

	key := func(id string) string {
		entry, found := answers.Lookup(0, id)
		if !found {
			t.Fatalf("no entry for %s", id)
		}

		return entry.Key()
	}

	t.Run("should give entries with the same answers the same key", func(t *testing.T) {
		t.Parallel()

		if key("a") != key("b") {
			t.Errorf("a and b differ: %s and %s", key("a"), key("b"))
		}

		if key("e") != key("f") {
			t.Errorf("e and f differ: %s and %s", key("e"), key("f"))
		}
	})

	t.Run("should give entries with different answers different keys", func(t *testing.T) {
		t.Parallel()

		keys := map[string]string{}
		for _, id := range []string{"a", "c", "d", "e", "g"} {
			if other, taken := keys[key(id)]; taken {
				t.Errorf("%s and %s share the key %s", id, other, key(id))
			}

			keys[key(id)] = id
		}
	})
}
