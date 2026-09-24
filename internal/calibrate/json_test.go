package calibrate_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/jev"
)

func TestWriteJSON(t *testing.T) {
	t.Parallel()

	full := calibrate.Report{
		Models:     []string{"model-a", "model-b"},
		Records:    14,
		Labelled:   12,
		Unlabelled: 2,
		Asked:      9,
		Stored:     3,
		Failed:     3,
		Questions: []calibrate.QuestionReport{
			urgentReport([]float64{0.5, 0.7}),
			teamReport(),
			starsReport(),
		},
	}

	t.Run("should hold the counts and then the questions in plan order", func(t *testing.T) {
		t.Parallel()

		doc := writeJSON(t, full)

		want := []string{"models", "records", "labelled", "unlabelled", "asked", "stored", "failed", "questions"}
		if got := keysOf(t, doc); !slices.Equal(got, want) {
			t.Errorf("keys = %v, want %v", got, want)
		}

		var decoded struct {
			Models                                               []string
			Records, Labelled, Unlabelled, Asked, Stored, Failed int
			Questions                                            []struct{ ID, Shape string }
		}
		decode(t, doc, &decoded)

		if !slices.Equal(decoded.Models, full.Models) || decoded.Records != 14 || decoded.Labelled != 12 ||
			decoded.Unlabelled != 2 || decoded.Asked != 9 || decoded.Stored != 3 || decoded.Failed != 3 {
			t.Errorf("counts = %+v", decoded)
		}

		var shapes []string
		for _, q := range decoded.Questions {
			shapes = append(shapes, q.ID+" "+q.Shape)
		}

		if want := []string{"urgent yes/no", "team pick", "stars rate"}; !slices.Equal(shapes, want) {
			t.Errorf("questions = %v, want %v", shapes, want)
		}
	})

	t.Run("should give each shape its fields in order", func(t *testing.T) {
		t.Parallel()

		questions := questionsOf(t, writeJSON(t, full))
		want := [][]string{
			{"id", "shape", "labelled", "yes", "no", "failed", "auc", "cuts", "values", "misses"},
			{"id", "shape", "labelled", "failed", "agreement", "options", "cuts", "grid", "misses"},
			{"id", "shape", "labelled", "failed", "agreement", "within_one", "mean_distance", "levels", "cuts", "grid", "misses"},
		}

		for i, q := range questions {
			if got := keysOf(t, q); !slices.Equal(got, want[i]) {
				t.Errorf("question %d keys = %v, want %v", i, got, want[i])
			}
		}
	})

	t.Run("should give a yes/no row at every distinct value answered and every miss", func(t *testing.T) {
		t.Parallel()

		var q struct {
			Values []struct{ Cut float64 }
			Misses []struct {
				ID    string
				Label string
				Value float64
			}
		}
		decode(t, questionsOf(t, writeJSON(t, full))[0], &q)

		var cuts []float64
		for _, row := range q.Values {
			cuts = append(cuts, row.Cut)
		}

		if want := []float64{0.1, 0.2, 0.3, 0.6, 0.8, 0.9}; !slices.Equal(cuts, want) {
			t.Errorf("values = %v, want %v", cuts, want)
		}

		if len(q.Misses) != 2 || q.Misses[0].ID != "T-3" || q.Misses[0].Label != "yes" || q.Misses[0].Value != 0.3 {
			t.Errorf("misses = %+v, want T-3 and T-5", q.Misses)
		}
	})

	t.Run("should list every yes/no miss rather than five", func(t *testing.T) {
		t.Parallel()

		report := calibrate.Report{Questions: []calibrate.QuestionReport{yesNoReport("urgent", calibrate.ScoreYesNo(
			namedYesNo("yes", 0.2, "yes", 0.2, "yes", 0.2, "yes", 0.2, "no", 0.8, "no", 0.8, "no", 0.8, "yes", 0.9),
			0, []float64{0.5},
		))}}

		var q struct{ Misses []struct{ ID string } }
		decode(t, questionsOf(t, writeJSON(t, report))[0], &q)

		if len(q.Misses) != 7 {
			t.Errorf("misses = %+v, want all seven", q.Misses)
		}
	})

	t.Run("should encode a cut row with its counts and shares", func(t *testing.T) {
		t.Parallel()

		var q struct{ Cuts []map[string]json.RawMessage }
		decode(t, questionsOf(t, writeJSON(t, full))[0], &q)

		row := q.Cuts[0]
		if string(row["cut"]) != "0.5" || string(row["flagged"]) != "3" {
			t.Errorf("row = %v", row)
		}

		for key, share := range map[string]calibrate.Share{
			"catches":            calibrate.Wilson(2, 3),
			"false_alarms":       calibrate.Wilson(1, 3),
			"right_when_flagged": calibrate.Wilson(2, 3),
		} {
			want, _ := json.Marshal(share)
			if string(row[key]) != string(want) {
				t.Errorf("%s = %s, want %s", key, row[key], want)
			}
		}
	})

	t.Run("should give pick and rate the whole grid and every miss", func(t *testing.T) {
		t.Parallel()

		questions := questionsOf(t, writeJSON(t, full))

		var pick struct {
			Options []struct {
				Name             string
				Labelled, Picked int
			}
			Cuts []struct {
				Cut                 float64
				Answered, Agreement struct{ Hits, Of int }
			}
			Grid   [][]int
			Misses []struct {
				ID, Label, Picked string
				Confidence        float64
			}
		}
		decode(t, questions[1], &pick)

		if len(pick.Options) != 3 || pick.Options[0].Name != "billing" || pick.Options[0].Labelled != 3 || pick.Options[0].Picked != 3 {
			t.Errorf("options = %+v", pick.Options)
		}

		if len(pick.Cuts) != 2 || pick.Cuts[1].Cut != 0.8 || pick.Cuts[1].Answered.Hits != 3 || pick.Cuts[1].Agreement.Of != 3 {
			t.Errorf("cuts = %+v", pick.Cuts)
		}

		if want := [][]int{{2, 1, 0}, {1, 1, 0}, {0, 0, 1}}; !slices.EqualFunc(pick.Grid, want, slices.Equal) {
			t.Errorf("grid = %v, want %v", pick.Grid, want)
		}

		if len(pick.Misses) != 2 || pick.Misses[0].ID != "T-3" || pick.Misses[0].Picked != "shipping" || pick.Misses[0].Confidence != 0.95 {
			t.Errorf("misses = %+v", pick.Misses)
		}

		var rate struct {
			WithinOne    struct{ Hits, Of int } `json:"within_one"`
			MeanDistance float64                `json:"mean_distance"`
			Levels       []struct{ Name string }
			Grid         [][]int
		}
		decode(t, questions[2], &rate)

		if rate.WithinOne.Hits != 3 || rate.WithinOne.Of != 4 || rate.MeanDistance != 0.75 || len(rate.Levels) != 3 || rate.Levels[2].Name != "high" {
			t.Errorf("rate = %+v", rate)
		}

		if want := [][]int{{1, 0, 0}, {0, 1, 1}, {1, 0, 0}}; !slices.EqualFunc(rate.Grid, want, slices.Equal) {
			t.Errorf("grid = %v, want %v", rate.Grid, want)
		}
	})

	t.Run("should add the other column only when an answer falls outside the options", func(t *testing.T) {
		t.Parallel()

		report := calibrate.Report{Questions: []calibrate.QuestionReport{pickReport("team", calibrate.ScorePick(
			namedChoice("billing", "billing", 0.9, "billing", "refunds", 0.8),
			[]string{"billing", "shipping"}, 0, []float64{0.5},
		))}}
		q := questionsOf(t, writeJSON(t, report))[0]

		want := []string{"id", "shape", "labelled", "failed", "agreement", "options", "cuts", "grid", "other", "misses"}
		if got := keysOf(t, q); !slices.Equal(got, want) {
			t.Errorf("keys = %v, want %v", got, want)
		}

		var decoded struct{ Other []int }
		decode(t, q, &decoded)

		if !slices.Equal(decoded.Other, []int{1, 0}) {
			t.Errorf("other = %v, want [1 0]", decoded.Other)
		}
	})

	t.Run("should give null for an undefined AUC and mean distance", func(t *testing.T) {
		t.Parallel()

		report := calibrate.Report{Questions: []calibrate.QuestionReport{
			failedReport("urgent"),
			rateReport("stars", calibrate.ScoreRate(nil, []string{"low", "high"}, 2, []float64{0.5})),
		}}
		questions := questionsOf(t, writeJSON(t, report))

		var yesNo, rate map[string]json.RawMessage
		decode(t, questions[0], &yesNo)
		decode(t, questions[1], &rate)

		if string(yesNo["auc"]) != "null" || string(rate["mean_distance"]) != "null" {
			t.Errorf("auc = %s, mean distance = %s, want null for both", yesNo["auc"], rate["mean_distance"])
		}
	})

	t.Run("should name a miss by its id under --id and by its line otherwise", func(t *testing.T) {
		t.Parallel()

		report := calibrate.Report{Questions: []calibrate.QuestionReport{
			yesNoReport("urgent", calibrate.ScoreYesNo([]calibrate.YesNoCase{
				{Name: "7", ID: 7.0, Line: 2, Yes: true, Value: 0.1},
				{Line: 12, Yes: false, Value: 0.8},
			}, 0, []float64{0.5})),
			pickReport("team", calibrate.ScorePick([]calibrate.ChoiceCase{
				{Line: 4, Label: "a", Picked: "b", Confidence: 0.9},
			}, []string{"a", "b"}, 0, []float64{0.5})),
		}}
		questions := questionsOf(t, writeJSON(t, report))

		var yesNo, pick struct{ Misses []map[string]json.RawMessage }
		decode(t, questions[0], &yesNo)
		decode(t, questions[1], &pick)

		misses := slices.Concat(yesNo.Misses, pick.Misses)
		want := [][]string{{"id", "7"}, {"line", "12"}, {"line", "4"}}

		for i, miss := range misses {
			_, hasID := miss["id"]
			_, hasLine := miss["line"]

			if hasID == hasLine || string(miss[want[i][0]]) != want[i][1] {
				t.Errorf("miss %d = %v, want only %s %s", i, miss, want[i][0], want[i][1])
			}
		}
	})

	t.Run("should add usage only when given", func(t *testing.T) {
		t.Parallel()

		cost := 0.25
		with := full
		with.Usage = &jev.Usage{InputTokens: 10, OutputTokens: 4, Cost: &cost}

		want := []string{"models", "records", "labelled", "unlabelled", "asked", "stored", "failed", "usage", "questions"}
		if got := keysOf(t, writeJSON(t, with)); !slices.Equal(got, want) {
			t.Errorf("keys = %v, want %v", got, want)
		}

		var decoded struct{ Usage jev.Usage }
		decode(t, writeJSON(t, with), &decoded)

		if decoded.Usage.InputTokens != 10 || decoded.Usage.OutputTokens != 4 || decoded.Usage.Cost == nil || *decoded.Usage.Cost != 0.25 {
			t.Errorf("usage = %+v", decoded.Usage)
		}
	})

	t.Run("should give an empty models list rather than null", func(t *testing.T) {
		t.Parallel()

		var decoded map[string]json.RawMessage
		decode(t, writeJSON(t, calibrate.Report{}), &decoded)

		if string(decoded["models"]) != "[]" || string(decoded["questions"]) != "[]" {
			t.Errorf("models = %s, questions = %s, want [] for both", decoded["models"], decoded["questions"])
		}
	})

	t.Run("should write exactly one line with a trailing newline", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer
		if err := calibrate.WriteJSON(&out, full); err != nil {
			t.Fatalf("WriteJSON: %v", err)
		}

		text := out.String()
		if strings.Count(text, "\n") != 1 || !strings.HasSuffix(text, "\n") {
			t.Errorf("output is not one line: %q", text)
		}
	})
}

func TestShareMarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("should encode a share with its counts, rate and interval", func(t *testing.T) {
		t.Parallel()

		share := calibrate.Wilson(45, 47)

		got, err := json.Marshal(share)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		var decoded map[string]float64
		decode(t, got, &decoded)

		if want := []string{"hits", "of", "rate", "low", "high"}; !slices.Equal(keysOf(t, got), want) {
			t.Errorf("keys = %v, want %v", keysOf(t, got), want)
		}

		if decoded["hits"] != 45 || decoded["of"] != 47 || decoded["rate"] != share.Rate ||
			decoded["low"] != share.Low || decoded["high"] != share.High {
			t.Errorf("share = %s, want %+v", got, share)
		}
	})

	t.Run("should encode an undefined share with null values", func(t *testing.T) {
		t.Parallel()

		got, err := json.Marshal(calibrate.Wilson(0, 0))
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}

		if want := `{"hits":0,"of":0,"rate":null,"low":null,"high":null}`; string(got) != want {
			t.Errorf("share = %s, want %s", got, want)
		}
	})
}

func writeJSON(t *testing.T, r calibrate.Report) []byte {
	t.Helper()

	var out bytes.Buffer
	if err := calibrate.WriteJSON(&out, r); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}

	return out.Bytes()
}

func questionsOf(t *testing.T, doc []byte) []json.RawMessage {
	t.Helper()

	var decoded struct{ Questions []json.RawMessage }
	decode(t, doc, &decoded)

	return decoded.Questions
}

func decode(t *testing.T, doc []byte, into any) {
	t.Helper()

	if err := json.Unmarshal(doc, into); err != nil {
		t.Fatalf("Unmarshal %s: %v", doc, err)
	}
}

func keysOf(t *testing.T, object []byte) []string {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(object))
	if _, err := dec.Token(); err != nil {
		t.Fatalf("Token: %v", err)
	}

	var keys []string

	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			t.Fatalf("Token: %v", err)
		}

		keys = append(keys, key.(string))

		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	}

	return keys
}
