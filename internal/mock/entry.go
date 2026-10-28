package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/num"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const normTolerance = 1e-9

var (
	lineKeys = []string{"id", "line", "model", "usage", "assert", "abstain", "error"}

	// The keys -o json writes inside an answer, which a replay accepts and leaves to the real code.
	echoedKeys = []string{"p", "legend", "decision", "fallback", "score", "norm"}
)

func (p parser) entry(fields map[string]any, where string) (*Entry, error) {
	ids := make([]string, 0, len(p.questions))
	for _, question := range p.questions {
		ids = append(ids, question.ID)
	}

	for _, key := range sortedKeys(fields) {
		if !slices.Contains(lineKeys, key) && !slices.Contains(ids, key) {
			return nil, fmt.Errorf("%s: '%s' is not a question. Questions: %s",
				where, key, strings.Join(ids, ", "))
		}
	}

	if failure, failed := fields["error"]; failed {
		return p.failure(failure, where)
	}

	answers := make(map[string]onesie.Answer, len(p.asked))

	for _, question := range p.asked {
		given, found := fields[question.ID]
		if !found {
			return nil, fmt.Errorf("%s: the entry has no answer for question '%s'. "+
				"Every entry answers every question the run asks", where, question.ID)
		}

		built, err := parseAnswer(question, given)
		if err != nil {
			return nil, fmt.Errorf("%s: question '%s' %w", where, question.ID, err)
		}

		answers[question.ID] = built
	}

	// encoding/json sorts map keys, so two entries with the same answers encode the same way.
	encoded, err := json.Marshal(answers)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}

	result := &onesie.Result{Answers: answers}

	// Check returns nil or an *AnswerFault, so no other error can reach past this.
	var fault *onesie.AnswerFault
	if errors.As(result.Check(p.wired), &fault) {
		return nil, fmt.Errorf("%s: %s", where, fault.Reason())
	}

	return &Entry{answers: answers, key: "answers " + string(encoded)}, nil
}

func sortedKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func (p parser) failure(failure any, where string) (*Entry, error) {
	refused := fmt.Errorf("%s: error %s is not one onesie can replay. Use a status from 400 to 599, "+
		"timeout or connection", where, shown(failure))

	switch typed := failure.(type) {
	case json.Number:
		status, isStatus := errorStatus(typed)
		if !isStatus {
			return nil, refused
		}

		return statusEntry(status, nil), nil
	case string:
		switch typed {
		case "timeout":
			return &Entry{fault: context.DeadlineExceeded, key: "error timeout"}, nil
		case "connection":
			return connectionEntry(), nil
		}
	case map[string]any:
		return writtenFailure(typed, refused)
	}

	return nil, refused
}

func writtenFailure(failure map[string]any, refused error) (*Entry, error) {
	switch failure["kind"] {
	case "http":
		number, isNumber := failure["status"].(json.Number)
		if !isNumber {
			return nil, refused
		}

		status, isStatus := errorStatus(number)
		if !isStatus {
			return nil, refused
		}

		return httpEntry(status, failure)
	case "transport":
		return connectionEntry(), nil
	case "response":
		return &Entry{key: "error response"}, nil
	case "input":
		// A line onesie could not read never became a request, so it has no answer to replay.
		return nil, nil
	}

	return nil, refused
}

func errorStatus(number json.Number) (int, bool) {
	status, err := strconv.Atoi(number.String())
	if err != nil || status < 400 || status > 599 {
		return 0, false
	}

	return status, true
}

func httpEntry(status int, failure map[string]any) (*Entry, error) {
	switch given := failure["body"].(type) {
	case nil:
		return statusEntry(status, nil), nil
	case string:
		return statusEntry(status, []byte(given)), nil
	default:
		body, err := json.Marshal(given)
		if err != nil {
			return nil, err
		}

		return statusEntry(status, body), nil
	}
}

func statusEntry(status int, body []byte) *Entry {
	key := fmt.Sprintf("error http %d", status)
	if body != nil {
		key += " " + string(body)
	}

	return &Entry{status: status, body: body, key: key}
}

func connectionEntry() *Entry {
	return &Entry{fault: errors.New("mock connection error"), key: "error connection"}
}

func parseAnswer(question plan.Question, given any) (onesie.Answer, error) {
	if question.Shape == plan.Box {
		return boxAnswer(given)
	}

	parts, err := partsOf(question, given)
	if err != nil {
		return nil, err
	}

	switch question.Shape {
	case plan.Pick:
		return pickAnswer(question, parts)
	case plan.Rate:
		return rateAnswer(question, parts)
	default:
		return yesNoAnswer(parts.value)
	}
}

