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
	three := plan.Question{
		ID: "t", Shape: plan.Pick, Options: []plan.Option{{Name: "billing"}, {Name: "platform"}, {Name: "sales"}},
	}

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
			name:       "should replay the probabilities of a pick",
			file:       `{"t":{"value":"platform","confidence":0.6,"p":{"billing":0.3,"platform":0.6,"sales":0.1}}}`,
			question:   three,
			value:      "platform",
			confidence: 0.6,
			p:          map[string]float64{"billing": 0.3, "platform": 0.6, "sales": 0.1},
		},
		{
			name:       "should replay the probabilities of a rate",
			file:       `{"r":{"value":"curt","p":{"calm":0.1,"curt":0.5,"rude":0.4}}}`,
			question:   rateQuestion("r", "calm", "curt", "rude"),
			value:      "curt",
			confidence: 1,
			score:      1,
			norm:       0.5,
			p:          map[string]float64{"calm": 0.1, "curt": 0.5, "rude": 0.4},
		},
		{
			name:       "should replay the probabilities of a rate from a request body by index",
			file:       `{"b":{"value":"2","p":{"0":0.1,"1":0.2,"2":0.7}}}`,
			question:   plan.Question{ID: "b", Shape: plan.Rate, Levels: []plan.Level{{}, {}, {}}},
			value:      "2",
			confidence: 1,
			score:      2,
			norm:       1,
			p:          map[string]float64{"0": 0.1, "1": 0.2, "2": 0.7},
		},
		{
			name:     "should refuse p that leaves an option out",
			file:     `{"t":{"value":"platform","p":{"billing":0.3,"platform":0.7}}}`,
			question: three,
			wantErr:  "onesie: --mock: question 't' has p with no entry for 'sales'",
		},
		{
			name:     "should refuse p with a key that is not an option",
			file:     `{"t":{"value":"platform","p":{"billing":0.1,"platform":0.7,"sales":0.1,"hr":0.1}}}`,
			question: three,
			wantErr:  "onesie: --mock: question 't' has p with the key 'hr', which is not one of billing, platform, sales",
		},
		{
			name:     "should refuse p with a value outside 0 to 1",
			file:     `{"t":{"value":"platform","p":{"billing":-0.1,"platform":0.7,"sales":0.1}}}`,
			question: three,
			wantErr:  "onesie: --mock: question 't' has p for 'billing' of -0.1, which lies outside [0,1]",
		},
		{
			name:     "should refuse p that is not an object",
			file:     `{"t":{"value":"platform","p":[1]}}`,
			question: three,
			wantErr:  "onesie: --mock: question 't' has p [1], which is not an object of probabilities",
		},
		{
			name:     "should refuse a pick whose p makes another option likelier",
			file:     `{"t":{"value":"platform","p":{"billing":0.5,"platform":0.4,"sales":0.1}}}`,
			question: three,
			wantErr:  "onesie: --mock: question 't' has p that makes 'billing' likelier than its value 'platform'",
		},
		{
			name:       "should accept a pick that ties with another option",
			file:       `{"t":{"value":"platform","p":{"billing":0.45,"platform":0.45,"sales":0.1}}}`,
			question:   three,
			value:      "platform",
			confidence: 1,
			p:          map[string]float64{"billing": 0.45, "platform": 0.45, "sales": 0.1},
		},
		{
			name:     "should refuse a rate whose modal level is not its value",
			file:     `{"r":{"value":"curt","p":{"calm":0.4,"curt":0.4,"rude":0.2}}}`,
			question: rateQuestion("r", "calm", "curt", "rude"),
			wantErr:  "onesie: --mock: question 'r' has p that makes 'calm' likelier than its value 'curt'",
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

			answers, err := Load(strings.NewReader(tc.file), []plan.Question{tc.question}, Options{})
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

		answers, err := Load(strings.NewReader(`{"u":0.3}`), []plan.Question{{ID: "u", Shape: plan.Noul}}, Options{})
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

	answers, err := Load(strings.NewReader(file), threeQuestions(), Options{ByID: true})
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
