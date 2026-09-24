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
)

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

// ID reduces a value to the string or json.Number that names a record. A number takes its shortest
// form, so 7 and 7.0 name the same record.
func ID(value any) (any, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case int:
		return json.Number(strconv.Itoa(v)), nil
	case *big.Int:
		return json.Number(v.String()), nil
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

func numberID(number json.Number) (any, error) {
	// Exact rather than through a float, so an integer id longer than a float64 holds keeps every
	// digit whether or not it was written with a fraction.
	exact, ok := new(big.Rat).SetString(number.String())
	if !ok {
		return nil, fmt.Errorf("%w, got %s", ErrNotID, number)
	}

	if exact.IsInt() {
		return json.Number(exact.Num().String()), nil
	}

	approx, _ := exact.Float64()

	return floatID(approx)
}

func floatID(f float64) (any, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return nil, fmt.Errorf("%w, got %v", ErrNotID, f)
	}

	if f == 0 {
		return json.Number("0"), nil
	}

	return json.Number(shortestFloat(f)), nil
}

func shortestFloat(f float64) string {
	// The form jq itself writes a number in, which is where a caller will have seen it.
	format := byte('f')
	if magnitude := math.Abs(f); magnitude < 1e-6 || magnitude >= 1e21 {
		format = 'e'
	}

	text := strconv.FormatFloat(f, format, -1, 64)

	mantissa, exponent, found := strings.Cut(text, "e")
	if !found {
		return text
	}

	return mantissa + "e" + exponent[:1] + strings.TrimLeft(exponent[1:], "0")
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
