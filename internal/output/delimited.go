package output

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// ErrColumnTaken reports an input column whose name a question id or a reserved column already
// uses, so a row would carry two values under one heading.
var ErrColumnTaken = errors.New("an input column has the name of an output column")

// NewDelimited builds a writer for the csv and tsv modes.
func NewDelimited(w io.Writer, mode Mode, opts DelimitedOptions) *Delimited {
	if mode == TSV {
		return &Delimited{tabs: w, opts: opts}
	}

	return &Delimited{rows: csv.NewWriter(w), opts: opts}
}

// Delimited writes records as rows under one header row. It is stateful, so one run uses one.
type Delimited struct {
	rows    *csv.Writer
	tabs    io.Writer
	opts    DelimitedOptions
	started bool
}

// DelimitedOptions fixes the columns a Delimited writes.
type DelimitedOptions struct {
	// IDs are the question ids, one column each, in question order.
	IDs []string
	// ID adds an id column ahead of the answers, for a run that names its records with --id.
	ID bool
	// Assert adds an assert column, true, false or abstain per row, when the run carries an
	// assertion.
	Assert bool
	// Header writes the header row before the first row. A resume into a file that has one leaves
	// it out.
	Header bool
}

// Write writes one record. header and fields are the input row it answers under a merge, and nil
// otherwise. The header is fixed by the first call.
func (d *Delimited) Write(rec Record, header []string, fields map[string]any) error {
	if !d.started {
		if err := d.start(header); err != nil {
			return err
		}
	}

	row := make([]string, 0, len(header)+len(d.opts.IDs)+3)

	for _, name := range header {
		row = append(row, cell(fields[name]))
	}

	if d.opts.ID {
		row = append(row, cell(rec.ID))
	}

	for _, id := range d.opts.IDs {
		row = append(row, answerCell(rec, id))
	}

	if d.opts.Assert {
		row = append(row, assertCell(rec))
	}

	failure := ""
	if rec.Failure != nil {
		failure = rec.Failure.Message
	}

	return d.writeRow(append(row, failure))
}

func (d *Delimited) start(header []string) error {
	d.started = true

	var columns []string
	if d.opts.ID {
		columns = append(columns, "id")
	}

	columns = append(columns, d.opts.IDs...)
	if d.opts.Assert {
		columns = append(columns, "assert")
	}

	columns = append(columns, "error")

	for _, name := range header {
		if slices.Contains(columns, name) {
			return fmt.Errorf("%w: '%s'", ErrColumnTaken, name)
		}
	}

	if !d.opts.Header {
		return nil
	}

	return d.writeRow(append(slices.Clone(header), columns...))
}

func (d *Delimited) writeRow(cells []string) error {
	if d.tabs != nil {
		_, err := io.WriteString(d.tabs, strings.Join(tabless(cells), "\t")+"\n")

		return err
	}

	if err := d.rows.Write(cells); err != nil {
		return err
	}

	// Per row rather than at the end, so a stream can be read while it runs.
	d.rows.Flush()

	return d.rows.Error()
}

func tabless(cells []string) []string {
	// TSV has no quoting, so a tab or line break inside a cell would split it.
	clean := make([]string, len(cells))
	for i, value := range cells {
		clean[i] = strings.NewReplacer("\t", " ", "\r\n", " ", "\n", " ", "\r", " ").Replace(value)
	}

	return clean
}

func answerCell(rec Record, id string) string {
	for _, named := range rec.Answers {
		if named.ID == id && named.Answer != nil {
			return cell(scalar(named.Answer))
		}
	}

	return ""
}

func assertCell(rec Record) string {
	// A failed record was never judged, so it claims neither outcome.
	if rec.Failure != nil {
		return ""
	}

	if rec.Abstained {
		return "abstain"
	}

	return strconv.FormatBool(!rec.AssertFailed)
}

func cell(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}

		return string(encoded)
	}
}
