// Package jq compiles and runs the jq expressions that --map and --id take, through gojq. Time
// functions read the real clock, and import and include are refused.
package jq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/itchyny/gojq"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

var (
	// ErrNoValue means the expression yielded nothing for the value it ran on.
	ErrNoValue = errors.New("yields no value")
	// ErrManyValues means the expression yielded more than one value.
	ErrManyValues = errors.New("yields more than one value")
	// ErrRun means the expression failed while it ran, and wraps the reason gojq gave.
	ErrRun = errors.New("fails")
	// ErrTooDeep means a value nests deeper than limits.MaxMapDepth.
	ErrTooDeep = fmt.Errorf("result nests deeper than %d levels", limits.MaxMapDepth)
	// ErrTooLarge means a value encodes to more bytes than the limit Marshal was given.
	ErrTooLarge = errors.New("result encodes to more than the limit")
	// ErrNotID means a value is neither a string nor a finite number, so it cannot name a record.
	ErrNotID = errors.New("id must be a string or a finite number")
	// ErrIDTooLong means a string id is longer than limits.MaxIDBytes.
	ErrIDTooLong = fmt.Errorf("id is longer than %d bytes", limits.MaxIDBytes)
	// ErrIDOutOfRange means a number id writes out longer than limits.MaxIDBytes, or a fraction
	// lies outside what a float64 holds.
	ErrIDOutOfRange = errors.New("id is out of range")
)

const shownNumberBytes = 64

// Compile parses and compiles a jq expression. A syntax error names the column it was found at.
func Compile(source string) (*Expr, error) {
	query, err := gojq.Parse(source)
	if err != nil {
		return nil, parseError(source, err)
	}

	code, err := gojq.Compile(query)
	if err != nil {
		return nil, err
	}

	return &Expr{code: code}, nil
}

// Expr is a compiled jq expression. It is safe to run from many goroutines at once.
type Expr struct {
	code *gojq.Code
}

// One runs the expression on value until ctx ends and demands exactly one result. A recursive builtin
// can still overflow on a value the expression nested millions deep, as in jq.
func (e *Expr) One(ctx context.Context, value any) (any, error) {
	results := e.code.RunWithContext(ctx, value)

	first, ok := results.Next()
	if !ok {
		return nil, ErrNoValue
	}

	if err := runError(ctx, first); err != nil {
		return nil, err
	}

	second, more := results.Next()
	if !more {
		return first, nil
	}

	if err := runError(ctx, second); err != nil {
		return nil, err
	}

	return nil, ErrManyValues
}

// Marshal encodes value as compact JSON with its keys sorted, where jq keeps insertion order. Its
// caps apply after the run, so a runaway expression spends memory and time before they refuse it.
func Marshal(value any, limit int) ([]byte, error) {
	if err := checkBounds(value, limit); err != nil {
		return nil, err
	}

	encoded, err := gojq.Marshal(value)
	if err != nil {
		return nil, err
	}

	if len(encoded) > limit {
		return nil, tooLarge(limit)
	}

	return encoded, nil
}

// ID reduces a value to the string or json.Number that names a record. An integer is written out in
// full and a fraction in its shortest form, so 7, 7.0 and 7e0 name the same record.
func ID(value any) (any, error) {
	switch v := value.(type) {
	case string:
		if len(v) > limits.MaxIDBytes {
			return nil, ErrIDTooLong
		}

		return v, nil
	case int:
		return json.Number(strconv.Itoa(v)), nil
	case *big.Int:
		return bigID(v)
	case float64:
		return floatID(v)
	case json.Number:
		return numberID(v)
	case nil:
		return nil, fmt.Errorf("%w, got null", ErrNotID)
	case bool:
		return nil, fmt.Errorf("%w, got boolean", ErrNotID)
	case []any:
		return nil, fmt.Errorf("%w, got array", ErrNotID)
	default:
		return nil, fmt.Errorf("%w, got object", ErrNotID)
	}
}

func bigID(number *big.Int) (any, error) {
	// Four bits a digit undercounts the digits, so a number past this has more than the limit and
	// is refused before its digits are written out.
	if number.BitLen() > 4*(limits.MaxIDBytes+1) {
		return nil, tooLongNumber("")
	}

	text := number.String()
	if len(text) > limits.MaxIDBytes {
		return nil, tooLongNumber("")
	}

	return json.Number(text), nil
}

func numberID(number json.Number) (any, error) {
	// An integer is written from the literal's own digits, so one longer than a float64 holds keeps
	// every digit. Its length is known from the exponent before any digit is written. A fraction
	// goes through a float64, as it does in jq.
	text := number.String()

	literal, ok := parseDecimal(text)
	if !ok {
		return nil, fmt.Errorf("%w, got %s", ErrNotID, text)
	}

	if literal.exponentOverflows {
		return nil, outOfRange(text)
	}

	if literal.isInteger() {
		if literal.integerBytes() > limits.MaxIDBytes {
			return nil, tooLongNumber(text)
		}

		return json.Number(literal.integerText()), nil
	}

	approx, err := strconv.ParseFloat(literal.normalized(), 64)
	if err != nil || approx == 0 {
		return nil, outOfRange(text)
	}

	return floatID(approx)
}

