package calibrate

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// WriteJSON renders the report as one json object on one line. It holds every row the table shows,
// plus a yes/no row at every distinct value, the whole grid and every miss.
func WriteJSON(w io.Writer, r Report) error {
	doc := jsonReport{
		Models:     r.Models,
		Records:    r.Records,
		Labelled:   r.Labelled,
		Unlabelled: r.Unlabelled,
		Asked:      r.Asked,
		Stored:     r.Stored,
		Failed:     r.Failed,
		Usage:      r.Usage,
		Questions:  make([]any, 0, len(r.Questions)),
	}
	if doc.Models == nil {
		doc.Models = []string{}
	}

	for _, q := range r.Questions {
		question, err := jsonQuestion(q)
		if err != nil {
			return err
		}

		doc.Questions = append(doc.Questions, question)
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	return enc.Encode(doc)
}

type jsonReport struct {
	Models     []string   `json:"models"`
	Records    int        `json:"records"`
	Labelled   int        `json:"labelled"`
	Unlabelled int        `json:"unlabelled"`
	Asked      int        `json:"asked"`
	Stored     int        `json:"stored"`
	Failed     int        `json:"failed"`
	Usage      *jev.Usage `json:"usage,omitempty"`
	Questions  []any      `json:"questions"`
}

func jsonQuestion(q QuestionReport) (any, error) {
	switch {
	case q.Shape == plan.Noul && q.YesNo != nil:
		return yesNoJSON(q.ID, *q.YesNo), nil
	case q.Shape == plan.Pick && q.Pick != nil:
		s := q.Pick

		return jsonPick{
			ID: q.ID, Shape: shapeName(q.Shape), Labelled: s.Labelled, Failed: s.Failed, Agreement: s.Agreement,
			Options: pickRowsJSON(s.Names), Cuts: confidenceJSON(s.Confidence), Grid: s.Grid, Other: s.Other,
			Misses: choiceMissesJSON(s.Misses),
		}, nil
	case q.Shape == plan.Rate && q.Rate != nil:
		s := q.Rate

		return jsonRate{
			ID: q.ID, Shape: shapeName(q.Shape), Labelled: s.Labelled, Failed: s.Failed, Agreement: s.Agreement,
			WithinOne: s.WithinOne, MeanDistance: definedOrNull(s.MeanDistance, s.HasMeanDistance),
			Levels: pickRowsJSON(s.Names), Cuts: confidenceJSON(s.Confidence), Grid: s.Grid, Other: s.Other,
			Misses: choiceMissesJSON(s.Misses),
		}, nil
	default:
		return nil, fmt.Errorf("calibrate: question '%s' has no %s score to report", q.ID, shapeName(q.Shape))
	}
}

func yesNoJSON(id string, s YesNoScore) jsonYesNo {
	misses := make([]jsonYesNoMiss, 0, len(s.Misses))
	for _, c := range s.Misses {
		misses = append(misses, jsonYesNoMiss{jsonRecord: recordJSON(c.ID, c.Line), Label: yesNoText(c.Yes), Value: c.Value})
	}

	return jsonYesNo{
		ID: id, Shape: shapeName(plan.Noul), Labelled: s.Labelled, Yes: s.Yes, No: s.No, Failed: s.Failed,
		AUC: definedOrNull(s.AUC, s.HasAUC), Cuts: cutRowsJSON(s.Cuts), Values: cutRowsJSON(s.Values), Misses: misses,
	}
}

type jsonYesNo struct {
	ID       string          `json:"id"`
	Shape    string          `json:"shape"`
	Labelled int             `json:"labelled"`
	Yes      int             `json:"yes"`
	No       int             `json:"no"`
	Failed   int             `json:"failed"`
	AUC      *float64        `json:"auc"`
	Cuts     []jsonCutRow    `json:"cuts"`
	Values   []jsonCutRow    `json:"values"`
	Misses   []jsonYesNoMiss `json:"misses"`
}

type jsonYesNoMiss struct {
	jsonRecord
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

func cutRowsJSON(rows []CutRow) []jsonCutRow {
	out := make([]jsonCutRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, jsonCutRow(row))
	}

	return out
}

type jsonCutRow struct {
	Cut              float64 `json:"cut"`
	Flagged          int     `json:"flagged"`
	Catches          Share   `json:"catches"`
	FalseAlarms      Share   `json:"false_alarms"`
	RightWhenFlagged Share   `json:"right_when_flagged"`
}

type jsonPick struct {
	ID        string              `json:"id"`
	Shape     string              `json:"shape"`
	Labelled  int                 `json:"labelled"`
	Failed    int                 `json:"failed"`
	Agreement Share               `json:"agreement"`
	Options   []jsonPickRow       `json:"options"`
	Cuts      []jsonConfidenceRow `json:"cuts"`
	Grid      [][]int             `json:"grid"`
	Other     []int               `json:"other,omitempty"`
	Misses    []jsonChoiceMiss    `json:"misses"`
}

type jsonRate struct {
	ID           string              `json:"id"`
	Shape        string              `json:"shape"`
	Labelled     int                 `json:"labelled"`
	Failed       int                 `json:"failed"`
	Agreement    Share               `json:"agreement"`
	WithinOne    Share               `json:"within_one"`
	MeanDistance *float64            `json:"mean_distance"`
	Levels       []jsonPickRow       `json:"levels"`
	Cuts         []jsonConfidenceRow `json:"cuts"`
	Grid         [][]int             `json:"grid"`
	Other        []int               `json:"other,omitempty"`
	Misses       []jsonChoiceMiss    `json:"misses"`
}

func pickRowsJSON(rows []PickRow) []jsonPickRow {
	out := make([]jsonPickRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, jsonPickRow(row))
	}

	return out
}

