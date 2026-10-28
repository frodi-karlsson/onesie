package answersfile

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

// ResumeLedger reads the answers a resume by id skips. It is nil when the run does not resume by id.
func ResumeLedger(ctx context.Context, answers *File, format Format) (*Ledger, error) {
	if answers == nil || !answers.resume || format.Namer == nil {
		return nil, nil
	}

	book := &Ledger{answered: map[ledgerKey]answeredLine{}}

	err := readAnswers(ctx, answers, func(r io.Reader) error {
		return format.eachAnswer(ctx, r, book.note)
	})
	if err != nil {
		return nil, err
	}

	return book, nil
}

// ResumedVerdicts reads the verdict on each line a resume by position skips. Under stopOnError a
// stored failure on the last line is asked again.
func ResumedVerdicts(
	ctx context.Context, answers *File, format Format, stopOnError bool, resume Resume,
) (Resume, error) {
	if answers == nil || !answers.resume || format.Namer != nil {
		return resume, nil
	}

	var (
		judged []Verdict
		last   Span
	)

	err := readAnswers(ctx, answers, func(r io.Reader) error {
		return format.eachVerdict(r, func(at Span, stored Verdict) {
			judged = append(judged, stored)
			last = at
		})
	})
	if err != nil {
		return Resume{}, err
	}

	// A run stopped by --stop-on-error writes nothing after the record it stopped at, so a stored
	// failure on the last line is where it stopped, and that record is asked again. The line is
	// trimmed with the partial one, under the lock, on the first write.
	if stopOnError && len(judged) > 0 && judged[len(judged)-1].Failure != nil {
		answers.resumeAt(last.Start)
		resume.Skip--
		resume.Stored = judged[:len(judged)-1]

		return resume, nil
	}

	resume.Stored = judged

	return resume, nil
}

func readAnswers(ctx context.Context, answers *File, read func(r io.Reader) error) (err error) {
	file, err := answers.open(answers.path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading %s to resume: %w", answers.path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	// Only up to the last complete line, since the partial one after it is trimmed before anything
	// is appended.
	if readErr := read(io.LimitReader(file, answers.keep)); readErr != nil {
		return fmt.Errorf("reading %s to resume: %w", answers.path, readErr)
	}

	return ctx.Err()
}

// ReadSpans calls read with the text of each span, reading the file forward. The text is reused
// across calls.
func (o *File) ReadSpans(ctx context.Context, spans []Span, read func(i int, text []byte)) (err error) {
	if len(spans) == 0 {
		return nil
	}

	file, err := o.open(o.path, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("reading %s to resume: %w", o.path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}

	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(spans[a].Start, spans[b].Start)
	})

	var text []byte

	for _, i := range order {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		at := spans[i]

		text = slices.Grow(text[:0], int(at.End-at.Start))[:at.End-at.Start]
		if _, readErr := file.ReadAt(text, at.Start); readErr != nil {
			return fmt.Errorf("reading %s to resume: %w", o.path, readErr)
		}

		read(i, text)
	}

	return nil
}

// Format says how the answers were written, so a resume reads them back.
type Format struct {
	Mode      output.Mode
	Merge     bool
	MergeKey  string
	Namer     *jq.Expr
	Gated     bool
	Forwarded bool
}

func (f Format) eachAnswer(
	ctx context.Context,
	r io.Reader,
	note func(id string, at Span, judged Verdict),
) error {
	return f.eachStored(r,
		func(at Span, text []byte) {
			if id, judged, ok := f.lineAnswer(ctx, text); ok {
				note(id, at, f.kept(judged))
			}
		},
		func(at Span, row map[string]any) {
			if id, judged, ok := f.rowAnswer(ctx, row); ok {
				note(id, at, f.kept(judged))
			}
		})
}

func (f Format) eachVerdict(r io.Reader, note func(at Span, judged Verdict)) error {
	// Every stored line is noted, a failed one included, since a resume by position skips one
	// record per line.
	return f.eachStored(r,
		func(at Span, text []byte) {
			note(at, f.kept(f.lineVerdict(text)))
		},
		func(at Span, row map[string]any) {
			note(at, f.kept(rowVerdict(row)))
		})
}

