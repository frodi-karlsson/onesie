// Package jq compiles and runs the jq expressions that --map and --id take, through gojq.
package jq

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/itchyny/gojq"
)

var (
	// ErrNoValue means the expression yielded nothing for the value it ran on.
	ErrNoValue = errors.New("yields no value")
	// ErrManyValues means the expression yielded more than one value.
	ErrManyValues = errors.New("yields more than one value")
	// ErrRun means the expression failed while it ran, and wraps the reason gojq gave.
	ErrRun = errors.New("fails")
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

// One runs the expression on value and demands exactly one result. A failure wraps ErrNoValue,
// ErrManyValues or ErrRun.
func (e *Expr) One(value any) (any, error) {
	results := e.code.Run(value)

	first, ok := results.Next()
	if !ok {
		return nil, ErrNoValue
	}

	if err := runError(first); err != nil {
		return nil, err
	}

	second, more := results.Next()
	if !more {
		return first, nil
	}

	if err := runError(second); err != nil {
		return nil, err
	}

	return nil, ErrManyValues
}

// Marshal encodes a value an expression yielded as JSON, the way jq prints it.
func Marshal(value any) ([]byte, error) {
	return gojq.Marshal(value)
}

func runError(result any) error {
	err, failed := result.(error)
	if !failed {
		return nil
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
