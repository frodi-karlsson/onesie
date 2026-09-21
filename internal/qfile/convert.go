package qfile

import (
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

// plain rewrites a decoded tree so encoding/json renders mappings as objects. A yaml.MapSlice
// marshals to an array of key and value pairs, which is valid JSON and the wrong shape entirely.
func plain(value any) any {
	switch typed := value.(type) {
	case yaml.MapSlice:
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
