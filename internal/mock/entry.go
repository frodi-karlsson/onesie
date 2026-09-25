package mock

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Model is the model name a mocked answer carries.
const Model = "mock"

const normTolerance = 1e-9

var (
	lineKeys = []string{"id", "model", "usage", "assert", "abstain", "error"}

	// The keys -o json writes inside an answer, which a replay accepts and leaves to the real code.
	echoedKeys = []string{"p", "legend", "decision", "fallback", "score", "norm"}
)

func parseEntry(fields map[string]any, questions []plan.Question, where string) (*Entry, bool, error) {
	ids := make([]string, 0, len(questions))
	for _, question := range questions {
		ids = append(ids, question.ID)
	}

	for _, key := range sortedKeys(fields) {
		if !slices.Contains(lineKeys, key) && !slices.Contains(ids, key) {
			return nil, false, fmt.Errorf("%s: '%s' is not a question. Questions: %s",
				where, key, strings.Join(ids, ", "))
		}
	}

	if failure, failed := fields["error"]; failed {
		return parseFailure(failure, where)
	}

	answers := make(map[string]jev.Answer, len(questions))

	for _, question := range questions {
		given, found := fields[question.ID]
		if !found {
			return nil, false, fmt.Errorf("%s: question '%s' has no answer, and an entry answers every question",
				where, question.ID)
		}

		built, err := parseAnswer(question, given)
		if err != nil {
			return nil, false, fmt.Errorf("%s: question '%s' %w", where, question.ID, err)
		}

		answers[question.ID] = built
	}

	// encoding/json sorts map keys, so two entries with the same answers encode the same way.
	encoded, err := json.Marshal(answers)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", where, err)
	}

	result := &jev.Result{Model: Model, Answers: answers}

	return &Entry{result: result, key: "answers " + string(encoded)}, true, nil
}

func sortedKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func parseFailure(failure any, where string) (*Entry, bool, error) {
	refused := fmt.Errorf("%s: error %s is not one onesie can replay. Use a status from 400 to 599, "+
		"timeout or connection", where, shown(failure))

	switch typed := failure.(type) {
	case json.Number:
		status, isStatus := errorStatus(typed)
		if !isStatus {
			return nil, false, refused
		}

		return statusEntry(status), true, nil
	case string:
		switch typed {
		case "timeout":
			return timeoutEntry(), true, nil
		case "connection":
			return connectionEntry(), true, nil
		}
	case map[string]any:
		return writtenFailure(typed, refused)
	}

	return nil, false, refused
}

func writtenFailure(failure map[string]any, refused error) (*Entry, bool, error) {
	switch failure["kind"] {
	case "http":
		number, isNumber := failure["status"].(json.Number)
		if !isNumber {
			return nil, false, refused
		}

		status, isStatus := errorStatus(number)
		if !isStatus {
			return nil, false, refused
		}

		return statusEntry(status), true, nil
	case "transport":
		return connectionEntry(), true, nil
	case "response":
		return &Entry{
			err: &jev.ResponseError{Status: http.StatusOK, Message: "onesie: mock response onesie could not use"},
			key: "error response",
		}, true, nil
	case "input":
		// A line onesie could not read never became a request, so it has no answer to replay.
		return nil, false, nil
	}

	return nil, false, refused
}

func errorStatus(number json.Number) (int, bool) {
	status, err := strconv.Atoi(number.String())
	if err != nil || status < 400 || status > 599 {
		return 0, false
	}

	return status, true
}

func statusEntry(status int) *Entry {
	return &Entry{
		err: jev.NewAPIError(status, fmt.Sprintf("onesie: mock status %d", status)),
		key: fmt.Sprintf("error http %d", status),
	}
}

func timeoutEntry() *Entry {
	return &Entry{
		err: &jev.TimeoutError{ConnectionError: jev.ConnectionError{Err: errors.New("mock timeout")}},
		key: "error timeout",
	}
}

