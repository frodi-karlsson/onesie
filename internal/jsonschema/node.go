// Package jsonschema holds the JSON Schema nodes onesie builds its schemas from, with properties
// kept in the order they are declared.
package jsonschema

import (
	"bytes"
	"encoding/json"
)

// Node is one JSON Schema object. Every field left zero is left out.
type Node struct {
	Schema               string     `json:"$schema,omitempty"`
	Title                string     `json:"title,omitempty"`
	Comment              string     `json:"$comment,omitempty"`
	Description          string     `json:"description,omitempty"`
	Ref                  string     `json:"$ref,omitempty"`
	Type                 any        `json:"type,omitempty"`
	Enum                 []string   `json:"enum,omitempty"`
	Const                any        `json:"const,omitempty"`
	Pattern              string     `json:"pattern,omitempty"`
	PatternErrorMessage  string     `json:"patternErrorMessage,omitempty"`
	Examples             []any      `json:"examples,omitempty"`
	Minimum              *float64   `json:"minimum,omitempty"`
	Maximum              *float64   `json:"maximum,omitempty"`
	Required             []string   `json:"required,omitempty"`
	PropertyNames        *Node      `json:"propertyNames,omitempty"`
	Properties           Properties `json:"properties,omitempty"`
	AdditionalProperties any        `json:"additionalProperties,omitempty"`
	MinProperties        *int       `json:"minProperties,omitempty"`
	MaxProperties        *int       `json:"maxProperties,omitempty"`
	Dependencies         Properties `json:"dependencies,omitempty"`
	Items                *Node      `json:"items,omitempty"`
	UniqueItems          bool       `json:"uniqueItems,omitempty"`
	MinItems             *int       `json:"minItems,omitempty"`
	MaxItems             *int       `json:"maxItems,omitempty"`
	AllOf                []any      `json:"allOf,omitempty"`
	AnyOf                []any      `json:"anyOf,omitempty"`
	OneOf                []any      `json:"oneOf,omitempty"`
	Not                  *Node      `json:"not,omitempty"`
	ErrorMessage         string     `json:"errorMessage,omitempty"`
	If                   *Node      `json:"if,omitempty"`
	Then                 any        `json:"then,omitempty"`
	Else                 any        `json:"else,omitempty"`
	Definitions          Properties `json:"definitions,omitempty"`
}

// Properties is a JSON object of named schemas, written in its slice order.
type Properties []Property

// Property is one named schema of Properties.
type Property struct {
	Name   string
	Schema any
}

// MarshalJSON writes the properties as one object, in order.
func (p Properties) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteByte('{')

	for i, item := range p {
		if i > 0 {
			buf.WriteByte(',')
		}

		name, err := json.Marshal(item.Name)
		if err != nil {
			return nil, err
		}

		value, err := json.Marshal(item.Schema)
		if err != nil {
			return nil, err
		}

		buf.Write(name)
		buf.WriteByte(':')
		buf.Write(value)
	}

	buf.WriteByte('}')

	return buf.Bytes(), nil
}