func partsOf(question plan.Question, given any) (answerParts, error) {
	parts := answerParts{value: given, confidence: 1}

	object, isObject := given.(map[string]any)
	if !isObject {
		return parts, nil
	}

	for _, key := range sortedKeys(object) {
		taken := key == "value" || slices.Contains(echoedKeys, key) ||
			(key == "confidence" && question.Shape != plan.Noul)
		if !taken {
			return parts, fmt.Errorf("carries the key '%s', which a mock answer does not take", key)
		}
	}

	value, found := object["value"]
	if !found {
		return parts, errors.New("has an answer object with no value")
	}

	parts.value = value
	parts.score = object["score"]
	parts.norm = object["norm"]
	parts.p = object["p"]

	if given, found := object["confidence"]; found {
		confidence, isNumber := finiteNumber(given)
		if !isNumber {
			return parts, fmt.Errorf("has confidence %s, which is not a number", shown(given))
		}

		parts.confidence = confidence
	}

	return parts, nil
}

func yesNoAnswer(given any) (onesie.Answer, error) {
	probability, isNumber := finiteNumber(given)
	if !isNumber {
		return nil, fmt.Errorf("answered %s, which is not a probability", shown(given))
	}

	return &onesie.NoulAnswer{Noul: probability}, nil
}

func pickAnswer(question plan.Question, parts answerParts) (onesie.Answer, error) {
	names := make([]string, 0, len(question.Options))
	for _, option := range question.Options {
		names = append(names, option.Name)
	}

	name, err := nameOf(parts.value, "an option")
	if err != nil {
		return nil, err
	}

	if !slices.Contains(names, name) {
		return nil, fmt.Errorf("picked '%s', which is not an option. Options: %s", name, strings.Join(names, ", "))
	}

	probabilities, err := replayed(parts.p, names, name, false)
	if err != nil {
		return nil, err
	}

	if probabilities == nil {
		probabilities = synthesized(names, name, parts.confidence)
	}

	return &onesie.ChoiceAnswer{Choice: name, Confidence: parts.confidence, Probabilities: probabilities}, nil
}

func rateAnswer(question plan.Question, parts answerParts) (onesie.Answer, error) {
	names := levelNames(question)

	name, err := nameOf(parts.value, "a level")
	if err != nil {
		return nil, err
	}

	index := slices.Index(names, name)
	if index < 0 {
		return nil, fmt.Errorf("rated '%s', which is not a level. Levels: %s", name, strings.Join(names, ", "))
	}

	given, err := replayed(parts.p, names, name, true)
	if err != nil {
		return nil, err
	}

	if given == nil {
		given = synthesized(names, name, parts.confidence)
	}

	top := float64(len(names) - 1)
	score := averaged(names, given, index)

	if parts.score != nil {
		replayed, isNumber := finiteNumber(parts.score)
		if !isNumber {
			return nil, fmt.Errorf("has score %s, which is not a number", shown(parts.score))
		}

		score = replayed
	}

	if parts.norm != nil {
		want := 0.0
		if top > 0 {
			want = score / top
		}

		norm, isNumber := finiteNumber(parts.norm)
		// A norm replays whether it is exact or rounded to four places, the form onesie prints.
		if !isNumber || (math.Abs(norm-want) > normTolerance && norm != num.Rounded(want)) {
			return nil, fmt.Errorf("has norm %s, but score %s on %d levels gives %s",
				shown(parts.norm), strconv.FormatFloat(score, 'g', -1, 64), len(names),
				strconv.FormatFloat(want, 'g', -1, 64))
		}
	}

	probabilities := make(map[string]float64, len(names))
	legend := make(map[string]string, len(names))

	for i, level := range question.Levels {
		key := strconv.Itoa(i)
		probabilities[key] = given[names[i]]
		legend[key] = legendOf(question, level)
	}

	return &onesie.ScoreAnswer{
		Score: score, Confidence: parts.confidence, Legend: legend, Probabilities: probabilities,
	}, nil
}

func averaged(names []string, p map[string]float64, index int) float64 {
	// A p outside [0,1] would derive a score Result.Check refuses first, hiding the p it names.
	for _, name := range names {
		if p[name] < 0 || p[name] > 1 {
			return float64(index)
		}
	}

	weighted, total := 0.0, 0.0
	for i, name := range names {
		weighted += float64(i) * p[name]
		total += p[name]
	}

	// A written p adds up to 1 only within the rounding Result.Check allows, so the average is taken
	// over what it holds.
	if total == 0 {
		return float64(index)
	}

	return num.Rounded(weighted / total)
}

