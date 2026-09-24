// Package input resolves where a state comes from and reads it, so the detection rule lives in one
// place rather than being rediscovered at every call site.
package input

import "fmt"

// maxLineBytes caps one input line. bufio's default of 64KiB is too small for a state that is a
// whole document, and an unbounded reader would let one malformed line exhaust memory.
const maxLineBytes = 8 << 20

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
	case "jsonl":
		return JSONL, nil
	case "lines":
		return Lines, nil
	case "request":
		return Request, nil
	default:
		return Text, fmt.Errorf(
			"onesie: -i takes text, json, jsonl, lines or request, got '%s'", name)
	}
}

// Streaming reports whether the mode reads one record per line.
func (m Mode) Streaming() bool {
	return m == JSONL || m == Lines || m == Request
}

// Mode is the input mode.
type Mode int

const (
	// Text sends all of stdin as one JSON string.
	Text Mode = iota
	// JSON parses all of stdin as one JSON value.
	JSON
	// JSONL parses one JSON value per line.
	JSONL
	// Lines sends one text line per record, as a JSON string.
	Lines
	// Request reads one complete API request body per line and forwards it unchanged.
	Request
)
