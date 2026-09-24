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
	"strconv"
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
	gated bool,
) (*ledger, error) {
	if answers == nil || !answers.resume || namer == nil {
		return nil, nil
	}

	book := &ledger{answered: map[ledgerKey]answeredLine{}}
	format := answersFormat{
		mode: mode, merge: merging(flags), mergeKey: mergeKey(flags), namer: namer, gated: gated,
	}

	err := readAnswers(ctx, answers, func(r io.Reader) error {
		return format.eachAnswer(ctx, r, book.note)
	})
	if err != nil {
		return nil, err
	}

	return book, nil
}

func resumedVerdicts(
	ctx context.Context,
	answers *outFile,
	namer *jq.Expr,
	format answersFormat,
	flags *runFlags,
) ([]verdict, error) {
	if answers == nil || !answers.resume || namer != nil {
		return nil, nil
	}

	var (
		judged []verdict
		last   span
	)

	err := readAnswers(ctx, answers, func(r io.Reader) error {
		return format.eachVerdict(r, func(at span, stored verdict) {
			judged = append(judged, stored)
			last = at
		})
	})
	if err != nil {
		return nil, err
	}

	// A run stopped by --stop-on-error writes nothing after the record it stopped at, so a stored
	// failure on the last line is where it stopped, and that record is asked again. The line is
	// trimmed with the partial one, under the lock, on the first write.
	if flags.stopOnError && len(judged) > 0 && judged[len(judged)-1].failure != nil {
		answers.resumeAt(last.start)
		flags.resumeSkip--

		return judged[:len(judged)-1], nil
	}

	return judged, nil
}