func connectionEntry() *Entry {
	return &Entry{
		err: &jev.ConnectionError{Err: errors.New("mock connection error")},
		key: "error connection",
	}
}

func parseAnswer(question plan.Question, given any) (jev.Answer, error) {
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

	if given, found := object["confidence"]; found {
		confidence, isUnit := unitNumber(given)
		if !isUnit {
			return parts, fmt.Errorf("has confidence %s, which lies outside [0,1]", shown(given))
		}

		parts.confidence = confidence
	}

	return parts, nil
}

func yesNoAnswer(given any) (jev.Answer, error) {
	if _, isNumber := given.(json.Number); !isNumber {
		return nil, fmt.Errorf("answered %s, which is not a probability", shown(given))
	}

	probability, isUnit := unitNumber(given)
	if !isUnit {
		return nil, fmt.Errorf("answered %s, which lies outside [0,1]", shown(given))
	}

	return &jev.NoulAnswer{Noul: probability}, nil
}

func pickAnswer(question plan.Question, parts answerParts) (jev.Answer, error) {
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

	// The value comes from Choice, so the rest of the mass only needs to add up.
	probabilities := make(map[string]float64, len(names))
	for _, other := range names {
		probabilities[other] = (1 - parts.confidence) / float64(max(len(names)-1, 1))
	}

	probabilities[name] = parts.confidence

	return &jev.ChoiceAnswer{Choice: name, Confidence: parts.confidence, Probabilities: probabilities}, nil
}

func rateAnswer(question plan.Question, parts answerParts) (jev.Answer, error) {
	names := levelNames(question)

	name, err := nameOf(parts.value, "a level")
	if err != nil {
		return nil, err
	}

	index := slices.Index(names, name)
	if index < 0 {
		return nil, fmt.Errorf("rated '%s', which is not a level. Levels: %s", name, strings.Join(names, ", "))
	}

	top := float64(len(names) - 1)
	score := float64(index)

	if parts.score != nil {
		replayed, isNumber := finiteNumber(parts.score)
		if !isNumber || replayed < 0 || replayed > top {
			return nil, fmt.Errorf("has score %s, which lies outside [0,%s]", shown(parts.score), strconv.Itoa(len(names)-1))
		}

		score = replayed
	}

	if parts.norm != nil {
		want := 0.0
		if top > 0 {
			want = score / top
		}

		norm, isNumber := finiteNumber(parts.norm)
		if !isNumber || math.Abs(norm-want) > normTolerance {
			return nil, fmt.Errorf("has norm %s, but score %s on %d levels gives %s",
				shown(parts.norm), strconv.FormatFloat(score, 'g', -1, 64), len(names),
				strconv.FormatFloat(want, 'g', -1, 64))
		}
	}

	// All the mass on the chosen level, so the modal level the value is read from is always that one.
	probabilities := make(map[string]float64, len(names))
	legend := make(map[string]string, len(names))

	for i, level := range question.Levels {
		key := strconv.Itoa(i)
		probabilities[key] = 0
		legend[key] = legendOf(question, level)
	}

	probabilities[strconv.Itoa(index)] = 1

	return &jev.ScoreAnswer{
		Score: score, Confidence: parts.confidence, Legend: legend, Probabilities: probabilities,
	}, nil
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

func unitNumber(given any) (float64, bool) {
	value, isNumber := finiteNumber(given)

	return value, isNumber && value >= 0 && value <= 1
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
}

// Result returns the response the API would have sent, or the error the request would have failed
// with.
func (e Entry) Result() (*jev.Result, error) {
	if e.err != nil {
		return nil, e.err
	}

	// A copy, since one entry of the object shape answers every record and a caller may change it.
	result := *e.result

	return &result, nil
}

// Key names what the entry answers, so two entries with the same answers share a key.
func (e Entry) Key() string {
	return e.key
}

// Entry is one checked answer, either an answer to every question or a failure.
type Entry struct {
	result *jev.Result
	err    error
	key    string
}
