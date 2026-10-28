package mock

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/num"
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
			file:       `{"t":{"value":"platform","confidence":0.6}}`,
			question:   plan.Question{ID: "t", Shape: plan.Pick, Options: []plan.Option{{Name: "billing"}, {Name: "platform"}, {Name: "sales"}}},
			value:      "platform",
			confidence: 0.6,
			p:          map[string]float64{"platform": 0.6, "billing": 0.2, "sales": 0.2},
		},
		{
			name:       "should keep a low confidence pick at least twice as likely as any other option",
			file:       `{"t":{"value":"platform","confidence":0.2}}`,
			question:   three,
			value:      "platform",
			confidence: 0.2,
			p:          map[string]float64{"platform": 0.5, "billing": 0.25, "sales": 0.25},
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
			name:       "should spread a rate's p by its confidence as a pick's, and average its score over p",
			file:       `{"r":{"value":"2","confidence":0.6}}`,
			question:   five,
			value:      "2",
			confidence: 0.6,
			score:      1.5,
			norm:       0.375,
			p:          map[string]float64{"1": 0.1, "2": 0.6, "3": 0.1, "4": 0.1, "5": 0.1},
		},
		{
			name:       "should keep the chosen level at a low confidence",
			file:       `{"r":{"value":"2","confidence":0.1}}`,
			question:   five,
			value:      "2",
			confidence: 0.1,
			score:      1.8333,
			norm:       0.4583,
			p:          map[string]float64{"1": 0.1667, "2": 0.3334, "3": 0.1667, "4": 0.1667, "5": 0.1667},
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
			name:       "should replay the probabilities of a rate and average its score over them",
			file:       `{"r":{"value":"curt","p":{"calm":0.1,"curt":0.5,"rude":0.4}}}`,
			question:   rateQuestion("r", "calm", "curt", "rude"),
			value:      "curt",
			confidence: 1,
			score:      1.3,
			norm:       0.65,
			p:          map[string]float64{"calm": 0.1, "curt": 0.5, "rude": 0.4},
		},
		{
			name:       "should replay the probabilities of a rate from a request body by index",
			file:       `{"b":{"value":"2","p":{"0":0.1,"1":0.2,"2":0.7}}}`,
			question:   plan.Question{ID: "b", Shape: plan.Rate, Levels: []plan.Level{{}, {}, {}}},
			value:      "2",
			confidence: 1,
			score:      1.6,
			norm:       0.8,
			p:          map[string]float64{"0": 0.1, "1": 0.2, "2": 0.7},
		},
		{
			name:     "should refuse p that leaves an option out",
			file:     `{"t":{"value":"platform","p":{"billing":0.3,"platform":0.7}}}`,
			question: three,
			wantErr:  "--mock: question 't' has p with no entry for 'sales'",
		},
		{
			name:     "should refuse p with a key that is not an option",
			file:     `{"t":{"value":"platform","p":{"billing":0.1,"platform":0.7,"sales":0.1,"hr":0.1}}}`,
			question: three,
			wantErr:  "--mock: question 't' has p with the key 'hr', which is not one of billing, platform, sales",
		},
		{
			name:     "should refuse p with a value outside 0 to 1",
			file:     `{"t":{"value":"platform","p":{"billing":-0.1,"platform":0.7,"sales":0.1}}}`,
			question: three,
			wantErr:  "--mock: question 't' has p for 'billing' of -0.1, which lies outside [0,1]",
		},
		{
			name:     "should refuse p that does not add up to 1",
			file:     `{"t":{"value":"platform","p":{"billing":0.1,"platform":0.5,"sales":0.1}}}`,
			question: three,
			wantErr:  "--mock: question 't' has p that adds up to 0.7, not 1",
		},
		{
			name:     "should refuse p that does not add up to 1 beside a confidence of 1",
			file:     `{"t":{"value":"platform","confidence":1,"p":{"billing":0.1,"platform":0.5,"sales":0.1}}}`,
			question: three,
			wantErr:  "--mock: question 't' has p that adds up to 0.7, not 1",
		},
		{
			name:     "should refuse p that does not add up to 1 beside a confidence below 1",
			file:     `{"t":{"value":"platform","confidence":0.6,"p":{"billing":0.1,"platform":0.5,"sales":0.1}}}`,
			question: three,
			wantErr:  "--mock: question 't' has p that adds up to 0.7, not 1",
		},
		{
			name:     "should refuse p that is not an object",
			file:     `{"t":{"value":"platform","p":[1]}}`,
			question: three,
			wantErr:  "--mock: question 't' has p [1], which is not an object of probabilities",
		},
		{
			name:     "should refuse a pick whose p makes another option likelier",
			file:     `{"t":{"value":"platform","p":{"billing":0.5,"platform":0.4,"sales":0.1}}}`,
			question: three,
			wantErr:  "--mock: question 't' has p that makes 'billing' likelier than its value 'platform'",
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
			wantErr:  "--mock: question 'r' has p that makes 'calm' likelier than its value 'curt'",
		},
		{
			name:     "should refuse a norm the score does not give",
			file:     `{"r":{"value":"4","score":2.6,"norm":0.5}}`,
			question: five,
			wantErr:  "--mock: question 'r' has norm 0.5, but score 2.6 on 5 levels gives 0.65",
		},
		{
			name:     "should refuse a score outside the levels",
			file:     `{"r":{"value":"4","score":7}}`,
			question: five,
			wantErr:  "--mock: question 'r' has score 7, which lies outside [0,4]",
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

	t.Run("should synthesize a p that loads back for every value and confidence", func(t *testing.T) {
		t.Parallel()

		for levels := 1; levels <= 6; levels++ {
			names := make([]string, 0, levels)
			for i := range levels {
				names = append(names, "l"+strconv.Itoa(i))
			}

			questions := []plan.Question{rateQuestion("q", names...)}
			options := make([]plan.Option, 0, levels)
			for _, name := range names {
				options = append(options, plan.Option{Name: name})
			}

			questions = append(questions, plan.Question{ID: "q", Shape: plan.Pick, Options: options})

			for _, question := range questions {
				for _, value := range names {
					for step := 0; step <= 20; step++ {
						assertRoundTrip(t, question, value, float64(step)/20)
					}

					for _, confidence := range []float64{0.81014, 0.12345, 0.33333333} {
						assertRoundTrip(t, question, value, confidence)
					}
				}
			}
		}
	})

	t.Run("should load a norm written exactly by an older onesie and one rounded to four places", func(t *testing.T) {
		t.Parallel()

		four := rateQuestion("q", "a", "b", "c", "d")

		for _, norm := range []string{"0.3333333333333333", "0.3333"} {
			file := `{"q":{"value":"b","score":1,"norm":` + norm + `}}`
			if _, err := Load(strings.NewReader(file), []plan.Question{four}, Options{}); err != nil {
				t.Errorf("Load(%s): %v", file, err)
			}
		}

		file := `{"q":{"value":"b","score":1,"norm":0.334}}`
		if _, err := Load(strings.NewReader(file), []plan.Question{four}, Options{}); err == nil {
			t.Errorf("Load(%s) = nil, want a norm the score does not give refused", file)
		}
	})

	t.Run("should name no model and bill nothing", func(t *testing.T) {
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

		if result.Model != "" {
			t.Errorf("model %q, want none, since the run fills it", result.Model)
		}

		if result.Usage != (onesie.Usage{}) {
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

func TestBoxAnswer(t *testing.T) {
	t.Parallel()

	n := func(text string) json.Number { return json.Number(text) }
	edges := func(left, top, right, bottom string) map[string]any {
		return map[string]any{"left": n(left), "top": n(top), "right": n(right), "bottom": n(bottom)}
	}

	tests := []struct {
		name           string
		given          any
		wantConfidence float64
		wantErr        string
	}{
		{name: "should read a box answer", given: map[string]any{"left": n("0.44"), "top": n("0.25"), "right": n("0.84"), "bottom": n("0.82"), "confidence": n("0.92")}, wantConfidence: 0.92},
		{name: "should default a box confidence to 1", given: edges("0.1", "0.1", "0.2", "0.2"), wantConfidence: 1},
		{name: "should refuse a bare number for a box", given: n("0.5"), wantErr: "which is not a box"},
		{name: "should refuse a box missing an edge", given: map[string]any{"left": n("0.1"), "top": n("0.1"), "right": n("0.2")}, wantErr: "has no 'bottom'"},
		{name: "should refuse a box with value", given: map[string]any{"value": n("1"), "left": n("0.1"), "top": n("0.1"), "right": n("0.2"), "bottom": n("0.2")}, wantErr: "carries the key 'value'"},
		{name: "should refuse a box whose left is right of its right", given: edges("0.5", "0.1", "0.2", "0.2"), wantErr: "left below right"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := boxAnswer(tc.given)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			box, ok := got.(*onesie.BoxAnswer)
			if !ok || box.Confidence != tc.wantConfidence {
				t.Errorf("answer = %+v, want a box with confidence %v", got, tc.wantConfidence)
			}
		})
	}
}

func assertRoundTrip(t *testing.T, question plan.Question, value string, confidence float64) {
	t.Helper()

	file := fmt.Sprintf(`{"q":{"value":%q,"confidence":%v}}`, value, confidence)

	first := loadNormalized(t, question, file)

	levels := len(question.Options) + len(question.Levels)
	if want := math.Ceil(max(confidence, 2/float64(levels+1))*1e4-1e-6) / 1e4; first.P.Values[value] != want {
		t.Errorf("%s: the value's p is %v, want %v", file, first.P.Values[value], want)
	}

	if first.P.Values[value] < confidence {
		t.Errorf("%s: the value's p %v is below its confidence", file, first.P.Values[value])
	}

	rest := num.Rounded((1 - first.P.Values[value]) / float64(levels-1))

	for key, share := range first.P.Values {
		if key != value && share != rest {
			t.Errorf("%s: p[%s] = %v, want an even share of the rest, %v", file, key, share, rest)
		}

		if key != value && share >= first.P.Values[value] {
			t.Errorf("%s: p[%s] = %v, want it below the value's %v", file, key, share, first.P.Values[value])
		}
	}

	if sum, fits := answer.SumFits(slices.Collect(maps.Values(first.P.Values))); !fits {
		t.Errorf("%s: p sums to %v, want 1 within the sum check", file, sum)
	}

	printed, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}

	replayed := loadNormalized(t, question, fmt.Sprintf(`{"q":%s}`, printed))

	again, err := json.Marshal(replayed)
	if err != nil {
		t.Fatal(err)
	}

	if string(again) != string(printed) {
		t.Errorf("%s printed %s, which replays as %s", file, printed, again)
	}
}

func loadNormalized(t *testing.T, question plan.Question, file string) *answer.Answer {
	t.Helper()

	answers, err := Load(strings.NewReader(file), []plan.Question{question}, Options{})
	if err != nil {
		t.Fatalf("Load(%s): %v", file, err)
	}

	entry, _ := answers.Lookup(1, nil)

	result, err := entry.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}

	normalized, err := answer.Normalize(question, result.Answers[question.ID])
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}

	return normalized
}