func readAnswers(ctx context.Context, answers *outFile, read func(r io.Reader) error) (err error) {
	file, err := answers.open(answers.path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("onesie: reading %s to resume: %w", answers.path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	// Only up to the last complete line, since the partial one after it is trimmed before anything
	// is appended.
	if readErr := read(io.LimitReader(file, answers.keep)); readErr != nil {
		return fmt.Errorf("onesie: reading %s to resume: %w", answers.path, readErr)
	}

	return ctx.Err()
}

type answersFormat struct {
	mode      output.Mode
	merge     bool
	mergeKey  string
	namer     *jq.Expr
	gated     bool
	forwarded bool
}

func (f answersFormat) eachAnswer(
	ctx context.Context,
	r io.Reader,
	note func(id string, at span, judged verdict),
) error {
	return f.eachStored(r,
		func(at span, text []byte) {
			if id, judged, ok := f.lineAnswer(ctx, text); ok {
				note(id, at, f.kept(judged))
			}
		},
		func(at span, row map[string]any) {
			if id, judged, ok := f.rowAnswer(ctx, row); ok {
				note(id, at, f.kept(judged))
			}
		})
}

func (f answersFormat) eachVerdict(r io.Reader, note func(at span, judged verdict)) error {
	// Every stored line is noted, a failed one included, since a resume by position skips one
	// record per line.
	return f.eachStored(r,
		func(at span, text []byte) {
			note(at, f.kept(f.lineVerdict(text)))
		},
		func(at span, row map[string]any) {
			note(at, f.kept(rowVerdict(row)))
		})
}

func (f answersFormat) kept(judged verdict) verdict {
	// A run with no gate wrote no verdict, so an assert it reads is an input field. Under a merge
	// only a gated run keeps an input column from taking that name. The error key and column are
	// reserved in every run, so a failure still stands.
	if !f.gated {
		return verdict{failure: judged.failure}
	}

	return judged
}

func (f answersFormat) eachStored(
	r io.Reader,
	visitLine func(at span, text []byte),
	visitRow func(at span, row map[string]any),
) error {
	if f.mode != output.CSV && f.mode != output.TSV {
		return eachLine(r, func(at span, text []byte) bool {
			visitLine(at, text)

			return true
		})
	}

	var columns []string

	return eachRow(r, f.mode == output.CSV, func(at span, cells []string) bool {
		if columns == nil {
			columns = cells

			return true
		}

		visitRow(at, rowOf(columns, cells))

		return true
	})
}

func rowOf(columns, cells []string) map[string]any {
	row := make(map[string]any, len(columns))

	for i, name := range columns {
		if i < len(cells) {
			row[name] = cells[i]
		}
	}

	return row
}

func (f answersFormat) lineVerdict(text []byte) verdict {
	if f.forwarded {
		return verdict{failure: ownFailure(text)}
	}

	_, object, ok := decodeLine(text)
	if !ok {
		return verdict{}
	}

	if !f.merge {
		return verdictOf(object)
	}

	folded, isObject := object[f.mergeKey].(map[string]any)
	if !isObject {
		return verdict{}
	}

	return verdictOf(folded)
}

func ownFailure(text []byte) *output.Failure {
	var line struct {
		Error *output.Failure `json:"error"`
	}

	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&line); err != nil || line.Error == nil {
		return nil
	}

	// A response body is written as the server sent it, and may carry an error key of its own. Only
	// a line matching onesie's encoding byte for byte is onesie's error line.
	if !bytes.Equal(bytes.TrimSuffix(text, []byte("\n")), output.EncodeFailure(line.Error)) {
		return nil
	}

	return line.Error
}

func (f answersFormat) lineAnswer(ctx context.Context, text []byte) (string, verdict, bool) {
	value, object, decoded := decodeLine(text)
	if !decoded {
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

func decodeLine(text []byte) (any, map[string]any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, nil, false
	}

	object, isObject := value.(map[string]any)

	return value, object, isObject
}

func verdictOf(answers map[string]any) verdict {
	judged := verdict{rejected: answers["assert"] == false, abstained: answers["abstain"] == true}

	if stored, failed := answers["error"]; failed {
		judged.failure = failureOf(stored)
	}

	return judged
}

type verdict struct {
	rejected  bool
	abstained bool
	failure   *output.Failure
}

func failureOf(stored any) *output.Failure {
	failure := &output.Failure{}

	fields, isObject := stored.(map[string]any)
	if !isObject {
		return failure
	}

	if kind, isText := fields["kind"].(string); isText {
		failure.Kind = kind
	}

	if message, isText := fields["message"].(string); isText {
		failure.Message = message
	}

	if number, isNumber := fields["status"].(json.Number); isNumber {
		if status, err := strconv.Atoi(number.String()); err == nil {
			failure.Status = &status
		}
	}

	return failure
}

func wrappedState(line map[string]any, mergeKey string) (any, bool) {
	state, hasState := line["state"]
	_, hasAnswers := line[mergeKey]
	_, stateIsObject := state.(map[string]any)

	return state, len(line) == 2 && hasState && hasAnswers && !stateIsObject
}

func (f answersFormat) rowAnswer(ctx context.Context, row map[string]any) (string, verdict, bool) {
	if failure, ok := row["error"].(string); ok && failure != "" {
		return "", verdict{}, false
	}

	judged := rowVerdict(row)

	if !f.merge {
		id, ok := answerID(row["id"])

		return id, judged, ok
	}

	id, ok := f.named(ctx, row)

	return id, judged, ok
}

func rowVerdict(row map[string]any) verdict {
	judged := verdict{rejected: row["assert"] == "false", abstained: row["assert"] == "abstain"}

	// A row keeps only the message, so its kind and status are unknown.
	if message, isText := row["error"].(string); isText && message != "" {
		judged.failure = &output.Failure{Message: message}
	}

	return judged
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
	mu       sync.Mutex
	answered map[ledgerKey]answeredLine
	lines    []span
	pending  int
	skips    tally
	ended    bool
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

func (l *ledger) admit(rec *namedRecord) (verdict, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec.slot = len(l.lines)

	if rec.id != nil {
		key := keyOf(idText(rec.id))
		if stored, found := l.answered[key]; found {
			delete(l.answered, key)
			l.lines = append(l.lines, stored.at)
			l.skips.count(stored.judged)

			return stored.judged, true
		}
	}

	l.lines = append(l.lines, span{})
	l.pending++

	return verdict{}, false
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

func (l *ledger) skipped() tally {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.skips
}

type tally struct {
	records   int
	failed    int
	rejected  int
	abstained int
}

func (t *tally) count(judged verdict) {
	t.records++

	// Counted as if judged this run, so a resumed gate exits on every answer in the file and not
	// only on the ones it asked for. A stored failure counts the same way, since a resume by
	// position skips it rather than asking again.
	if judged.failure != nil {
		t.failed++
	}

	if judged.rejected {
		t.rejected++
	}

	if judged.abstained {
		t.abstained++
	}
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
