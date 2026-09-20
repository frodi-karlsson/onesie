package jev

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// Levels widens plain strings into Score criteria, which is what most rubrics are.
func Levels(levels ...string) []any {
	widened := make([]any, len(levels))
	for i, level := range levels {
		widened[i] = level
	}

	return widened
}

// ValidateQuestions rejects a request the API would reject, before it is sent.
func ValidateQuestions(questions map[string]Question) error {
	if len(questions) == 0 {
		return &ValidationError{Message: "at least one question is required"}
	}

	// Sorted so the reported offender does not depend on map iteration order.
	for _, name := range slices.Sorted(maps.Keys(questions)) {
		if err := questions[name].validate(name); err != nil {
			return err
		}
	}

	return nil
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
