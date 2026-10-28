// Package answer normalizes API answers into the shape the CLI prints and applies the per question
// policy. It performs no IO.
package answer

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"

	"github.com/frodi-karlsson/onesie-go"

	"github.com/frodi-karlsson/onesie/internal/num"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// NormalizeAll normalizes the answer to every question, in question order.
func NormalizeAll(questions []plan.Question, result *onesie.Result) []*Answer {
	normalized := make([]*Answer, 0, len(questions))

	for _, question := range questions {
		normalized = append(normalized, Normalize(question, result.Answers[question.ID]))
	}

	return normalized
}

// Normalize turns one answer that passed Result.Check into the printed shape, and panics on one that
// did not. A question with no labels keeps the API's own keys.
func Normalize(q plan.Question, raw onesie.Answer) *Answer {
	switch typed := raw.(type) {
	case *onesie.NoulAnswer:
		return &Answer{Value: typed.Noul}
	case *onesie.ChoiceAnswer:
		return normalizeChoice(q, typed)
	case *onesie.ScoreAnswer:
		return normalizeScore(q, typed)
	case *onesie.BoxAnswer:
		return normalizeBox(typed)
	default:
		panic(fmt.Sprintf(
			"answer: question '%s' has a %T answer, so Result.Check did not run before Normalize", q.ID, raw))
	}
}

// MarshalJSON emits decision only when policy set one, so a false decision survives.
func (a Answer) MarshalJSON() ([]byte, error) {
	type plain Answer

	encoded, err := json.Marshal(plain(a))
	if err != nil {
		return nil, err
	}

	if !a.Decided {
		return encoded, nil
	}

	decision, err := json.Marshal(a.Decision)
	if err != nil {
		return nil, err
	}

	// Splicing appends decision last. Object key order carries no meaning in JSON, so this is
	// cheaper than hand rolling an encoder to place it anywhere else.
	trimmed := encoded[:len(encoded)-1]
	if len(trimmed) > 1 {
		trimmed = append(trimmed, ',')
	}

	trimmed = append(trimmed, []byte(`"decision":`)...)
	trimmed = append(trimmed, decision...)

	return append(trimmed, '}'), nil
}

func normalizeChoice(q plan.Question, raw *onesie.ChoiceAnswer) *Answer {
	// Result.Check proved the keys are exactly the options, so the plan's order is the whole set.
	keys := make([]string, 0, len(q.Options))
	for _, option := range q.Options {
		keys = append(keys, option.Name)
	}

	values := maps.Clone(raw.Probabilities)

	confidence := raw.Confidence

	return &Answer{
		Value:      raw.Choice,
		Confidence: &confidence,
		P:          &Probabilities{Keys: keys, Values: values},
	}
}

func normalizeScore(q plan.Question, raw *onesie.ScoreAnswer) *Answer {
	score := raw.Score
	confidence := raw.Confidence

	norm := 0.0
	if top := len(q.Levels) - 1; top > 0 {
		norm = num.Rounded(score / float64(top))
	}

	out := &Answer{
		Score:      &score,
		Norm:       &norm,
		Confidence: &confidence,
	}

	if !q.Labelled {
		// Result.Check proved the keys are exactly the level indexes.
		indexes := onesie.NumericOrder(slices.Collect(maps.Keys(raw.Probabilities)))

		out.Value = modal(indexes, raw.Probabilities)
		out.Legend = raw.Legend
		out.P = &Probabilities{Keys: indexes, Values: maps.Clone(raw.Probabilities)}

		return out
	}

	labels := make([]string, 0, len(q.Levels))
	values := make(map[string]float64, len(q.Levels))

	for i, level := range q.Levels {
		labels = append(labels, level.Label)
		values[level.Label] = raw.Probabilities[strconv.Itoa(i)]
	}

	out.Value = modal(labels, values)
	out.P = &Probabilities{Keys: labels, Values: values}

	return out
}

func normalizeBox(raw *onesie.BoxAnswer) *Answer {
	edges := raw.Edges
	confidence := raw.Confidence

	return &Answer{
		Left: &edges.Left, Top: &edges.Top, Right: &edges.Right, Bottom: &edges.Bottom,
		Confidence: &confidence,
	}
}

// Box reports the answer's edges when it answers a box question.
func (a *Answer) Box() (Edges, bool) {
	if a.Left == nil || a.Top == nil || a.Right == nil || a.Bottom == nil {
		return Edges{}, false
	}

	return Edges{Left: *a.Left, Top: *a.Top, Right: *a.Right, Bottom: *a.Bottom}, true
}

// Edges is a box answer's four edges in the order every output mode writes them.
type Edges struct {
	Left   float64 `json:"left"`
	Top    float64 `json:"top"`
	Right  float64 `json:"right"`
	Bottom float64 `json:"bottom"`
}

// String writes the edges for a person, as the table does.
func (e Edges) String() string {
	return fmt.Sprintf("left %s  top %s  right %s  bottom %s",
		num.Format(e.Left), num.Format(e.Top), num.Format(e.Right), num.Format(e.Bottom))
}

// SumFits reports a distribution's sum, rounded to four places, and whether it is 1 within 0.02.
func SumFits(probabilities []float64) (float64, bool) {
	sum := 0.0
	for _, value := range probabilities {
		sum += value
	}

	// A provider that writes two places per entry leaves the sum a hundredth or two off 1.
	const tolerance = 0.02

	return num.Rounded(sum), math.Abs(sum-1) <= tolerance+1e-9
}

// Answer is the normalized shape for one question. Absent fields are omitted, which is what makes
// a yes or no answer a bare value, not a mostly empty object.
type Answer struct {
	Value      any               `json:"value,omitempty"`
	Score      *float64          `json:"score,omitempty"`
	Norm       *float64          `json:"norm,omitempty"`
	Left       *float64          `json:"left,omitempty"`
	Top        *float64          `json:"top,omitempty"`
	Right      *float64          `json:"right,omitempty"`
	Bottom     *float64          `json:"bottom,omitempty"`
	Confidence *float64          `json:"confidence,omitempty"`
	P          *Probabilities    `json:"p,omitempty"`
	Legend     map[string]string `json:"legend,omitempty"`
	Fallback   string            `json:"fallback,omitempty"`

	// Decision is the answer after policy. Decided distinguishes an absent decision from a false
	// one, which omitempty alone cannot do.
	Decision any  `json:"-"`
	Decided  bool `json:"-"`
}

// Probabilities marshals in a fixed key order. A plain map would not, because encoding/json sorts
// map keys and the output must follow the order the question defined.
type Probabilities struct {
	Keys   []string
	Values map[string]float64
}

// MarshalJSON writes the entries in Keys order.
func (p Probabilities) MarshalJSON() ([]byte, error) {
	var buf []byte

	buf = append(buf, '{')

	for i, key := range p.Keys {
		if i > 0 {
			buf = append(buf, ',')
		}

		name, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}

		// strconv writes NaN and infinities bare, which is not valid JSON. This tool's contract is
		// one parseable line per record, so failing loudly beats breaking every consumer.
		value := p.Values[key]
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("probability for '%s' is not a finite number", key)
		}

		buf = append(buf, name...)
		buf = append(buf, ':')
		buf = append(buf, num.Format(value)...)
	}

	return append(buf, '}'), nil
}

func modal(levels []string, probabilities map[string]float64) string {
	best := ""
	highest := -1.0

	for _, level := range levels {
		// Strictly greater, so a tie keeps the lowest level.
		if probabilities[level] > highest {
			best = level
			highest = probabilities[level]
		}
	}

	return best
}