func parseDecimal(text string) (decimal, bool) {
	var literal decimal

	rest, negative := strings.CutPrefix(text, "-")
	literal.negative = negative

	mantissa, exponentText, hasExponent := strings.Cut(strings.ToLower(rest), "e")
	whole, fraction, _ := strings.Cut(mantissa, ".")

	if whole == "" || !allDigits(whole) || !allDigits(fraction) {
		return decimal{}, false
	}

	var exponent int64

	if hasExponent {
		digits := exponentText
		if strings.HasPrefix(digits, "+") || strings.HasPrefix(digits, "-") {
			digits = digits[1:]
		}

		if digits == "" || !allDigits(digits) {
			return decimal{}, false
		}

		parsed, err := strconv.ParseInt(exponentText, 10, 32)
		exponent = parsed
		literal.exponentOverflows = err != nil
	}

	significant := whole + fraction
	trimmed := strings.TrimLeft(significant, "0")
	literal.digits = strings.TrimRight(trimmed, "0")

	if literal.digits == "" {
		literal.exponentOverflows = false
		exponent = 0
	}

	literal.point = int64(len(whole)-(len(significant)-len(trimmed))) + exponent

	return literal, true
}

type decimal struct {
	negative          bool
	digits            string
	point             int64
	exponentOverflows bool
}

func (d decimal) isInteger() bool {
	return d.digits == "" || int64(len(d.digits)) <= d.point
}

func (d decimal) integerBytes() int64 {
	if d.digits == "" {
		return 1
	}

	if d.negative {
		return d.point + 1
	}

	return d.point
}

func (d decimal) integerText() string {
	if d.digits == "" {
		return "0"
	}

	sign := ""
	if d.negative {
		sign = "-"
	}

	return sign + d.digits + strings.Repeat("0", int(d.point)-len(d.digits))
}

func (d decimal) normalized() string {
	// ParseFloat keeps 800 digits on its slow path and counts the point from the digits it kept,
	// so a literal with more digits ahead of its point or exponent comes back at the wrong
	// magnitude. A point placed ahead of every digit is counted before any are dropped.
	sign := ""
	if d.negative {
		sign = "-"
	}

	return sign + "0." + d.digits + "e" + strconv.FormatInt(d.point, 10)
}

func allDigits(text string) bool {
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func floatID(f float64) (any, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, fmt.Errorf("%w, got %v", ErrNotID, f)
	}

	if f == 0 {
		return json.Number("0"), nil
	}

	if f == math.Trunc(f) {
		return json.Number(strconv.FormatFloat(f, 'f', -1, 64)), nil
	}

	return json.Number(shortestFraction(f)), nil
}

func shortestFraction(f float64) string {
	// The form jq itself writes a fraction in, which is where a caller will have seen it.
	format := byte('f')
	if math.Abs(f) < 1e-6 {
		format = 'e'
	}

	text := strconv.FormatFloat(f, format, -1, 64)

	mantissa, exponent, found := strings.Cut(text, "e")
	if !found {
		return text
	}

	return mantissa + "e" + exponent[:1] + strings.TrimLeft(exponent[1:], "0")
}

func tooLongNumber(text string) error {
	if text == "" || len(text) > shownNumberBytes {
		return fmt.Errorf("%w, it writes out to more than %d bytes", ErrIDOutOfRange, limits.MaxIDBytes)
	}

	return fmt.Errorf("%w, got %s, which writes out to more than %d bytes",
		ErrIDOutOfRange, text, limits.MaxIDBytes)
}

func outOfRange(text string) error {
	if len(text) > shownNumberBytes {
		return ErrIDOutOfRange
	}

	return fmt.Errorf("%w, got %s", ErrIDOutOfRange, text)
}

func checkBounds(value any, limit int) error {
	// A stack of its own rather than recursion, since the gojq encoder recurses and a value deep
	// enough overflows the goroutine stack, which Go cannot recover from. Counting the fewest bytes
	// each value encodes to refuses a value too large to send before anything copies it.
	pending := [][]any{{value}}
	size := 0

	for len(pending) > 0 {
		siblings := pending[len(pending)-1]
		if len(siblings) == 0 {
			pending = pending[:len(pending)-1]

			continue
		}

		current := siblings[0]
		pending[len(pending)-1] = siblings[1:]

		size += leastBytes(current)
		if size > limit {
			return tooLarge(limit)
		}

		children := childrenOf(current)
		if children == nil {
			continue
		}

		if len(pending) > limits.MaxMapDepth {
			return ErrTooDeep
		}

		pending = append(pending, children)
	}

	return nil
}

func leastBytes(value any) int {
	switch v := value.(type) {
	case string:
		return len(v) + len(`""`)
	case nil:
		return len("null")
	case bool:
		if v {
			return len("true")
		}

		return len("false")
	case []any:
		return len("[]") + max(len(v)-1, 0)
	case map[string]any:
		size := len("{}") + max(len(v)-1, 0)
		for key := range v {
			size += len(key) + len(`"":`)
		}

		return size
	default:
		return 1
	}
}

func childrenOf(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case map[string]any:
		values := make([]any, 0, len(v))
		for _, child := range v {
			values = append(values, child)
		}

		return values
	default:
		return nil
	}
}

func tooLarge(limit int) error {
	return fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
}

func runError(ctx context.Context, result any) error {
	err, failed := result.(error)
	if !failed {
		return nil
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	return fmt.Errorf("%w: %w", ErrRun, err)
}

func parseError(source string, err error) error {
	var syntax *gojq.ParseError
	if !errors.As(err, &syntax) {
		return err
	}

	start := max(syntax.Offset-len(syntax.Token), 0)
	column := utf8.RuneCountInString(source[:min(start, len(source))]) + 1

	return fmt.Errorf("%w at column %d", err, column)
}
