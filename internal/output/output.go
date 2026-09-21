// Package output encodes a normalized record in each of the modes the CLI offers. It owns no state
// and writes to the writer it is given.
package output

import (
	"fmt"
	"io"

	"github.com/frodi-karlsson/jev-cli/internal/answer"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

// Write encodes one record in the requested mode. The table mode is rendered at a default width,
// so a caller that knows the terminal width should call WriteTable directly.
func Write(w io.Writer, mode Mode, rec Record) error {
	switch mode {
	case Values:
		return writeValues(w, rec)
	case Raw:
		return writeRaw(w, rec)
	case Table:
		return WriteTable(w, rec, fallbackWidth)
	default:
		return writeJSON(w, rec)
	}
}

// ParseMode maps the -o flag onto a Mode. tty and streaming decide what auto resolves to.
func ParseMode(name string, tty, streaming bool) (Mode, error) {
	switch name {
	case "json":
		return JSON, nil
	case "values":
		return Values, nil
	case "table":
		return Table, nil
	case "raw":
		return Raw, nil
	case "", "auto":
		if tty && !streaming {
			return Table, nil
		}

		return JSON, nil
	default:
		return JSON, fmt.Errorf(
			"jev: -o takes auto, json, values, table or raw, got '%s'", name)
	}
}

// Mode is the output mode from -o.
type Mode int

const (
	// JSON is the full normalized object, one line per record.
	JSON Mode = iota
	// Values is the id to answer mapping per record.
	Values
	// Table is the human readable form, with probability bars.
	Table
	// Raw is the bare scalar, single question only.
	Raw
)

// Record is one input's worth of output, with the answers in question order.
type Record struct {
	Model   string
	Usage   *jev.Usage
	Answers []Named
	Failure *Failure
}

// Named pairs a question id with its normalized answer. Answer is nil when the request failed and
// the question had no fallback.
type Named struct {
	ID     string
	Answer *answer.Answer
}

// Failure is the reserved error key. Status is nil for a transport or input failure, where no HTTP
// status was ever received.
type Failure struct {
	Kind    string `json:"kind"`
	Status  *int   `json:"status"`
	Message string `json:"message"`
}
