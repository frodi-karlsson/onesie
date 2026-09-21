package qfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/printer"
	"github.com/goccy/go-yaml/token"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

// Write renders questions as a jev question file, preserving order, labels and policy, so the
// result reloads through Load to the same plan.
func Write(questions []plan.Question) ([]byte, error) {
	doc := make(yaml.MapSlice, 0, len(questions))

	for _, question := range questions {
		if question.ID == plan.PositionalID {
			return nil, errors.New(
				"jev: --print-questions needs a named question. Use --ask NAME=QUESTION")
		}

		body, err := writeQuestion(question)
		if err != nil {
			return nil, err
		}

		doc = append(doc, yaml.MapItem{Key: question.ID, Value: body})
	}

	return marshal(doc)
}

func writeQuestion(question plan.Question) (yaml.MapSlice, error) {
	instructions, err := yamlValue(question.Instructions)
	if err != nil {
		return nil, fmt.Errorf(
			"jev: 'ask' in question '%s' cannot be written: %w", question.ID, err)
	}

	if question.Criteria != nil && question.Shape != plan.Noul {
		// Dropping the rubric silently would leave the user believing it reached the API, which is
		// the line the loader already holds against a file carrying both.
		return nil, fmt.Errorf(
			"jev: question '%s' has both 'yes_means' and '%s'. A question is one or the other",
			question.ID, question.Shape)
	}

	body := yaml.MapSlice{{Key: "ask", Value: instructions}}

	switch question.Shape {
	case plan.Pick:
		options, optionsErr := writeOptions(question)
		if optionsErr != nil {
			return nil, optionsErr
		}

		body = append(body, yaml.MapItem{Key: "pick", Value: options})
	case plan.Rate:
		levels, levelsErr := writeLevels(question)
		if levelsErr != nil {
			return nil, levelsErr
		}

		body = append(body, yaml.MapItem{Key: "rate", Value: levels})
	default:
		rubric, rubricErr := writeRubric(question)
		if rubricErr != nil {
			return nil, rubricErr
		}

		body = append(body, rubric...)
	}

	return append(body, writePolicy(question.Policy)...), nil
}

func writeOptions(question plan.Question) (yaml.MapSlice, error) {
	options := make(yaml.MapSlice, 0, len(question.Options))

	for _, option := range question.Options {
		desc, err := yamlValue(option.Desc)
		if err != nil {
			return nil, fmt.Errorf(
				"jev: 'pick' option '%s' in question '%s' cannot be written: %w",
				option.Name, question.ID, err)
		}

		options = append(options, yaml.MapItem{Key: option.Name, Value: desc})
	}

	return options, nil
}

func writeLevels(question plan.Question) ([]any, error) {
	// A sequence of single key mappings for both forms. Section 4 takes the mapping form's order
	// from the parser and names the sequence forms preferred, and a rubric whose order is the
	// answer's meaning should not depend on a parser detail. A body's levels have no names, so the
	// index becomes the label, which is what section 10 requires and what makes the indices used
	// as p keys in section 6.3 explicit.
	levels := make([]any, 0, len(question.Levels))

	for i, level := range question.Levels {
		label := level.Label
		if !question.Labelled {
			label = strconv.Itoa(i)
		}

		desc, err := yamlValue(level.Desc)
		if err != nil {
			return nil, fmt.Errorf(
				"jev: 'rate' level '%s' in question '%s' cannot be written: %w",
				label, question.ID, err)
		}

		levels = append(levels, yaml.MapSlice{{Key: label, Value: desc}})
	}

	return levels, nil
}

func writeRubric(question plan.Question) (yaml.MapSlice, error) {
	if question.Criteria == nil {
		return nil, nil
	}

	yes, err := yamlValue(question.Criteria.Yes)
	if err != nil {
		return nil, fmt.Errorf(
			"jev: 'yes_means' in question '%s' cannot be written: %w", question.ID, err)
	}

	no, err := yamlValue(question.Criteria.No)
	if err != nil {
		return nil, fmt.Errorf(
			"jev: 'no_means' in question '%s' cannot be written: %w", question.ID, err)
	}

	// yes_means and no_means, not yes and no. Under the YAML 1.2 core schema goccy implements a
	// bare yes stays the string "yes" rather than becoming a boolean, so the short spelling is not
	// an alias the loader accepts and checkKeys would reject it.
	return yaml.MapSlice{
		{Key: "yes_means", Value: yes},
		{Key: "no_means", Value: no},
	}, nil
}

func writePolicy(policy plan.Policy) yaml.MapSlice {
	out := yaml.MapSlice{}

	if policy.Threshold != nil {
		out = append(out, yaml.MapItem{Key: "threshold", Value: *policy.Threshold})
	}

	if policy.MinConfidence != nil {
		// Underscore, matching the key readPolicy looks up. The flag is spelled with a dash and
		// the file key is not.
		out = append(out, yaml.MapItem{Key: "min_confidence", Value: *policy.MinConfidence})
	}

	if policy.Fallback != nil {
		out = append(out, yaml.MapItem{Key: "fallback", Value: policy.Fallback.Text})
	}

	return out
}

func yamlValue(value any) (any, error) {
	raw, ok := value.(json.RawMessage)
	if !ok {
		return value, nil
	}

	// Back through the ordered decoder, so a structured description written to a file comes out as
	// a mapping in its original order rather than as the byte sequence goccy renders a
	// json.RawMessage as.
	return DecodeOrdered(raw)
}

func marshal(doc yaml.MapSlice) ([]byte, error) {
	node, err := yaml.ValueToNode(doc)
	if err != nil {
		return nil, err
	}

	ast.Walk(quoter{}, node)

	var out printer.Printer

	return out.PrintNode(node), nil
}

type quoter struct{}

func (q quoter) Visit(node ast.Node) ast.Visitor {
	switch typed := node.(type) {
	case *ast.MappingValueNode:
		if key, ok := requote(typed.Key).(ast.MapKeyNode); ok {
			typed.Key = key
		}

		typed.Value = requote(typed.Value)
	case *ast.SequenceNode:
		for i, item := range typed.Values {
			typed.Values[i] = requote(item)
		}
	}

	return q
}

func requote(node ast.Node) ast.Node {
	text, ok := scalarText(node)
	if !ok || !strings.ContainsAny(text, "\t\r") {
		// Every other scalar is left to goccy, because section 10 means the file to be read and
		// edited and forcing every multi line description onto one quoted line loses the block
		// scalar that makes it readable.
		return node
	}

	// goccy emits a tab as a plain scalar and a carriage return as a block scalar whose breaks are
	// carriage returns, and its own parser drops the first and rewrites the second as a line feed.
	// JSON string escaping is a strict subset of YAML's double quoted escaping, so encoding/json is
	// a correct emitter for the one form that survives.
	encoded, err := json.Marshal(text)
	if err != nil {
		return node
	}

	return ast.String(token.New(string(encoded), string(encoded), node.GetToken().Position))
}

func scalarText(node ast.Node) (string, bool) {
	switch typed := node.(type) {
	case *ast.StringNode:
		return typed.Value, true
	case *ast.LiteralNode:
		return typed.Value.Value, true
	default:
		return "", false
	}
}
