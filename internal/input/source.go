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
	case "jsonl":
		return JSONL, nil
	case "lines":
		return Lines, nil
	case "request":
		return Request, nil
	case "csv":
		return CSV, nil
	case "tsv":
		return TSV, nil
	default:
		return Text, fmt.Errorf(
			"onesie: -i takes text, json, jsonl, lines, csv, tsv or request, got '%s'", name)
	}
}

// Mode is the input mode.
type Mode int

// Streaming reports whether the mode reads one record per line.
func (m Mode) Streaming() bool {
	return m == JSONL || m == Lines || m == Request || m.Delimited()
}

// Delimited reports whether the mode reads rows under a header, as csv and tsv do.
func (m Mode) Delimited() bool {
	return m == CSV || m == TSV
}

// String names the mode as -i spells it.
func (m Mode) String() string {
	switch m {
	case Text:
		return "text"
	case JSON:
		return "json"
	case JSONL:
		return "jsonl"
	case Lines:
		return "lines"
	case Request:
		return "request"
	case CSV:
		return "csv"
	case TSV:
		return "tsv"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

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
	// CSV reads a header row, then one comma separated row per record, sent as an object.
	CSV
	// TSV is CSV with tabs, and a quote is ordinary text.
	TSV
)
