// Package input resolves where a state comes from and reads it, so the detection rule lives in one
// place rather than being rediscovered at every call site.
package input

import "fmt"

// Source names where the state came from. It is resolved once, before any read.
type Source int

const (
	// SourceNone means nothing supplied a state.
	SourceNone Source = iota
	// SourceStdin means the state was read from standard input.
	SourceStdin
	// SourceState means the state came from --state.
	SourceState
	// SourceStateFile means the state came from --state-file.
	SourceStateFile
	// SourceBody means a request body carried its own state.
	SourceBody
)

// String names the source as an error message would spell it.
func (s Source) String() string {
	switch s {
	case SourceStdin:
		return "stdin"
	case SourceState:
		return "--state"
	case SourceStateFile:
		return "--state-file"
	case SourceBody:
		return "a request body"
	default:
		return "nothing"
	}
}

// ParseMode maps the -i flag onto a Mode.
func ParseMode(name string) (Mode, error) {
	switch name {
	case "", "text":
		return Text, nil
	case "json":
		return JSON, nil
	case "jsonl", "lines", "request":
		return Text, fmt.Errorf("jev: -i %s is not available yet", name)
	default:
		return Text, fmt.Errorf("jev: -i takes text or json, got '%s'", name)
	}
}

// Mode is the input mode. The streaming modes arrive in a later milestone.
type Mode int

const (
	// Text sends all of stdin as one JSON string.
	Text Mode = iota
	// JSON parses all of stdin as one JSON value.
	JSON
)
