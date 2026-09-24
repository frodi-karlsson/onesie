// Package qfile loads a question file or a raw API request body into the ordered question slice the
// planner already consumes.
package qfile

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/frodi-karlsson/onesie/internal/plan"
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
			"onesie: a question file is a mapping of question id to definition")
	}

	if _, isBody := lookup(top, "questions"); isBody {
		return loadBody(top)
	}

	return loadQuestions(top)
}

// File is a loaded question file or request body. Questions are in file order.
type File struct {
	Questions []plan.Question

	// Assert is the file's top level assertion, empty when the file carries none. A request body
	// carries none at all.
	Assert string

	// IsBody is true when the file was a raw API request body rather than a question file.
	IsBody bool
	// Model is the body's model, empty for a question file.
	Model string
	// State is the body's state. HasState distinguishes an absent state from a null one.
	State    any
	HasState bool

	// StateWire is the body's state as ordered JSON, which is what reaches the API. State is the
	// parsed form, for the checks that need a Go value.
	StateWire json.RawMessage
}

// DecodeOrdered parses YAML or JSON keeping every mapping as an ordered yaml.MapSlice, so the
// loader can read question order, option order and level order from the file.
func DecodeOrdered(data []byte) (any, error) {
	// A syntax error is left to the decode below, which reports it with the message this package
	// already owns.
	if parsed, err := parser.ParseBytes(data, 0); err == nil {
		if err := checkSingleDocument(parsed); err != nil {
			return nil, err
		}
	}

	var raw any

	if err := yaml.UnmarshalWithOptions(data, &raw, yaml.UseOrderedMap()); err != nil {
		return nil, fmt.Errorf("onesie: %w", err)
	}

	return raw, nil
}

func checkSingleDocument(parsed *ast.File) error {
	// A single Unmarshal keeps only the first document and drops the rest in silence, so the
	// documents are counted before anything is decoded. Only the parser can tell a separator apart
	// from a --- inside a quoted string or a block scalar, which is why this is not a byte scan.
	// An empty document is not counted, so a leading or a trailing separator still loads.
	documents := 0

	for _, document := range parsed.Docs {
		if document.Body != nil {
			documents++
		}
	}

	if documents > 1 {
		return errors.New(
			"onesie: a question file is one document, found a second after a --- separator")
	}

	return nil
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

func describe(value any) string {
	// Printing a mapping or a sequence with %v gives Go's own formatting, which is neither the
	// YAML nor the JSON the reader wrote, so only a scalar is quoted back at them.
	switch typed := value.(type) {
	case yaml.MapSlice:
		return "a mapping"
	case []any:
		return "a sequence"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("'%v'", typed)
	}
}