type jsonPickRow struct {
	Name            string `json:"name"`
	Labelled        int    `json:"labelled"`
	Picked          int    `json:"picked"`
	Found           Share  `json:"found"`
	RightWhenPicked Share  `json:"right_when_picked"`
}

func confidenceJSON(rows []ConfidenceRow) []jsonConfidenceRow {
	out := make([]jsonConfidenceRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, jsonConfidenceRow(row))
	}

	return out
}

type jsonConfidenceRow struct {
	Cut       float64 `json:"cut"`
	Answered  Share   `json:"answered"`
	Agreement Share   `json:"agreement"`
}

func choiceMissesJSON(cases []ChoiceCase) []jsonChoiceMiss {
	out := make([]jsonChoiceMiss, 0, len(cases))
	for _, c := range cases {
		out = append(out, jsonChoiceMiss{
			jsonRecord: recordJSON(c.ID, c.Line), Label: c.Label, Picked: c.Picked, Confidence: c.Confidence,
		})
	}

	return out
}

type jsonChoiceMiss struct {
	jsonRecord
	Label      string  `json:"label"`
	Picked     string  `json:"picked"`
	Confidence float64 `json:"confidence"`
}

func recordJSON(id any, line int) jsonRecord {
	if id != nil {
		return jsonRecord{ID: id}
	}

	return jsonRecord{Line: line}
}

type jsonRecord struct {
	ID   any `json:"id,omitempty"`
	Line int `json:"line,omitempty"`
}

// MarshalJSON encodes a share as its hits, of, rate, low and high, with null for the rate and
// interval of an undefined share.
func (s Share) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Hits int      `json:"hits"`
		Of   int      `json:"of"`
		Rate *float64 `json:"rate"`
		Low  *float64 `json:"low"`
		High *float64 `json:"high"`
	}{
		Hits: s.Hits,
		Of:   s.Of,
		Rate: definedOrNull(s.Rate, s.Defined),
		Low:  definedOrNull(s.Low, s.Defined),
		High: definedOrNull(s.High, s.Defined),
	})
}

func definedOrNull(value float64, defined bool) *float64 {
	if !defined {
		return nil
	}

	return &value
}