func (f Format) kept(judged Verdict) Verdict {
	// A run with no gate wrote no verdict, so an assert it reads is an input field. Under a merge
	// only a gated run keeps an input column from taking that name. The error key and column are
	// reserved in every run, so a failure still stands.
	if !f.Gated {
		return Verdict{Failure: judged.Failure}
	}

	return judged
}

func (f Format) eachStored(
	r io.Reader,
	visitLine func(at Span, text []byte),
	visitRow func(at Span, row map[string]any),
) error {
	if f.Mode != output.CSV && f.Mode != output.TSV {
		return eachLine(r, func(at Span, text []byte) bool {
			visitLine(at, text)

			return true
		})
	}

	var columns []string

	return eachRow(r, f.Mode == output.CSV, func(at Span, cells []string) bool {
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

func (f Format) lineVerdict(text []byte) Verdict {
	if f.Forwarded {
		return Verdict{Failure: ownFailure(text)}
	}

	_, object, ok := decodeLine(text)
	if !ok {
		return Verdict{}
	}

	if !f.Merge {
		return verdictOf(object)
	}

	folded, isObject := object[f.MergeKey].(map[string]any)
	if !isObject {
		return Verdict{}
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

func (f Format) lineAnswer(ctx context.Context, text []byte) (string, Verdict, bool) {
	value, object, decoded := decodeLine(text)
	if !decoded {
		return "", Verdict{}, false
	}

	// Only an answered line is noted. A failed line answers nothing, and under --merge a
	// duplicate's error line carries the same fields as the record it repeats.
	if !f.Merge {
		if _, failed := object["error"]; failed {
			return "", Verdict{}, false
		}

		id, ok := answerID(object["id"])

		return id, verdictOf(object), ok
	}

	folded, isObject := object[f.MergeKey].(map[string]any)
	if !isObject {
		return "", Verdict{}, false
	}

	if _, failed := folded["error"]; failed {
		return "", Verdict{}, false
	}

	// A record that is not an object merges into a wrapper around it. An object whose only key is
	// state merges into the same shape, so the whole line is still tried when the state names nothing.
	if state, wrapped := wrappedState(object, f.MergeKey); wrapped {
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

func verdictOf(answers map[string]any) Verdict {
	judged := Verdict{Rejected: answers["assert"] == false, Abstained: answers["abstain"] == true}

	if stored, failed := answers["error"]; failed {
		judged.Failure = failureOf(stored)
	}

	return judged
}

// Verdict is a stored line's outcome: rejected by the gate, abstained or failed.
type Verdict struct {
	Rejected  bool
	Abstained bool
	Failure   *output.Failure
}

func failureOf(stored any) *output.Failure {
	failure := &output.Failure{}

	fields, isObject := stored.(map[string]any)
	if !isObject {
		return failure
	}

	if kind, isText := fields["kind"].(string); isText {
		failure.Kind = output.Kind(kind)
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

func (f Format) rowAnswer(ctx context.Context, row map[string]any) (string, Verdict, bool) {
	if failure, ok := row["error"].(string); ok && failure != "" {
		return "", Verdict{}, false
	}

	judged := rowVerdict(row)

	if !f.Merge {
		id, ok := answerID(row["id"])

		return id, judged, ok
	}

	id, ok := f.named(ctx, row)

	return id, judged, ok
}

func rowVerdict(row map[string]any) Verdict {
	judged := Verdict{Rejected: row["assert"] == "false", Abstained: row["assert"] == "abstain"}

	// A row keeps only the message, so its kind and status are unknown.
	if message, isText := row["error"].(string); isText && message != "" {
		judged.Failure = &output.Failure{Message: message}
	}

	return judged
}

func (f Format) named(ctx context.Context, value any) (string, bool) {
	id, err := f.Namer.One(ctx, value)
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

	return jq.IDText(id), true
}

type ledgerKey [16]byte

// Span is a line's byte range in the answers file.
type Span struct {
	Start int64
	End   int64
}

// Ledger keys the answered lines of a resume by id, and orders the lines for compaction.
type Ledger struct {
	mu       sync.Mutex
	answered map[ledgerKey]answeredLine
	lines    []Span
	pending  int
	skips    Tally
	ended    bool
}

type answeredLine struct {
	at     Span
	judged Verdict
}

func (l *Ledger) note(id string, at Span, judged Verdict) {
	// The newest line wins, since a resume appends after what the file held before.
	l.answered[keyOf(id)] = answeredLine{at: at, judged: judged}
}

func keyOf(id string) ledgerKey {
	// Half the digest, since the ledger holds one key per answered line and 128 bits still leave a
	// collision out of reach.
	sum := sha256.Sum256([]byte(id))

	return ledgerKey(sum[:16])
}

// Admit gives a record its slot in the order, and its stored verdict when the file answers it.
func (l *Ledger) Admit(id any) (slot int, judged Verdict, answered bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	slot = len(l.lines)

	if id != nil {
		key := keyOf(jq.IDText(id))
		if stored, found := l.answered[key]; found {
			delete(l.answered, key)
			l.lines = append(l.lines, stored.at)
			l.skips.Count(stored.judged)

			return slot, stored.judged, true
		}
	}

	l.lines = append(l.lines, Span{})
	l.pending++

	return slot, Verdict{}, false
}

// StoredAt is where the file answers id, when it does.
func (l *Ledger) StoredAt(id any) (Span, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id == nil {
		return Span{}, false
	}

	stored, found := l.answered[keyOf(jq.IDText(id))]

	return stored.at, found
}

// Keep puts the line answering id in the order without asking it again.
func (l *Ledger) Keep(id any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id == nil {
		return
	}

	key := keyOf(jq.IDText(id))

	// Kept in its place and nothing more. The record is not asked, so it is neither pending nor
	// skipped.
	if stored, found := l.answered[key]; found {
		delete(l.answered, key)
		l.lines = append(l.lines, stored.at)
	}
}

// Forget drops the line answering id, so its record is asked again.
func (l *Ledger) Forget(id any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.answered, keyOf(jq.IDText(id)))
}

// Wrote records where the line for slot was written.
func (l *Ledger) Wrote(slot int, at Span) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.lines[slot] = at
	l.pending--
}

// End marks the input as read to its end.
func (l *Ledger) End() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.ended = true
}

// Complete reports whether every record was read and every line written.
func (l *Ledger) Complete() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.ended && l.pending == 0
}

// TakeOrder hands over the lines in input order. Without prune the lines no record asked for follow.
func (l *Ledger) TakeOrder(prune bool) []Span {
	l.mu.Lock()
	defer l.mu.Unlock()

	lines := l.lines
	l.lines = nil

	if prune {
		return lines
	}

	unasked := slices.SortedFunc(maps.Values(l.answered), func(a, b answeredLine) int {
		return cmp.Compare(a.at.Start, b.at.Start)
	})

	for _, stored := range unasked {
		lines = append(lines, stored.at)
	}

	return lines
}

// Skipped counts the records a resume skipped.
func (l *Ledger) Skipped() Tally {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.skips
}

// Tally counts records and their outcomes.
type Tally struct {
	Records   int
	Failed    int
	Rejected  int
	Abstained int
}

// Count adds one record with this verdict.
func (t *Tally) Count(judged Verdict) {
	t.Records++

	// Counted as if judged this run, so a resumed gate exits on every answer in the file and not
	// only on the ones it asked for. A stored failure counts the same way, since a resume by
	// position skips it rather than asking again.
	if judged.Failure != nil {
		t.Failed++
	}

	if judged.Rejected {
		t.Rejected++
	}

	if judged.Abstained {
		t.Abstained++
	}
}

func eachLine(r io.Reader, visit func(at Span, text []byte) bool) error {
	reader := bufio.NewReader(r)

	var offset int64

	for {
		text, err := reader.ReadBytes('\n')
		at := Span{Start: offset, End: offset + int64(len(text))}
		offset = at.End

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

func eachRow(r io.Reader, quoted bool, visit func(at Span, cells []string) bool) error {
	// TSV has no quoting, so a row is a line.
	if !quoted {
		return eachLine(r, func(at Span, text []byte) bool {
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
		if !visit(Span{Start: start, End: end}, cells) {
			return nil
		}

		start = end
	}
}

func headerLength(r io.Reader, quoted bool) (int64, error) {
	var length int64

	err := eachRow(r, quoted, func(at Span, _ []string) bool {
		length = at.End

		return false
	})

	return length, err
}
