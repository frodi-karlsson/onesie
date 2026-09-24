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
	book = &ledger{answered: map[ledgerKey]answeredLine{}}

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

func (f answersFormat) eachAnswer(
	ctx context.Context,
	r io.Reader,
	note func(id string, at span, judged verdict),
) error {
	if f.mode != output.CSV && f.mode != output.TSV {
		return eachLine(r, func(at span, text []byte) bool {
			if id, judged, ok := f.lineAnswer(ctx, text); ok {
				note(id, at, judged)
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

		if id, judged, ok := f.rowAnswer(ctx, columns, cells); ok {
			note(id, at, judged)
		}

		return true
	})
}

func (f answersFormat) lineAnswer(ctx context.Context, text []byte) (string, verdict, bool) {
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", verdict{}, false
	}

	object, isObject := value.(map[string]any)
	if !isObject {
		return "", verdict{}, false
	}

	// Only an answered line is noted. A failed line answers nothing, and under --merge a
	// duplicate's error line carries the same fields as the record it repeats.
	if !f.merge {
		if _, failed := object["error"]; failed {
			return "", verdict{}, false
		}

		id, ok := answerID(object["id"])

		return id, verdictOf(object), ok
	}

	folded, isObject := object[f.mergeKey].(map[string]any)
	if !isObject {
		return "", verdict{}, false
	}

	if _, failed := folded["error"]; failed {
		return "", verdict{}, false
	}

	// A record that is not an object merges into a wrapper around it. An object whose only key is
	// state merges into the same shape, so the whole line is still tried when the state names nothing.
	if state, wrapped := wrappedState(object, f.mergeKey); wrapped {
		if id, ok := f.named(ctx, state); ok {
			return id, verdictOf(folded), true
		}
	}

	id, ok := f.named(ctx, value)

	return id, verdictOf(folded), ok
}

func verdictOf(answers map[string]any) verdict {
	return verdict{rejected: answers["assert"] == false, abstained: answers["abstain"] == true}
}

type verdict struct {
	rejected  bool
	abstained bool
}

func wrappedState(line map[string]any, mergeKey string) (any, bool) {
	state, hasState := line["state"]
	_, hasAnswers := line[mergeKey]
	_, stateIsObject := state.(map[string]any)

	return state, len(line) == 2 && hasState && hasAnswers && !stateIsObject
}

func (f answersFormat) rowAnswer(ctx context.Context, columns, cells []string) (string, verdict, bool) {
	row := make(map[string]any, len(columns))

	for i, name := range columns {
		if i < len(cells) {
			row[name] = cells[i]
		}
	}

	if failure, ok := row["error"].(string); ok && failure != "" {
		return "", verdict{}, false
	}

	// An input column cannot take the assert column's name, so under --merge it is still the gate's.
	judged := verdict{rejected: row["assert"] == "false", abstained: row["assert"] == "abstain"}

	if !f.merge {
		id, ok := answerID(row["id"])

		return id, judged, ok
	}

	id, ok := f.named(ctx, row)

	return id, judged, ok
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

type ledgerKey [16]byte

type span struct {
	start int64
	end   int64
}

type ledger struct {
	mu        sync.Mutex
	answered  map[ledgerKey]answeredLine
	lines     []span
	pending   int
	skipped   int
	rejected  int
	abstained int
	ended     bool
}

type answeredLine struct {
	at     span
	judged verdict
}

func (l *ledger) note(id string, at span, judged verdict) {
	// The newest line wins, since a resume appends after what the file held before.
	l.answered[keyOf(id)] = answeredLine{at: at, judged: judged}
}

func keyOf(id string) ledgerKey {
	// Half the digest, since the ledger holds one key per answered line and 128 bits still leave a
	// collision out of reach.
	sum := sha256.Sum256([]byte(id))

	return ledgerKey(sum[:16])
}

func (l *ledger) admit(rec *namedRecord) (answered bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec.slot = len(l.lines)

	if rec.id != nil {
		key := keyOf(idText(rec.id))
		if stored, found := l.answered[key]; found {
			delete(l.answered, key)
			l.lines = append(l.lines, stored.at)
			l.skip(stored.judged)

			return true
		}
	}

	l.lines = append(l.lines, span{})
	l.pending++

	return false
}

func (l *ledger) skip(judged verdict) {
	l.skipped++

	// Counted as if judged this run, so a resumed gate exits on every answer in the file and not
	// only on the ones it asked for.
	if judged.rejected {
		l.rejected++
	}

	if judged.abstained {
		l.abstained++
	}
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

func (l *ledger) takeOrder(prune bool) []span {
	l.mu.Lock()
	defer l.mu.Unlock()

	lines := l.lines
	l.lines = nil

	if prune {
		return lines
	}

	unasked := slices.SortedFunc(maps.Values(l.answered), func(a, b answeredLine) int {
		return cmp.Compare(a.at.start, b.at.start)
	})

	for _, stored := range unasked {
		lines = append(lines, stored.at)
	}

	return lines
}

func (l *ledger) skips() (records, rejected, abstained int) {
	if l == nil {
		return 0, 0, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	return l.skipped, l.rejected, l.abstained
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
