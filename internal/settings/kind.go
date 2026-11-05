package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/frodi-karlsson/onesie/internal/jsonschema"
)

var (
	// ErrEmptyValue is what a kind that needs a value returns for an empty one.
	ErrEmptyValue = errors.New("empty value")

	accountIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
)

// Kind is the type of a setting's value: how it parses, how a question file gives it, its zero
// value, the schema fragment that describes it and the name help shows for it.
type Kind interface {
	Parse(raw string) (any, error)
	FromFile(raw any) (any, error)
	Zero() any
	Schema() jsonschema.Node
	ValueName() string
}

// Bool is a setting that is on or off.
func Bool() Kind {
	return boolKind{}
}

// Text is a setting that takes any text, the empty text included.
func Text() Kind {
	return textKind{}
}

// NonEmptyText is a setting that takes text with something other than spaces in it.
func NonEmptyText() Kind {
	return textKind{empty: refuseBlank}
}

// Separator is a setting that takes any text but the empty one, so a space is a value.
func Separator() Kind {
	return textKind{empty: refuseEmpty}
}

// Int is a setting that takes a whole number from min to max.
func Int(least, most int) Kind {
	return intKind{least: least, most: most}
}

// Probability is a setting that takes a number from 0 to 1.
func Probability() Kind {
	return probabilityKind{}
}

// Enum is a setting that takes one of values, or an alias that stands for one of them.
func Enum(values []string, aliases map[string]string) Kind {
	return enumKind{values: values, aliases: aliases}
}

// Expr is a setting that takes a boolean expression over the record, as --assert takes it. The
// expression is checked where it is compiled, not here.
func Expr() Kind {
	return exprKind{}
}

// Duration is a setting that takes a duration of zero or more, such as 90m.
func Duration() Kind {
	return durationKind{}
}

// Fallback is a setting that takes the answer standing in for one that falls short.
func Fallback() Kind {
	return fallbackKind{}
}

// Endpoint is the setting that takes either an API root or a Cloudflare account id.
func Endpoint() EndpointKind {
	return EndpointKind{}
}

// Options is the setting that takes a pick question's options: comma separated text from a flag, a
// sequence or a mapping from a question file.
func Options() Kind {
	return listKind{definition: "pick"}
}

// Levels is the setting that takes a rate question's levels: comma separated text from a flag, a
// sequence or a mapping from a question file.
func Levels() Kind {
	return listKind{definition: "rate"}
}

// TrueOnly is a switch that a flag turns on or off, and that a question file can only set to true.
func TrueOnly() Kind {
	return trueOnlyKind{}
}

type boolKind struct{}

func (boolKind) Parse(raw string) (any, error) {
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("takes true or false, got '%s'", raw)
	}

	return on, nil
}

func (boolKind) FromFile(raw any) (any, error) {
	on, ok := raw.(bool)
	if !ok {
		return false, errors.New("takes true or false")
	}

	return on, nil
}

func (boolKind) Zero() any {
	return false
}

func (boolKind) Schema() jsonschema.Node {
	return jsonschema.Node{Type: "boolean"}
}

func (boolKind) ValueName() string {
	return ""
}

type textKind struct {
	empty emptyRule
}

type emptyRule int

const (
	allowEmpty emptyRule = iota
	refuseEmpty
	refuseBlank
)

func (k textKind) Parse(raw string) (any, error) {
	if k.empty.refuses(raw) {
		return "", ErrEmptyValue
	}

	return raw, nil
}

func (r emptyRule) refuses(raw string) bool {
	switch r {
	case refuseEmpty:
		return raw == ""
	case refuseBlank:
		return strings.TrimSpace(raw) == ""
	default:
		return false
	}
}

func (k textKind) FromFile(raw any) (any, error) {
	text, ok := raw.(string)
	if !ok {
		return "", errors.New("takes a string")
	}

	return k.Parse(text)
}

func (textKind) Zero() any {
	return ""
}

func (k textKind) Schema() jsonschema.Node {
	if k.empty == refuseBlank {
		return jsonschema.Node{Type: "string", Pattern: `\S`}
	}

	return jsonschema.Node{Type: "string"}
}

