// Package qfile loads a question file or a raw API request body into the ordered question slice the
// planner already consumes.
package qfile

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

// Load reads a question file or a raw API request body. A top level questions key selects the
// body form.
func Load(data []byte) (*File, error) {
	raw, err := DecodeOrdered(data)
	if err != nil {
		return nil, err
	}

	top, ok := mapping(raw)
	if !ok {
		return nil, errors.New(
			"jev: a question file is a mapping of question id to definition")
	}

	if _, found := lookup(top, "assert"); found {
		return nil, errors.New("jev: a top level 'assert' key is not available yet")
	}

	if _, isBody := lookup(top, "questions"); isBody {
		return loadBody(top)
	}

	return loadQuestions(top)
}

// File is a loaded question file or request body. Questions are in file order.
type File struct {
	Questions []plan.Question

	// IsBody is true when the file was a raw API request body rather than a question file.
	IsBody bool
	// Model is the body's model, empty for a question file.
	Model string
	// State is the body's state. HasState distinguishes an absent state from a null one.
	State    any
	HasState bool
}

// DecodeOrdered parses YAML or JSON keeping every mapping as an ordered yaml.MapSlice, so the
// loader can read question order, option order and level order from the file.
func DecodeOrdered(data []byte) (any, error) {
	var raw any

	if err := yaml.UnmarshalWithOptions(data, &raw, yaml.UseOrderedMap()); err != nil {
		return nil, fmt.Errorf("jev: %w", err)
	}

	return raw, nil
}

func lookup(items yaml.MapSlice, name string) (any, bool) {
	for _, item := range items {
		if fmt.Sprintf("%v", item.Key) == name {
			return item.Value, true
		}
	}

	return nil, false
}

func mapping(value any) (yaml.MapSlice, bool) {
	items, ok := value.(yaml.MapSlice)

	return items, ok
}
