package qfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/printer"
	"github.com/goccy/go-yaml/token"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Write renders questions, an assertion and an abstain expression as a question file, preserving
// order, labels and policy so it reloads through Load to the same plan. An empty expression writes
// no key.
func Write(questions []plan.Question, assertion, abstainIf string) ([]byte, error) {
	doc := make(yaml.MapSlice, 0, len(questions)+2)

	// Both gate keys go above the questions, because each gate reads all of them and every other
	// top level key in the file names one.
	if assertion != "" {
		doc = append(doc, yaml.MapItem{Key: "assert", Value: assertion})
	}

	if abstainIf != "" {
		doc = append(doc, yaml.MapItem{Key: "abstain_if", Value: abstainIf})
	}

	for _, question := range questions {
		if question.ID == plan.PositionalID {
			return nil, errors.New(
				"onesie: --print-questions needs a named question. Use --ask NAME=QUESTION")
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
			"onesie: 'ask' in question '%s' cannot be written: %w", question.ID, err)
	}

	if question.Criteria != nil && question.Shape != plan.Noul {
		// Dropping the rubric silently would leave the user believing it reached the API, which is
		// the line the loader already holds against a file carrying both.
		return nil, fmt.Errorf(
			"onesie: question '%s' has both 'yes_means' and '%s'. A question is one or the other",
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
				"onesie: 'pick' option '%s' in question '%s' cannot be written: %w",
				option.Name, question.ID, err)
		}

		options = append(options, yaml.MapItem{Key: option.Name, Value: desc})
	}

	return options, nil
}

func writeLevels(question plan.Question) ([]any, error) {
	// A sequence of single key mappings for both forms, so a rubric's order never depends on a
	// parser detail, as section 4 prefers. A body's levels have no names, so the index becomes the
	// label, as section 10 requires.
	levels := make([]any, 0, len(question.Levels))

	for i, level := range question.Levels {
		label := level.Label
		if !question.Labelled {
			label = strconv.Itoa(i)
		}

		desc, err := yamlValue(level.Desc)
		if err != nil {
			return nil, fmt.Errorf(
				"onesie: 'rate' level '%s' in question '%s' cannot be written: %w",
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
			"onesie: 'yes_means' in question '%s' cannot be written: %w", question.ID, err)
	}

	no, err := yamlValue(question.Criteria.No)
	if err != nil {
		return nil, fmt.Errorf(
			"onesie: 'no_means' in question '%s' cannot be written: %w", question.ID, err)
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
		return yamlNumbers(value)
	}

	// Back through the ordered decoder, so a structured description written to a file comes out as
	// a mapping in its original order rather than as the byte sequence goccy renders a
	// json.RawMessage as.
	decoded, err := DecodeOrdered(raw)
	if err != nil {
		return nil, err
	}

	return yamlNumbers(decoded)
}

func yamlNumbers(value any) (any, error) {
	switch typed := value.(type) {
	case yaml.MapSlice:
		out := make(yaml.MapSlice, 0, len(typed))

		for _, item := range typed {
			converted, err := yamlNumbers(item.Value)
			if err != nil {
				return nil, err
			}

			out = append(out, yaml.MapItem{Key: item.Key, Value: converted})
		}

		return out, nil
	case []any:
		out := make([]any, 0, len(typed))

		for _, item := range typed {
			converted, err := yamlNumbers(item)
			if err != nil {
				return nil, err
			}

			out = append(out, converted)
		}

		return out, nil
	case json.Number:
		return yamlNumber(typed)
	case float64:
		return wholeNumber(typed), nil
	default:
		return value, nil
	}
}

func wholeNumber(float float64) any {
	// goccy writes a whole float with an exponent, as 8e+13, and reads it back as an integer, so a
	// whole float is written as the integer it is and the file prints the same way twice.
	if float == math.Trunc(float) && float >= math.MinInt64 && float < math.MaxInt64 {
		return int64(float)
	}

	return float
}

func yamlNumber(number json.Number) (any, error) {
	// goccy writes a json.Number past int64 as a quoted string, so each becomes the Go number it
	// names. One that no int64 or float64 holds exactly is refused rather than rounded.
	if integer, err := number.Int64(); err == nil {
		return integer, nil
	}

	float, err := number.Float64()
	if err == nil {
		exact, _ := new(big.Rat).SetString(number.String())
		shortest, _ := new(big.Rat).SetString(strconv.FormatFloat(float, 'g', -1, 64))

		if exact != nil && shortest != nil && exact.Cmp(shortest) == 0 {
			return wholeNumber(float), nil
		}
	}

	return nil, fmt.Errorf("%s has more digits than a question file keeps", number)
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
		// Probed at the column the key is written at, since goccy's block scalars keep or lose the
		// spaces that end their last line depending on how deep they are indented.
		depth := depthAt(typed.Key.GetToken())

		asKey := probe{
			depth: depth, leaf: func(text string) any { return yaml.MapSlice{{Key: text, Value: "v"}} },
			read: func(leaf yaml.MapItem) any { return leaf.Key },
		}
		if key, ok := requote(typed.Key, asKey).(ast.MapKeyNode); ok {
			typed.Key = key
		}

		typed.Value = requote(typed.Value, probe{
			depth: depth, leaf: func(text string) any { return text },
			read: func(leaf yaml.MapItem) any { return leaf.Value },
		})
	case *ast.SequenceNode:
		// goccy writes a sequence's dash at the column of the key that holds it.
		asItem := probe{
			depth: depthAt(typed.Start), leaf: func(text string) any { return []any{text} },
			read: func(leaf yaml.MapItem) any {
				if items, ok := leaf.Value.([]any); ok && len(items) == 1 {
					return items[0]
				}

				return nil
			},
		}

		for i, item := range typed.Values {
			typed.Values[i] = requote(item, asItem)
		}
	}

	return q
}