func synthesized(names []string, value string, confidence float64) map[string]float64 {
	// At least twice what each other name takes keeps the value the strict mode, which replayed
	// demands of a written p, since a rate reads its value from the first of equal modes. Rounded up
	// to four places, so the value's p is never below its confidence. The small allowance keeps
	// float noise, as in 0.6 times 10000, from rounding up a whole step. Capped at 1, so a confidence
	// above 1 reaches Result.Check as the confidence it is and not as a p above 1.
	share := math.Ceil(max(min(confidence, 1), 2/float64(len(names)+1))*1e4-1e-6) / 1e4
	rest := num.Rounded((1 - share) / float64(len(names)-1))

	probabilities := make(map[string]float64, len(names))
	for _, name := range names {
		probabilities[name] = rest
	}

	probabilities[value] = share

	return probabilities
}

func boxAnswer(given any) (onesie.Answer, error) {
	object, isObject := given.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("answered %s, which is not a box. A box answer is an object of "+
			"left, top, right, bottom and an optional confidence", shown(given))
	}

	for _, key := range sortedKeys(object) {
		if !slices.Contains([]string{"left", "top", "right", "bottom", "confidence"}, key) {
			return nil, fmt.Errorf("carries the key '%s', which a mock box answer does not take", key)
		}
	}

	edges := make(map[string]float64, 4)

	for _, name := range []string{"left", "top", "right", "bottom"} {
		value, found := object[name]
		if !found {
			return nil, fmt.Errorf("has no '%s'. A box answer has all four edges", name)
		}

		edge, isNumber := finiteNumber(value)
		if !isNumber {
			return nil, fmt.Errorf("has %s %s, which is not a number", name, shown(value))
		}

		edges[name] = edge
	}

	confidence := 1.0

	if value, found := object["confidence"]; found {
		parsed, isNumber := finiteNumber(value)
		if !isNumber {
			return nil, fmt.Errorf("has confidence %s, which is not a number", shown(value))
		}

		confidence = parsed
	}

	return &onesie.BoxAnswer{
		Edges: onesie.Edges{
			Left: edges["left"], Top: edges["top"], Right: edges["right"], Bottom: edges["bottom"],
		},
		Confidence: confidence,
	}, nil
}

func replayed(given any, names []string, value string, firstWins bool) (map[string]float64, error) {
	if given == nil {
		return nil, nil
	}

	object, isObject := given.(map[string]any)
	if !isObject {
		return nil, fmt.Errorf("has p %s, which is not an object of probabilities", shown(given))
	}

	for _, key := range sortedKeys(object) {
		if !slices.Contains(names, key) {
			return nil, fmt.Errorf("has p with the key '%s', which is not one of %s", key, strings.Join(names, ", "))
		}
	}

	probabilities := make(map[string]float64, len(names))

	for _, name := range names {
		raw, found := object[name]
		if !found {
			return nil, fmt.Errorf("has p with no entry for '%s'", name)
		}

		probability, isNumber := finiteNumber(raw)
		if !isNumber {
			return nil, fmt.Errorf("has p for '%s' of %s, which is not a number", name, shown(raw))
		}

		probabilities[name] = probability
	}

	for i, name := range names {
		// onesie reads a rate's value from its modal level, and the first of equal levels wins, so a
		// tie ahead of the value would print another level. A pick's value is the pick itself.
		ahead := firstWins && i < slices.Index(names, value)
		if probabilities[name] > probabilities[value] || (ahead && probabilities[name] == probabilities[value]) {
			return nil, fmt.Errorf("has p that makes '%s' likelier than its value '%s'", name, value)
		}
	}

	return probabilities, nil
}

func levelNames(question plan.Question) []string {
	names := make([]string, 0, len(question.Levels))

	for i, level := range question.Levels {
		// A rate question from a request body has no level names, and onesie prints its level index.
		if !question.Labelled {
			names = append(names, strconv.Itoa(i))

			continue
		}

		names = append(names, level.Label)
	}

	return names
}

func legendOf(question plan.Question, level plan.Level) string {
	if question.Labelled {
		return level.Label
	}

	if text, isText := level.Desc.(string); isText {
		return text
	}

	return ""
}

func nameOf(given any, what string) (string, error) {
	switch typed := given.(type) {
	case string:
		return typed, nil
	case json.Number:
		// A number whose text is a name is that name, as a calibrate label is, so 4 is --rate 1,2,3,4,5.
		return typed.String(), nil
	}

	return "", fmt.Errorf("answered %s, which is not the name of %s", shown(given), what)
}

func finiteNumber(given any) (float64, bool) {
	number, isNumber := given.(json.Number)
	if !isNumber {
		return 0, false
	}

	value, err := number.Float64()
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}

	return value, true
}

func shown(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}

	return string(encoded)
}

type answerParts struct {
	value      any
	confidence float64
	score      any
	norm       any
	p          any
}

// Key names what the entry answers, so two entries with the same answers share a key.
func (e Entry) Key() string {
	return e.key
}

// Entry is one checked answer, either an answer to every question or a failure, which Transport
// turns into what a server sends.
type Entry struct {
	answers map[string]onesie.Answer
	status  int
	body    []byte
	fault   error
	key     string
}
