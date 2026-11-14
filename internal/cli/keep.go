package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
)

type keeper struct {
	out      io.Writer
	verdicts verdictSet
	mode     input.Mode
	header   func() []string
	comma    func() rune
	causes   *causeList
	seen     []string
	headed   bool
	rows     *output.FieldWriter
}

func newKeeper(out, errOut io.Writer, verdicts, stops verdictSet, mode input.Mode, source *naming) *keeper {
	kept := &keeper{out: out, verdicts: verdicts, mode: mode, header: source.header, comma: source.comma}

	// Under --stop-on=error onesie reports the one error that reaches the keeper, so it names none.
	if !stops.has(output.Error) {
		kept.causes = &causeList{w: errOut}
	}

	return kept
}

func (k *keeper) write(l line) error {
	if k.seen == nil {
		k.seen = l.input.Header
	}

	printable := k.printable(l.input)
	kept := k.verdicts.has(l.verdict)

	if err := k.name(l, kept && !printable); err != nil {
		return err
	}

	if !kept || !printable {
		return nil
	}

	if !k.mode.Delimited() {
		_, err := io.WriteString(k.out, l.input.Raw+"\n")

		return err
	}

	if err := k.writeHeader(); err != nil {
		return err
	}

	return k.writeRow(rowOf(l.input))
}

func (k *keeper) printable(rec input.Record) bool {
	if k.mode.Delimited() {
		return rowOf(rec) != nil
	}

	// A line over max-line-bytes is dropped as it is read, so it holds no text.
	return rec.Raw != ""
}

func rowOf(rec input.Record) []string {
	fields, isRow := rec.State.(map[string]any)
	if !isRow || rec.Header == nil {
		return nil
	}

	row := make([]string, len(rec.Header))
	for i, name := range rec.Header {
		if text, isText := fields[name].(string); isText {
			row[i] = text
		}
	}

	return row
}

func (k *keeper) name(l line, unprinted bool) error {
	// The record that aborts the run is named by the error onesie reports for it, and so is every
	// record that failed the same way before the abort reached them.
	if l.verdict != output.Error || l.aborts || k.causes == nil {
		return nil
	}

	name := fmt.Sprintf("line %d", l.input.Line)
	message := strings.TrimPrefix(l.record.Failure.Message, name+": ")

	// A kept error with no text to print is named whatever the cap, so it is never lost.
	if unprinted {
		return k.causes.always(name, message)
	}

	return k.causes.name(name, message)
}

func (k *keeper) writeHeader() error {
	if k.headed {
		return nil
	}

	names := k.seen
	if names == nil {
		names = k.header()
	}

	if names == nil {
		return nil
	}

	k.headed = true

	return k.writeRow(names)
}

func (k *keeper) writeRow(fields []string) error {
	if k.rows == nil {
		mode := output.CSV
		if k.mode == input.TSV {
			mode = output.TSV
		}

		k.rows = output.NewFieldWriter(k.out, mode, k.comma())
	}

	return k.rows.WriteRow(fields)
}

func (k *keeper) finish(result engine.Result) error {
	if k.mode.Delimited() && !result.Broken {
		if err := k.writeHeader(); err != nil && !engine.BrokenPipe(err) {
			return err
		}
	}

	if k.causes == nil {
		return nil
	}

	return k.causes.finish()
}
