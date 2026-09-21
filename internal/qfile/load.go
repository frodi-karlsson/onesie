// Package qfile loads a question file or a raw API request body into the ordered question slice the
// planner already consumes.
package qfile

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// DecodeOrdered parses YAML or JSON keeping every mapping as an ordered yaml.MapSlice, so the
// loader can read question order, option order and level order from the file.
func DecodeOrdered(data []byte) (any, error) {
	var raw any

	if err := yaml.UnmarshalWithOptions(data, &raw, yaml.UseOrderedMap()); err != nil {
		return nil, fmt.Errorf("jev: %w", err)
	}

	return raw, nil
}
