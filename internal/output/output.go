// Package output encodes a normalized record in each of the modes the CLI offers. It owns no state
// and writes to the writer it is given.
package output

import (
	"fmt"
	"io"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
)

// The kinds a record's error key names.
const (
	// KindResponse is a 2xx response onesie could not use.
	KindResponse Kind = "response"
	// KindHTTP is a response with a status outside 2xx, apart from an auth one.
	KindHTTP Kind = "http"
	// KindAuth is a response that refused the key or the account behind it.
	KindAuth Kind = "auth"
	// KindTransport is a connection that failed or timed out.
	KindTransport Kind = "transport"
	// KindInput is a record that never became a request.
	KindInput Kind = "input"
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
	case Markdown:
		return WriteMarkdown(w, rec, MarkdownOptions{})
	case CSV, TSV:
		ids := make([]string, 0, len(rec.Answers))

		var boxes []string

		for _, named := range rec.Answers {
			ids = append(ids, named.ID)

			if named.Box {
				boxes = append(boxes, named.ID)
			}
		}

		return NewDelimited(w, mode, DelimitedOptions{IDs: ids, Boxes: boxes, ID: rec.ID != nil, Header: true}).
			Write(rec, nil, nil)
	default:
		return writeJSON(w, rec)
	}
}

// ParseMode maps the -o flag onto a Mode. tty, streaming and merge decide what auto resolves to.
func ParseMode(name string, tty, streaming, merge bool) (Mode, error) {
	switch name {
	case "json":
		return JSON, nil
	case "values":
		return Values, nil
	case "table":
		return Table, nil
	case "raw":
		return Raw, nil
	case "csv":
		return CSV, nil
	case "tsv":
		return TSV, nil
	case "markdown", "md":
		return Markdown, nil
	case "", "auto":
		if tty && !streaming && !merge {
			return Table, nil
		}

		return JSON, nil
	default:
		return JSON, fmt.Errorf(
			"-o takes auto, json, values, table, raw, csv, tsv or markdown, got '%s'", name)
	}
}

// Mode is the form Write prints a record in. ParseMode resolves -o auto from the terminal.
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
	// CSV is one comma separated row per record, under a header row.
	CSV
	// TSV is CSV with tabs.
	TSV
	// Markdown is a GitHub flavoured table for a PR comment or a job summary.
	Markdown
)

// String names the mode as -o spells it.
func (m Mode) String() string {
	switch m {
	case JSON:
		return "json"
	case Values:
		return "values"
	case Table:
		return "table"
	case Raw:
		return "raw"
	case CSV:
		return "csv"
	case TSV:
		return "tsv"
	case Markdown:
		return "markdown"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// Record is one input's worth of output, with the answers in question order.
type Record struct {
	// ID names the record under --id, as a string or a json.Number. Nil leaves the id out.
	ID any
	// Line is the input line number under -i lines, counting from one. Zero leaves it out.
	Line    int
	Model   string
	Usage   *onesie.Usage
	Answers []Named
	Failure *Failure

	// AssertFailed is the reserved assert key. It is written as false and only when the record was
	// judged a no, so a record with neither key passed.
	AssertFailed bool
	// Abstained means the assertion did not hold and the abstain expression did. It is written as
	// an abstain key set to true, and a record carries at most one of the two keys.
	Abstained bool
}

// Named pairs a question id with its normalized answer. Answer is nil when the request failed and
// the question had no fallback.
type Named struct {
	ID     string
	Answer *answer.Answer
	// Box marks a box question, which a failed answer cannot show and the csv columns need.
	Box bool
}

// Failure is the reserved error key. Status is nil for a transport or input failure, where no HTTP
// status was ever received.
type Failure struct {
	Kind    Kind   `json:"kind"`
	Status  *int   `json:"status"`
	Message string `json:"message"`
}

// Kind names what failed, so a consumer can tell a retry worth making from a record to fix.
type Kind string
