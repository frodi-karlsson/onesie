// Package answer normalizes API answers into the shape the CLI prints and applies the per question
// policy. It performs no IO.
package answer

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

// Normalize turns one API answer into the printed shape, keeping the API's own keys when the
// question carries no labels.
func Normalize(q plan.Question, raw jev.Answer) (*Answer, error) {
	if raw == nil {
		return nil, fmt.Errorf("jev: question '%s' has no answer", q.ID)
	}

	// Checked once here rather than per branch, since every branch below reads labels the
	// question only has for its own shape.
	if want := wireKind(q.Shape); want != raw.Kind() {
		return nil, fmt.Errorf("jev: question '%s' expects a %s answer, got %s",
			q.ID, want, raw.Kind())
	}

	switch typed := raw.(type) {
	case *jev.NoulAnswer:
		return &Answer{Value: typed.Noul}, nil
	case *jev.ChoiceAnswer:
		return normalizeChoice(q, typed), nil
	case *jev.ScoreAnswer:
		return normalizeScore(q, typed), nil
	default:
		return nil, fmt.Errorf("jev: question '%s' returned an unknown answer type '%s'",
			q.ID, raw.Kind())
	}
}

func wireKind(shape plan.Shape) string {
	switch shape {
	case plan.Pick:
		return "choice"
	case plan.Rate:
		return "score"
	default:
		return "noul"
	}
}

// Answer is the normalized shape for one question. Absent fields are omitted, which is what makes
// a yes/no answer a bare value rather than a mostly empty object.
type Answer struct {
	Value      any               `json:"value,omitempty"`
	Score      *float64          `json:"score,omitempty"`
	Norm       *float64          `json:"norm,omitempty"`
	Confidence *float64          `json:"confidence,omitempty"`
	P          *Probabilities    `json:"p,omitempty"`
	Legend     map[string]string `json:"legend,omitempty"`
	Fallback   string            `json:"fallback,omitempty"`

	// Decision is the answer after policy. Decided distinguishes an absent decision from a false
	// one, which omitempty alone cannot do.
	Decision any  `json:"-"`
	Decided  bool `json:"-"`
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
			return nil, fmt.Errorf("jev: probability for '%s' is not a finite number", key)
		}

		buf = append(buf, name...)
		buf = append(buf, ':')
		buf = strconv.AppendFloat(buf, value, 'g', -1, 64)
	}

	return append(buf, '}'), nil
}

func normalizeChoice(q plan.Question, raw *jev.ChoiceAnswer) *Answer {
	keys := make([]string, 0, len(q.Options))
	for _, option := range q.Options {
		keys = append(keys, option.Name)
	}

	confidence := raw.Confidence

	return &Answer{
		Value:      raw.Choice,
		Confidence: &confidence,
		P:          &Probabilities{Keys: keys, Values: raw.Probabilities},
	}
}

func normalizeScore(q plan.Question, raw *jev.ScoreAnswer) *Answer {
	indexes := sortedIndexes(raw.Probabilities)
	winner := modal(indexes, raw.Probabilities)

	levels := len(q.Levels)
	if levels == 0 {
		levels = len(indexes)
	}

	score := raw.Score
	confidence := raw.Confidence

	norm := 0.0
	if levels > 1 {
		norm = score / float64(levels-1)
	}

	out := &Answer{
		Score:      &score,
		Norm:       &norm,
		Confidence: &confidence,
	}

	if !q.Labelled {
		out.Value = winner
		out.Legend = raw.Legend
		out.P = &Probabilities{Keys: indexes, Values: raw.Probabilities}

		return out
	}

	labels := make([]string, 0, len(q.Levels))
	values := make(map[string]float64, len(q.Levels))

	for i, level := range q.Levels {
		labels = append(labels, level.Label)
		values[level.Label] = raw.Probabilities[strconv.Itoa(i)]
	}

	out.Value = labelAt(q, winner)
	out.P = &Probabilities{Keys: labels, Values: values}

	return out
}

func sortedIndexes(probabilities map[string]float64) []string {
	keys := make([]string, 0, len(probabilities))
	for key := range probabilities {
		keys = append(keys, key)
	}

	// Numeric order, not lexical. Map iteration is randomized, and an unsorted or lexically sorted
	// scan would break the lowest index tie break in modal.
	sort.Slice(keys, func(i, j int) bool {
		left, leftErr := strconv.Atoi(keys[i])
		right, rightErr := strconv.Atoi(keys[j])

		if leftErr != nil || rightErr != nil {
			return keys[i] < keys[j]
		}

		return left < right
	})

	return keys
}

func modal(indexes []string, probabilities map[string]float64) string {
	best := ""
	highest := -1.0

	for _, index := range indexes {
		// Strict greater than, so a tie keeps the lowest index rather than the last one seen.
		if probabilities[index] > highest {
			best = index
			highest = probabilities[index]
		}
	}

	return best
}

func labelAt(q plan.Question, index string) string {
	position, err := strconv.Atoi(index)
	if err != nil || position < 0 || position >= len(q.Levels) {
		return index
	}

	return q.Levels[position].Label
}