func (textKind) ValueName() string {
	return "string"
}

type intKind struct {
	least, most int
}

func (k intKind) Parse(raw string) (any, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("takes a whole number, got '%s'", raw)
	}

	return k.inRange(n)
}

func (k intKind) inRange(n int) (any, error) {
	if n < k.least || n > k.most {
		return 0, fmt.Errorf("takes a whole number from %d to %d, got %d", k.least, k.most, n)
	}

	return n, nil
}

func (k intKind) FromFile(raw any) (any, error) {
	n, ok := fileNumber(raw)
	if !ok || n != math.Trunc(n) || n < math.MinInt32 || n > math.MaxInt32 {
		return 0, errors.New("takes a whole number")
	}

	return k.inRange(int(n))
}

func fileNumber(raw any) (float64, bool) {
	switch n := raw.(type) {
	case json.Number:
		parsed, err := n.Float64()

		return parsed, err == nil
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case uint64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func (intKind) Zero() any {
	return 0
}

func (k intKind) Schema() jsonschema.Node {
	least, most := float64(k.least), float64(k.most)

	return jsonschema.Node{Type: "integer", Minimum: &least, Maximum: &most}
}

func (intKind) ValueName() string {
	return "int"
}

type probabilityKind struct{}

func (k probabilityKind) Parse(raw string) (any, error) {
	p, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0.0, fmt.Errorf("takes a number between 0 and 1, got '%s'", raw)
	}

	return k.inRange(p)
}

func (probabilityKind) inRange(p float64) (any, error) {
	if p < 0 || p > 1 {
		return 0.0, fmt.Errorf("takes a number between 0 and 1, got %v", p)
	}

	return p, nil
}

func (k probabilityKind) FromFile(raw any) (any, error) {
	p, ok := fileNumber(raw)
	if !ok {
		return 0.0, errors.New("takes a number between 0 and 1")
	}

	return k.inRange(p)
}

func (probabilityKind) Zero() any {
	return 0.0
}

func (probabilityKind) Schema() jsonschema.Node {
	return jsonschema.Node{Ref: definitionRef("probability")}
}

func (probabilityKind) ValueName() string {
	return "number"
}

type enumKind struct {
	values  []string
	aliases map[string]string
}

func (k enumKind) Parse(raw string) (any, error) {
	if strings.TrimSpace(raw) == "" {
		return "", ErrEmptyValue
	}

	if canonical, ok := k.aliases[raw]; ok {
		return canonical, nil
	}

	for _, value := range k.values {
		if value == raw {
			return value, nil
		}
	}

	return "", fmt.Errorf("takes %s, got '%s'", inWords(k.values), raw)
}

func inWords(values []string) string {
	if len(values) < 2 {
		return strings.Join(values, "")
	}

	return strings.Join(values[:len(values)-1], ", ") + " or " + values[len(values)-1]
}

func (k enumKind) FromFile(raw any) (any, error) {
	text, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("takes %s", inWords(k.values))
	}

	return k.Parse(text)
}

func (enumKind) Zero() any {
	return ""
}

func (k enumKind) Schema() jsonschema.Node {
	return jsonschema.Node{Type: "string", Enum: k.values}
}

func (enumKind) ValueName() string {
	return "string"
}

type exprKind struct{}

func (exprKind) Parse(raw string) (any, error) {
	return raw, nil
}

func (k exprKind) FromFile(raw any) (any, error) {
	switch value := raw.(type) {
	case string:
		return k.Parse(value)
	case bool:
		return strconv.FormatBool(value), nil
	default:
		return "", errors.New("takes an expression as a string, or true or false")
	}
}

func (exprKind) Zero() any {
	return ""
}

func (exprKind) Schema() jsonschema.Node {
	return jsonschema.Node{Type: []string{"string", "boolean"}}
}

func (exprKind) ValueName() string {
	return "string"
}

type durationKind struct{}

func (durationKind) Parse(raw string) (any, error) {
	if raw == "" {
		return time.Duration(0), ErrEmptyValue
	}

	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return time.Duration(0), fmt.Errorf("takes a duration of zero or more, such as 90m or 72h, got '%s'", raw)
	}

	return d, nil
}

