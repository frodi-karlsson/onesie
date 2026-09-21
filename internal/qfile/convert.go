package qfile

import (
	"encoding/json"
	"fmt"

	"github.com/goccy/go-yaml"
)

// Decode parses YAML or JSON and returns a value safe to hand to encoding/json. JSON is a subset of
// YAML, so one parser covers both.
func Decode(data []byte) (any, error) {
	var raw any

	// UseOrderedMap keeps every nesting level as a MapSlice. Without it a nested mapping becomes a
	// Go map, and the rate mapping form in the spec would have a nondeterministic level order.
	if err := yaml.UnmarshalWithOptions(data, &raw, yaml.UseOrderedMap()); err != nil {
		return nil, fmt.Errorf("jev: %w", err)
	}

	return plain(raw), nil
}

func plain(value any) any {
	switch typed := value.(type) {
	case yaml.MapSlice:
		// A yaml.MapSlice marshals to an array of key and value pairs, which is valid JSON and the
		// wrong shape entirely, so it is rewritten into a map here before encoding/json ever sees it.
		out := make(map[string]any, len(typed))
		for _, item := range typed {
			out[fmt.Sprintf("%v", item.Key)] = plain(item.Value)
		}

		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, plain(item))
		}

		return out
	default:
		return value
	}
}

// MarshalOrdered encodes a decoded tree as JSON keeping every mapping in the order it was written.
// plain is the right choice for a value bound for a Go consumer, and this is the right choice for
// one bound for the wire, where a request body is meant to be replayed as it was given.
func MarshalOrdered(value any) ([]byte, error) {
	switch typed := value.(type) {
	case yaml.MapSlice:
		buf := []byte{'{'}

		for i, item := range typed {
			if i > 0 {
				buf = append(buf, ',')
			}

			// Through json.Marshal rather than wrapping in quotes. A key is not character checked
			// anywhere, so one carrying a quote would otherwise produce a body no parser can read.
			key, err := json.Marshal(fmt.Sprintf("%v", item.Key))
			if err != nil {
				return nil, err
			}

			buf = append(buf, key...)
			buf = append(buf, ':')

			encoded, err := MarshalOrdered(item.Value)
			if err != nil {
				return nil, err
			}

			buf = append(buf, encoded...)
		}

		return append(buf, '}'), nil
	case []any:
		buf := []byte{'['}

		for i, item := range typed {
			if i > 0 {
				buf = append(buf, ',')
			}

			encoded, err := MarshalOrdered(item)
			if err != nil {
				return nil, err
			}

			buf = append(buf, encoded...)
		}

		return append(buf, ']'), nil
	default:
		return json.Marshal(value)
	}
}

func wireValue(value any) (any, error) {
	// Only a mapping or a sequence is wrapped. Order is a property of those two and of nothing
	// else, so a scalar stays the Go value it already was. That keeps a file's plain string
	// description the same type as the one --desc produces, which is what lets the two authoring
	// paths compare equal.
	switch value.(type) {
	case yaml.MapSlice, []any:
		encoded, err := MarshalOrdered(value)
		if err != nil {
			return nil, err
		}

		return json.RawMessage(encoded), nil
	default:
		return value, nil
	}
}
