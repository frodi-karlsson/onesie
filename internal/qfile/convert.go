package qfile

import (
	"encoding/json"
	"fmt"

	"github.com/goccy/go-yaml"
)

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

// MarshalOrdered encodes a decoded tree as JSON, keeping every mapping in its written order. Use it
// for the wire, where a body is replayed as given, and plain for a Go consumer.
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
	// Only a mapping or a sequence is wrapped, since order is a property of those alone. A scalar
	// stays the Go value it was, so a file's plain string description has the same type --desc
	// produces.
	switch value.(type) {
	case yaml.MapSlice, []any:
		encoded, err := MarshalOrdered(value)
		if err != nil {
			return nil, err
		}

		return json.RawMessage(encoded), nil
	default:
		// A scalar is checked rather than returned on trust, so a value encoding/json refuses,
		// .nan and .inf among them, is reported here against the key that carries it rather than
		// as a bare marshal failure once the request is built.
		if _, err := json.Marshal(value); err != nil {
			return nil, err
		}

		return value, nil
	}
}
