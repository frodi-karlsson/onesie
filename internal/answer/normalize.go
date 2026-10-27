// Package answer normalizes API answers into the shape the CLI prints and applies the per question
// policy. It performs no IO.
package answer

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie-go"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Normalize turns one API answer into the printed shape, keeping the API's own keys when the
// question carries no labels.
func Normalize(q plan.Question, raw onesie.Answer) (*Answer, error) {
	if raw == nil {
		return nil, unusable("question '%s' has no answer", q.ID)
	}

	// Checked once here rather than per branch, since every branch below reads labels the
	// question only has for its own shape.
	if want := wireKind(q.Shape); want != raw.Kind() {
		return nil, unusable("question '%s' expects a %s answer, got %s", q.ID, want, raw.Kind())
	}

	switch typed := raw.(type) {
	case *onesie.NoulAnswer:
		if !unit(typed.Noul) {
			return nil, unusable("question '%s' answered %s, which lies outside [0,1]", q.ID, Number(typed.Noul))
		}

		return &Answer{Value: typed.Noul}, nil
	case *onesie.ChoiceAnswer:
		if err := checkChoice(q, typed); err != nil {
			return nil, err
		}

		return normalizeChoice(q, typed), nil
	case *onesie.ScoreAnswer:
		if err := checkScore(q, typed); err != nil {
			return nil, err
		}

		return normalizeScore(q, typed), nil
	case *onesie.BoxAnswer:
		return normalizeBox(q, typed)
	default:
		return nil, unusable("question '%s' returned an unknown answer type '%s'", q.ID, raw.Kind())
	}
}

func unusable(format string, args ...any) error {
	// An answer that does not fit the question is deterministic, so it is a 200 whose body onesie
	// could not use rather than a transport failure. That gives it kind response in a stream and
	// exit 4 in a single shot run, and the same retry would only get it again.
	return &onesie.ResponseError{
		Status:  http.StatusOK,
		Message: "onesie: 200 response could not be used: " + fmt.Sprintf(format, args...),
	}
}

func unit(value float64) bool {
	return !math.IsNaN(value) && value >= 0 && value <= 1
}

func checkChoice(q plan.Question, raw *onesie.ChoiceAnswer) error {
	names := make([]string, 0, len(q.Options))
	for _, option := range q.Options {
		names = append(names, option.Name)
	}

	options := strings.Join(names, ", ")

	if !slices.Contains(names, raw.Choice) {
		return unusable("question '%s' picked '%s', which is not one of its options: %s", q.ID, raw.Choice, options)
	}

	for _, key := range slices.Sorted(maps.Keys(raw.Probabilities)) {
		if !slices.Contains(names, key) {
			return unusable("question '%s' has p for '%s', which is not one of its options: %s", q.ID, key, options)
		}
	}

	for _, name := range names {
		if _, found := raw.Probabilities[name]; !found {
			return unusable("question '%s' has no p for '%s'", q.ID, name)
		}
	}

	return checkSpread(q.ID, names, raw.Probabilities, raw.Confidence)
}

func checkScore(q plan.Question, raw *onesie.ScoreAnswer) error {
	top := len(q.Levels) - 1
	if top < 0 {
		top = len(raw.Probabilities) - 1
	}

	if math.IsNaN(raw.Score) || raw.Score < 0 || raw.Score > float64(top) {
		return unusable("question '%s' has score %s, which lies outside [0,%d]", q.ID, Number(raw.Score), top)
	}

	indexes := make([]string, 0, top+1)
	for i := range top + 1 {
		indexes = append(indexes, strconv.Itoa(i))
	}

	for _, key := range sortedIndexes(raw.Probabilities) {
		if !slices.Contains(indexes, key) {
			return unusable("question '%s' has p for '%s', which is not a level index from 0 to %d", q.ID, key, top)
		}
	}

	for i, index := range indexes {
		if _, found := raw.Probabilities[index]; !found {
			return unusable("question '%s' has no p for level %d", q.ID, i)
		}
	}

	return checkSpread(q.ID, indexes, raw.Probabilities, raw.Confidence)
}

