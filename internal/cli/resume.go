package cli

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func resumeLedger(
	ctx context.Context,
	answers *outFile,
	flags *runFlags,
	namer *jq.Expr,
	mode output.Mode,
) (*ledger, error) {
	if answers == nil || !answers.resume || namer == nil {
		return nil, nil
	}

	return readLedger(ctx, answers, answersFormat{
		mode: mode, merge: merging(flags), mergeKey: mergeKey(flags), namer: namer,
	})
}

func readLedger(ctx context.Context, answers *outFile, format answersFormat) (book *ledger, err error) {
	book = &ledger{answered: map[[sha256.Size]byte]span{}}

	file, err := answers.open(answers.path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return book, nil
	}

	if err != nil {
		return nil, fmt.Errorf("onesie: reading %s to resume: %w", answers.path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	// Only up to the last complete line, since the partial one after it is trimmed before anything
	// is appended.
	readErr := format.eachAnswer(ctx, io.LimitReader(file, answers.keep), book.note)
	if readErr != nil {
		return nil, fmt.Errorf("onesie: reading %s to resume: %w", answers.path, readErr)
	}

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	return book, nil
}

type answersFormat struct {
	mode     output.Mode
	merge    bool
	mergeKey string
	namer    *jq.Expr
}

func (f answersFormat) eachAnswer(ctx context.Context, r io.Reader, note func(id string, at span)) error {
	if f.mode != output.CSV && f.mode != output.TSV {
		return eachLine(r, func(at span, text []byte) bool {
			if id, ok := f.lineAnswer(ctx, text); ok {
				note(id, at)
			}

			return true
		})
	}

	var columns []string

	return eachRow(r, f.mode == output.CSV, func(at span, cells []string) bool {
		if columns == nil {
			columns = cells

			return true
		}

		if id, ok := f.rowAnswer(ctx, columns, cells); ok {
			note(id, at)
		}

		return true
	})
}

func (f answersFormat) lineAnswer(ctx context.Context, text []byte) (string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}

	object, isObject := value.(map[string]any)
	if !isObject {
		return "", false
	}

	// Only an answered line is noted. A failed line answers nothing, and under --merge a
	// duplicate's error line carries the same fields as the record it repeats.
	if !f.merge {
		if _, failed := object["error"]; failed {
			return "", false
		}

		return answerID(object["id"])
	}

	folded, isObject := object[f.mergeKey].(map[string]any)
	if !isObject {
		return "", false
	}

	if _, failed := folded["error"]; failed {
		return "", false
	}

	return f.named(ctx, value)
}

func (f answersFormat) rowAnswer(ctx context.Context, columns, cells []string) (string, bool) {
	row := make(map[string]any, len(columns))

	for i, name := range columns {
		if i < len(cells) {
			row[name] = cells[i]
		}
	}

	if failure, ok := row["error"].(string); ok && failure != "" {
		return "", false
	}

	if !f.merge {
		return answerID(row["id"])
	}

	return f.named(ctx, row)
}

func (f answersFormat) named(ctx context.Context, value any) (string, bool) {
	id, err := f.namer.One(ctx, value)
	if err != nil {
		return "", false
	}

	return answerID(id)
}

func answerID(value any) (string, bool) {
	id, err := jq.ID(value)
	if err != nil {
		return "", false
	}

	return idText(id), true
}

type span struct {
	start int64
	end   int64
}

type ledger struct {
	mu       sync.Mutex
	answered map[[sha256.Size]byte]span
	lines    []span
	pending  int
	skipped  int
	ended    bool
}

func (l *ledger) note(id string, at span) {
	// The newest line wins, since a resume appends after what the file held before.
	l.answered[sha256.Sum256([]byte(id))] = at
}

func (l *ledger) admit(rec *namedRecord) (answered bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec.slot = len(l.lines)

	if rec.id != nil {
		key := sha256.Sum256([]byte(idText(rec.id)))
		if at, found := l.answered[key]; found {
			delete(l.answered, key)
			l.lines = append(l.lines, at)
			l.skipped++

			return true
		}
	}

	l.lines = append(l.lines, span{})
	l.pending++

	return false
}

func (l *ledger) wrote(slot int, at span) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lines[slot] = at
	l.pending--
}

func (l *ledger) end() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.ended = true
}

func (l *ledger) complete() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.ended && l.pending == 0
}

func (l *ledger) order(prune bool) []span {
	l.mu.Lock()
	defer l.mu.Unlock()

	lines := slices.Clone(l.lines)
	if prune {
		return lines
	}

	unasked := slices.SortedFunc(maps.Values(l.answered), func(a, b span) int {
		return cmp.Compare(a.start, b.start)
	})

	return append(lines, unasked...)
}

func (l *ledger) skips() int {
	if l == nil {
		return 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.skipped
}

func eachLine(r io.Reader, visit func(at span, text []byte) bool) error {
	reader := bufio.NewReader(r)

	var offset int64

	for {
		text, err := reader.ReadBytes('\n')
		at := span{start: offset, end: offset + int64(len(text))}
		offset = at.end

		if bytes.HasSuffix(text, []byte("\n")) && !visit(at, text) {
			return nil
		}

		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}
	}
}

func eachRow(r io.Reader, quoted bool, visit func(at span, cells []string) bool) error {
	// TSV has no quoting, so a row is a line.
	if !quoted {
		return eachLine(r, func(at span, text []byte) bool {
			return visit(at, strings.Split(strings.TrimSuffix(string(text), "\n"), "\t"))
		})
	}

	reader := csv.NewReader(bufio.NewReader(r))
	reader.FieldsPerRecord = -1

	var start int64

	for {
		cells, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return err
		}

		end := reader.InputOffset()
		if !visit(span{start: start, end: end}, cells) {
			return nil
		}

		start = end
	}
}

func headerLength(r io.Reader, quoted bool) (int64, error) {
	var length int64

	err := eachRow(r, quoted, func(at span, _ []string) bool {
		length = at.end

		return false
	})

	return length, err
}