type probe struct {
	depth int
	leaf  func(text string) any
	read  func(leaf yaml.MapItem) any
}

func depthAt(tok *token.Token) int {
	if tok == nil || tok.Position == nil {
		return 0
	}

	return max(tok.Position.Column-1, 0) / 2
}

func requote(node ast.Node, at probe) ast.Node {
	if float, isFloat := node.(*ast.FloatNode); isFloat {
		pointMantissa(float.Token)

		return node
	}

	text, ok := scalarText(node)
	if !ok || !strings.ContainsFunc(text, needsEscape) && survivesUnquoted(text, at) {
		// Every other scalar is left to goccy, because section 10 means the file to be read and
		// edited and forcing every multi line description onto one quoted line loses the block
		// scalar that makes it readable.
		return node
	}

	quoted, err := doubleQuoted(text)
	if err != nil {
		return node
	}

	return ast.String(token.New(quoted, quoted, node.GetToken().Position))
}

func doubleQuoted(text string) (string, error) {
	// goccy emits a tab as a plain scalar and a carriage return as a block scalar whose breaks are
	// carriage returns, and its own parser drops the first and rewrites the second as a line feed.
	// Any other control character it emits raw, which YAML only allows escaped. JSON string
	// escaping is a subset of YAML's double quoted escaping, so encoding/json is a correct emitter,
	// once it leaves html alone and escapes the delete and C1 controls it writes raw.
	var buf bytes.Buffer

	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(text); err != nil {
		return "", err
	}

	var quoted strings.Builder

	for _, r := range strings.TrimSuffix(buf.String(), "\n") {
		if r >= 0x7f && r <= 0x9f {
			fmt.Fprintf(&quoted, `\u%04x`, r)

			continue
		}

		quoted.WriteRune(r)
	}

	return quoted.String(), nil
}

func pointMantissa(tok *token.Token) {
	// goccy writes 8e13 as 8e+13, and goccy's own parser reads an exponent with no point in its
	// mantissa as a string, so the point keeps the file readable by an older onesie.
	mantissa, exponent, found := strings.Cut(tok.Value, "e")
	if !found || strings.Contains(mantissa, ".") {
		return
	}

	tok.Value = mantissa + ".0e" + exponent
	tok.Origin = tok.Value
}

func needsEscape(r rune) bool {
	return r != '\n' && unicode.IsControl(r)
}

func survivesUnquoted(text string, at probe) bool {
	// goccy leaves some scalars unquoted that the loader then reads as syntax or as a number, such
	// as one starting with a question mark and a space, a key of three dots or a string like 1e-7.
	// Its block scalars lose a lone line feed and, at some depths, the spaces that end the last
	// line. So a scalar keeps goccy's form only when it reads back unchanged where it stands.
	var doc any = yaml.MapSlice{{Key: "k", Value: at.leaf(text)}}
	if leaf, isMapping := at.leaf(text).(yaml.MapSlice); isMapping {
		doc = leaf
	}

	for range at.depth {
		doc = yaml.MapSlice{{Key: "k", Value: doc}}
	}

	node, err := yaml.ValueToNode(doc)
	if err != nil {
		return false
	}

	var out printer.Printer

	read, err := DecodeOrdered(out.PrintNode(node))
	if err != nil {
		return false
	}

	for range at.depth {
		items, isMapping := read.(yaml.MapSlice)
		if !isMapping || len(items) != 1 {
			return false
		}

		read = items[0].Value
	}

	items, isMapping := read.(yaml.MapSlice)

	return isMapping && len(items) == 1 && at.read(items[0]) == text
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