func (k durationKind) FromFile(raw any) (any, error) {
	text, ok := raw.(string)
	if !ok {
		return time.Duration(0), errors.New("takes a duration, such as 90m or 72h")
	}

	return k.Parse(text)
}

func (durationKind) Zero() any {
	return time.Duration(0)
}

func (durationKind) Schema() jsonschema.Node {
	return jsonschema.Node{Type: "string"}
}

func (durationKind) ValueName() string {
	return "duration"
}

type fallbackKind struct{}

func (fallbackKind) Parse(raw string) (any, error) {
	return raw, nil
}

func (fallbackKind) FromFile(raw any) (any, error) {
	switch value := raw.(type) {
	case string:
		return value, nil
	case bool:
		return strconv.FormatBool(value), nil
	default:
		return "", errors.New("takes a string, or true or false")
	}
}

func (fallbackKind) Zero() any {
	return ""
}

func (fallbackKind) Schema() jsonschema.Node {
	return jsonschema.Node{Type: []string{"string", "boolean"}}
}

func (fallbackKind) ValueName() string {
	return "string"
}

type listKind struct {
	definition string
}

func (listKind) Parse(raw string) (any, error) {
	return textKind{}.Parse(raw)
}

func (listKind) FromFile(raw any) (any, error) {
	switch raw.(type) {
	case []any, map[string]any:
		return raw, nil
	default:
		return nil, errors.New("takes a sequence or a mapping")
	}
}

func (listKind) Zero() any {
	return ""
}

func (k listKind) Schema() jsonschema.Node {
	return jsonschema.Node{Ref: definitionRef(k.definition)}
}

func (listKind) ValueName() string {
	return "string"
}

type trueOnlyKind struct {
	boolKind
}

func (trueOnlyKind) FromFile(raw any) (any, error) {
	if on, ok := raw.(bool); !ok || !on {
		return false, errors.New("takes only true")
	}

	return true, nil
}

func (trueOnlyKind) Schema() jsonschema.Node {
	return jsonschema.Node{Const: true}
}

func definitionRef(name string) string {
	return "#/definitions/" + name
}

// EndpointKind is the kind of the endpoint, whose sources each say which half they give.
type EndpointKind struct{}

// Parse reads raw as an API root, the half --base-url gives.
func (k EndpointKind) Parse(raw string) (any, error) {
	return k.ParseHalf(raw, HalfBaseURL)
}

// ParseHalf reads raw as the half of the endpoint its source gives.
func (EndpointKind) ParseHalf(raw string, half EndpointHalf) (any, error) {
	if strings.TrimSpace(raw) == "" {
		return EndpointValue{}, ErrEmptyValue
	}

	switch half {
	case HalfBaseURL:
		return EndpointValue{BaseURL: raw}, nil
	case HalfAccount:
		if !accountIDPattern.MatchString(raw) {
			return EndpointValue{}, fmt.Errorf("takes an account id of 32 hexadecimal characters, got '%s'", raw)
		}

		return EndpointValue{AccountID: raw}, nil
	default:
		return EndpointValue{}, errors.New("is given by a source that names no half of the endpoint")
	}
}

// FromFile reads a question file's value as an API root.
func (k EndpointKind) FromFile(raw any) (any, error) {
	text, ok := raw.(string)
	if !ok {
		return EndpointValue{}, errors.New("takes a string")
	}

	return k.Parse(text)
}

// Zero is the endpoint no source gives.
func (EndpointKind) Zero() any {
	return EndpointValue{}
}

// Schema describes an API root.
func (EndpointKind) Schema() jsonschema.Node {
	return jsonschema.Node{Type: "string"}
}

// ValueName is the name help shows for the value.
func (EndpointKind) ValueName() string {
	return "string"
}

// EndpointValue is where a run sends its requests: an API root, or a Cloudflare account whose
// root the provider builds. At most one of the two is set.
type EndpointValue struct {
	BaseURL   string
	AccountID string
}
