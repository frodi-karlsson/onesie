package jev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// Levels widens plain strings into Score criteria, which is what most rubrics are.
func Levels(levels ...string) []any {
	widened := make([]any, len(levels))
	for i, level := range levels {
		widened[i] = level
	}

	return widened
}

// Questions is an ordered list of questions, written to the wire as a JSON object in slice order,
// so a request body carries its questions in the order they were authored. A shared value must not
// be appended to, since two appends onto a base with spare capacity write the same backing array.
type Questions []NamedQuestion

// NamedQuestion pairs a question with the id it answers under.
type NamedQuestion struct {
	ID       string
	Question Question
}

// MarshalJSON writes the questions as a JSON object, keeping slice order.
func (q Questions) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteByte('{')

	for i, named := range q {
		if i > 0 {
			buf.WriteByte(',')
		}

		// Through json.Marshal rather than quoting by hand, because an id is not character checked
		// here and one carrying a quote would otherwise produce a body no parser can read.
		key, err := json.Marshal(named.ID)
		if err != nil {
			return nil, err
		}

		buf.Write(key)
		buf.WriteByte(':')

		value, err := json.Marshal(named.Question)
		if err != nil {
			return nil, err
		}

		buf.Write(value)
	}

	buf.WriteByte('}')

	return buf.Bytes(), nil
}

// ValidateQuestions rejects a request the API would reject, before it is sent.
func ValidateQuestions(questions Questions) error {
	if len(questions) == 0 {
		return &ValidationError{Message: "at least one question is required"}
	}

	seen := make(map[string]struct{}, len(questions))

	for _, named := range questions {
		// The body is keyed by id, so a repeated one collapses into a single API answer.
		if _, taken := seen[named.ID]; taken {
			return &ValidationError{
				Question: named.ID,
				Message:  fmt.Sprintf("duplicate question id %q", named.ID),
			}
		}

		seen[named.ID] = struct{}{}

		if isNil(named.Question) {
			return &ValidationError{
				Question: named.ID,
				Message:  fmt.Sprintf("question %q must not be nil", named.ID),
			}
		}

		if err := named.Question.validate(named.ID); err != nil {
			return err
		}
	}

	return nil
}

func isNil(question Question) bool {
	if question == nil {
		return true
	}

	// A typed nil pointer is not equal to nil, and every validate has a value receiver, so calling
	// through one would dereference it and panic.
	value := reflect.ValueOf(question)

	return value.Kind() == reflect.Pointer && value.IsNil()
}

// Question is one of Noul, Choice or Score. The interface is closed, because validate is
// unexported, so no type outside this package can satisfy it.
type Question interface {
	json.Marshaler

	validate(name string) error
}

// Noul asks a yes or no question and is answered with the probability of yes.
type Noul struct {
	Instructions any
	Criteria     *NoulCriteria
}

// MarshalJSON encodes the question in the shape the API expects.
func (q Noul) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type         string        `json:"type"`
		Instructions any           `json:"instructions"`
		Criteria     *NoulCriteria `json:"criteria,omitempty"`
	}{Type: "noul", Instructions: q.Instructions, Criteria: q.Criteria})
}

func (Noul) validate(string) error {
	return nil
}

// NoulCriteria describes what a yes and a no mean.
type NoulCriteria struct {
	True  any `json:"true"`
	False any `json:"false"`
}

// Choice picks one option from a set and is answered with a distribution over them.
type Choice struct {
	Instructions any
	Criteria     map[string]any
}

// MarshalJSON encodes the question in the shape the API expects.
func (q Choice) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type         string         `json:"type"`
		Instructions any            `json:"instructions"`
		Criteria     map[string]any `json:"criteria"`
	}{Type: "choice", Instructions: q.Instructions, Criteria: q.Criteria})
}

func (Choice) validate(string) error {
	return nil
}

// Score rates the state against an ordered rubric and is answered with a weighted value.
type Score struct {
	Instructions any
	Criteria     []any
}

// MarshalJSON encodes the question in the shape the API expects.
func (q Score) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type         string `json:"type"`
		Instructions any    `json:"instructions"`
		Criteria     []any  `json:"criteria"`
	}{Type: "score", Instructions: q.Instructions, Criteria: q.Criteria})
}

func (q Score) validate(name string) error {
	if len(q.Criteria) >= 2 {
		return nil
	}

	return &ValidationError{
		Question: name,
		Message: fmt.Sprintf(
			"score question %q has %d criteria, at least two are required",
			name, len(q.Criteria),
		),
	}
}
