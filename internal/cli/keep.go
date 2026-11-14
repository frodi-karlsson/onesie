package cli

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/wording"
)

type keeper struct {
	out      io.Writer
	errOut   io.Writer
	verdicts verdictSet
	mode     input.Mode
	header   func() []string
	comma    func() rune
	seen     []string
	headed   bool
	table    *csv.Writer
	causes   []string
	more     int
}

func (k *keeper) write(l line, verdict output.Verdict) error {
	if k.seen == nil {
		k.seen = l.input.Header
	}

	printable := k.printable(l.input)
	if verdict == output.Error {
		// A kept error with no text to print is named whatever the cap, so it is never lost.
		k.note(l, k.verdicts.has(output.Error) && !printable)
	}

	if !k.verdicts.has(verdict) || !printable {
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

func (k *keeper) note(l line, always bool) {
	if len(k.causes) >= maxCauses && !always {
		k.more++

		return
	}

	message := l.record.Failure.Message
	if prefix := fmt.Sprintf("line %d: ", l.input.Line); l.input.Line > 0 && !strings.HasPrefix(message, prefix) {
		message = prefix + message
	}

	k.causes = append(k.causes, message)
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
	// A tsv field holds no tab or line break, since the reader splits on them, so it needs no quoting.
	if k.mode == input.TSV {
		_, err := io.WriteString(k.out, strings.Join(fields, "\t")+"\n")

		return err
	}

	if k.table == nil {
		k.table = csv.NewWriter(k.out)
		if comma := k.comma(); comma != 0 {
			k.table.Comma = comma
		}
	}

	if err := k.table.Write(fields); err != nil {
		return err
	}

	// Flushed per row, so a reader such as head sees each kept row as it is decided.
	k.table.Flush()

	return k.table.Error()
}

func (k *keeper) finish(result engine.Result, reported bool) error {
	if k.mode.Delimited() && !result.Broken {
		if err := k.writeHeader(); err != nil && !engine.BrokenPipe(err) {
			return err
		}
	}

	// The record that ended the run is already named by the error onesie reports for it.
	if result.Aborted && reported {
		k.drop(text(result.Cause))
	}

	for _, message := range k.causes {
		if err := output.Notice(k.errOut, message); err != nil {
			return err
		}
	}

	if k.more == 0 {
		return nil
	}

	return output.Notice(k.errOut, "and "+wording.Plural(k.more, "more error", "more errors"))
}

func (k *keeper) drop(message string) {
	if k.more > 0 {
		k.more--

		return
	}

	for i := len(k.causes) - 1; i >= 0; i-- {
		if strings.HasSuffix(k.causes[i], message) {
			k.causes = append(k.causes[:i], k.causes[i+1:]...)

			return
		}
	}
}
