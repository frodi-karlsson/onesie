// Package qfile loads a question file or a raw API request body into the ordered question slice the
// planner already consumes.
package qfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/token"

	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

var plainFloat = regexp.MustCompile(`^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)[eE][-+]?[0-9]+$`)

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
	// AbstainIf is the file's top level abstain expression, empty when the file carries none. A
	// request body carries none at all.
	AbstainIf string

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

// DecodeOrdered parses YAML or JSON keeping every mapping as an ordered yaml.MapSlice and a JSON
// number as its json.Number, so the loader reads order and numbers as the file wrote them.
func DecodeOrdered(data []byte) (any, error) {
	if len(data) > limits.MaxQuestionFileBytes {
		return nil, fmt.Errorf("onesie: a question file is at most %d bytes, and this one is %d",
			limits.MaxQuestionFileBytes, len(data))
	}

	// JSON through encoding/json, since goccy reads a number like 8e13 or one past uint64 as a
	// string, and a request body is meant to reach the API as it was written.
	if json.Valid(data) {
		return decodeJSON(data)
	}

	// A syntax error is left to the decode below, which reports it with the message this package
	// already owns.
	parsed, err := parser.ParseBytes(data, 0)
	if err != nil {
		var raw any

		err = yaml.UnmarshalWithOptions(data, &raw, yaml.UseOrderedMap())

		return nil, fmt.Errorf("onesie: %w", err)
	}

	if err := checkSingleDocument(parsed); err != nil {
		return nil, err
	}

	var raw any

	for _, document := range parsed.Docs {
		if document.Body == nil {
			continue
		}

		// goccy expands an alias as it decodes, so a few hundred bytes of nested aliases would
		// decode to gigabytes.
		refusal := &aliases{}
		ast.Walk(refusal, document.Body)

		if refusal.found {
			return nil, errors.New("onesie: a question file cannot use a YAML alias, since onesie does " +
				"not expand one. Write the value out in full")
		}

		ast.Walk(exponents{}, document.Body)

		if err := yaml.NodeToValue(document.Body, &raw, yaml.UseOrderedMap()); err != nil {
			return nil, fmt.Errorf("onesie: %w", err)
		}
	}

	return raw, nil
}

func decodeJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	return decodeJSONValue(decoder)
}

func decodeJSONValue(decoder *json.Decoder) (any, error) {
	tok, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("onesie: %w", err)
	}

	switch tok {
	case json.Delim('{'):
		items := yaml.MapSlice{}

		for decoder.More() {
			keyTok, keyErr := decoder.Token()
			if keyErr != nil {
				return nil, fmt.Errorf("onesie: %w", keyErr)
			}

			key, isText := keyTok.(string)
			if !isText {
				return nil, fmt.Errorf("onesie: a mapping key must be text, got %v", keyTok)
			}

			if _, repeated := lookup(items, key); repeated {
				return nil, fmt.Errorf("onesie: mapping key %q already defined", key)
			}

			value, valueErr := decodeJSONValue(decoder)
			if valueErr != nil {
				return nil, valueErr
			}

			items = append(items, yaml.MapItem{Key: key, Value: value})
		}

		_, err = decoder.Token()

		return items, err
	case json.Delim('['):
		items := []any{}

		for decoder.More() {
			value, valueErr := decodeJSONValue(decoder)
			if valueErr != nil {
				return nil, valueErr
			}

			items = append(items, value)
		}

		_, err = decoder.Token()

		return items, err
	default:
		return tok, nil
	}
}

type aliases struct {
	found bool
}

func (a *aliases) Visit(node ast.Node) ast.Visitor {
	if _, isAlias := node.(*ast.AliasNode); isAlias {
		a.found = true

		return nil
	}

	return a
}

type exponents struct{}

func (e exponents) Visit(node ast.Node) ast.Visitor {
	// A value only, since a key is read as text whatever it looks like.
	switch typed := node.(type) {
	case *ast.MappingValueNode:
		typed.Value = asFloat(typed.Value)
	case *ast.SequenceNode:
		for i, item := range typed.Values {
			typed.Values[i] = asFloat(item)
		}
	}

	return e
}

func asFloat(node ast.Node) ast.Node {
	// goccy reads a plain scalar like 1e-7 as a string, where YAML 1.2 reads a float. A quoted
	// scalar is a string in both.
	text, isText := node.(*ast.StringNode)
	if !isText || text.Token.Type != token.StringType || !plainFloat.MatchString(text.Value) {
		return node
	}

	value, err := strconv.ParseFloat(text.Value, 64)
	if err != nil {
		return node
	}

	return &ast.FloatNode{BaseNode: &ast.BaseNode{}, Token: text.Token, Value: value}
}

func checkSingleDocument(parsed *ast.File) error {
	// A single Unmarshal keeps only the first document and drops the rest in silence, so documents
	// are counted first. Only the parser can tell a separator from a --- inside a quoted string or
	// a block scalar. An empty document is not counted, so a leading or trailing separator still
	// loads.
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