func checkSpread(id string, keys []string, probabilities map[string]float64, confidence float64) error {
	values := make([]float64, 0, len(keys))

	for _, key := range keys {
		value := probabilities[key]
		if !unit(value) {
			return unusable("question '%s' has p for '%s' of %s, which lies outside [0,1]", id, key, Number(value))
		}

		values = append(values, value)
	}

	if sum, fits := SumFits(values); !fits {
		return unusable("question '%s' has p that adds up to %s, not 1", id, Number(sum))
	}

	if !unit(confidence) {
		return unusable("question '%s' has confidence %s, which lies outside [0,1]", id, Number(confidence))
	}

	return nil
}

func wireKind(shape plan.Shape) string {
	switch shape {
	case plan.Pick:
		return "choice"
	case plan.Rate:
		return "score"
	case plan.Box:
		return "box"
	default:
		return "noul"
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
	// checkChoice proved the keys are exactly the options, so the plan's order is the whole set.
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
		norm = Rounded(score / float64(levels-1))
	}

	out := &Answer{
		Score:      &score,
		Norm:       &norm,
		Confidence: &confidence,
	}

	if !q.Labelled {
		out.Value = winner
		out.Legend = raw.Legend
		out.P = byIndex(q, raw.Probabilities)

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

func normalizeBox(q plan.Question, raw *onesie.BoxAnswer) (*Answer, error) {
	edges := raw.Edges
	values := []float64{edges.Left, edges.Top, edges.Right, edges.Bottom, raw.Confidence}

	if slices.ContainsFunc(values, func(v float64) bool { return math.IsNaN(v) || v < 0 || v > 1 }) {
		return nil, unusable("box question '%s' has an edge or a confidence outside [0,1]", q.ID)
	}

	if edges.Left >= edges.Right || edges.Top >= edges.Bottom {
		return nil, unusable("box question '%s' has a left edge not left of its right edge, "+
			"or a top edge not above its bottom edge", q.ID)
	}

	confidence := raw.Confidence

	return &Answer{
		Left: &edges.Left, Top: &edges.Top, Right: &edges.Right, Bottom: &edges.Bottom,
		Confidence: &confidence,
	}, nil
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
		edgeText(e.Left), edgeText(e.Top), edgeText(e.Right), edgeText(e.Bottom))
}

func edgeText(value float64) string {
	return Number(value)
}

// SumFits reports a distribution's sum, rounded to four places, and whether it is 1 within the
// rounding a provider that writes two places per entry can leave, half a hundredth each, at least 0.01.
func SumFits(probabilities []float64) (float64, bool) {
	sum := 0.0
	for _, value := range probabilities {
		sum += value
	}

	tolerance := max(0.01, 0.005*float64(len(probabilities)))

	return Rounded(sum), math.Abs(sum-1) <= tolerance+1e-9
}

// Rounded rounds a number onesie computes itself to four places, so a gate, -o json and the table
// all see the same value rather than float noise.
func Rounded(value float64) float64 {
	return math.Round(value*1e4) / 1e4
}

// Number writes a number of an answer as -o json does, in the shortest form that reads back as the
// same number and never rounded, so every output mode shows the number a gate compares.
func Number(value float64) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return strconv.FormatFloat(value, 'g', -1, 64)
	}

	return string(encoded)
}

// Answer is the normalized shape for one question. Absent fields are omitted, which is what makes
// a yes/no answer a bare value rather than a mostly empty object.
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

func byIndex(q plan.Question, probabilities map[string]float64) *Probabilities {
	values := make(map[string]float64, len(q.Levels)+len(probabilities))
	for i := range q.Levels {
		values[strconv.Itoa(i)] = 0
	}

	maps.Copy(values, probabilities)

	return &Probabilities{Keys: sortedIndexes(values), Values: values}
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
			return nil, fmt.Errorf("onesie: probability for '%s' is not a finite number", key)
		}

		buf = append(buf, name...)
		buf = append(buf, ':')
		buf = append(buf, Number(value)...)
	}

	return append(buf, '}'), nil
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
