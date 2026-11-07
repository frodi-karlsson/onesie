package input

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/frodi-karlsson/onesie/internal/delimiter"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

var errRowTooLong = errors.New("a row is longer than the limit")

// NewStream reads one record per line, as opts says.
func NewStream(r io.Reader, mode Mode, opts StreamOptions) *Stream {
	stream := &Stream{
		reader:    bufio.NewReaderSize(r, 64*1024),
		mode:      mode,
		skipBlank: opts.SkipBlank,
		lineCap:   lineCapOf(mode),
		comma:     delimiter.DefaultComma,
	}

	if mode == CSV {
		if opts.Delimiter.Comma != 0 {
			stream.comma = opts.Delimiter.Comma
		}

		// Bounds what one row may pull from stdin, since an unterminated quote would otherwise read
		// the rest of the input into a single field.
		stream.budget = &rowBudget{reader: stream.reader}
		stream.rows = csvRows(stream.budget, stream.comma)
		stream.detect = opts.Delimiter.Detect
	}

	return stream
}

func csvRows(r io.Reader, comma rune) *csv.Reader {
	rows := csv.NewReader(r)
	rows.Comma = comma
	// Checked per row instead, so a short row fails that one record rather than the run.
	rows.FieldsPerRecord = -1

	return rows
}

// StreamOptions holds how a Stream reads its lines.
type StreamOptions struct {
	// SkipBlank drops blank lines entirely rather than reporting them, which breaks the one line per
	// line alignment and is therefore opt in.
	SkipBlank bool
	// Delimiter splits a CSV row. Every other mode ignores it.
	Delimiter Delimiter
}

func lineCapOf(mode Mode) int {
	if mode == Request {
		return limits.MaxRequestLineBytes
	}

	return limits.MaxLineBytes
}

// Stream yields one record per input line, in input order.
type Stream struct {
	reader      *bufio.Reader
	mode        Mode
	skipBlank   bool
	lineCap     int
	index       int
	line        int
	rows        *csv.Reader
	budget      *rowBudget
	header      []string
	headerReady bool
	headerErr   error
	comma       rune
	detect      bool
}

// Next returns the next record. The second result is false at end of input. A record carrying a
// non nil Err is an input error, which is reported and counted but does not stop the run.
func (s *Stream) Next() (Record, bool, error) {
	if s.mode.Delimited() {
		return s.nextRow()
	}

	for {
		text, tooLong, err := s.read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return Record{}, false, nil
			}

			// Not wrapped with a spelling of its own. LineError already prefixes stdin for line
			// zero, and carrying both would report the same word twice.
			return Record{}, false, &LineError{Err: err}
		}

		s.line++

		if tooLong {
			// The line itself is dropped rather than buffered, so Raw is empty and --merge wraps
			// an empty state. LineError already names the line, which is what locates the record.
			return s.fail("", errors.New("line is longer than the limit")), true, nil
		}

		// Trimmed, since a line of spaces is the same formatting accident and would pay for a
		// request the model cannot answer.
		if strings.TrimSpace(text) == "" {
			if s.skipBlank {
				continue
			}

			return s.fail(text, fmt.Errorf("blank line, %w. Pass --skip-blank to drop blank lines", ErrEmptyState)), true, nil
		}

		return s.record(text), true, nil
	}
}

// Comma is the CSV separator, final after ReadHeader or the first record from Next.
func (s *Stream) Comma() rune {
	return s.comma
}

// ReadHeader initializes a delimited stream and returns an independent header snapshot.
// The stream has one owner, so callers must not read it concurrently.
func (s *Stream) ReadHeader() (Header, error) {
	if !s.mode.Delimited() {
		return Header{}, errors.New("headers require csv or tsv input")
	}
	if err := s.ensureHeader(); err != nil {
		return Header{}, err
	}
	separator := s.comma
	if s.mode == TSV {
		separator = '\t'
	}
	return Header{Names: slices.Clone(s.header), Separator: separator}, nil
}

// Header holds validated column names and the effective field separator.
type Header struct {
	Names     []string
	Separator rune
}

func (s *Stream) nextRow() (Record, bool, error) {
	if err := s.ensureHeader(); err != nil {
		if errors.Is(err, io.EOF) {
			return Record{}, false, nil
		}
		return Record{}, false, err
	}

	fields, err := s.nextFields()
	if errors.Is(err, io.EOF) {
		return Record{}, false, nil
	}

	var bad *rowError
	if errors.As(err, &bad) {
		return s.fail("", bad.err), true, nil
	}

	if err != nil {
		return Record{}, false, err
	}

	if len(fields) != len(s.header) {
		return s.fail("", fmt.Errorf("row has %d fields, the header has %d", len(fields), len(s.header))), true, nil
	}

	return s.row(fields), true, nil
}

func (s *Stream) ensureHeader() error {
	if s.headerReady {
		return s.headerErr
	}
	s.headerReady = true
	if s.detect {
		if err := s.detectComma(); err != nil {
			s.headerErr = err
			return err
		}
	}
	s.header, s.headerErr = s.readHeader()
	if s.headerErr == nil && s.header == nil {
		s.headerErr = io.EOF
	}
	return s.headerErr
}

func (s *Stream) detectComma() error {
	s.detect = false

	// Detection runs on the caller goroutine. Replaying the held lines ahead of the remaining
	// input preserves the csv reader line numbers.
	held, comma, err := s.leadingLines()
	if err != nil {
		return err
	}

	s.comma = comma
	s.budget.reader = io.MultiReader(bytes.NewReader(held), s.reader)
	s.rows = csvRows(s.budget, s.comma)

	return nil
}

func (s *Stream) leadingLines() (held []byte, comma rune, err error) {
	for {
		line, readErr := s.reader.ReadSlice('\n')
		held = append(held, line...)

		if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, bufio.ErrBufferFull) {
			return nil, 0, &LineError{Err: readErr}
		}

		// The csv reader skips an empty line, so the header is the first line that is not empty. A line
		// with no newline ends the input or fills the buffer, and the blank lines held are capped as a
		// row is.
		if !emptyLine(line) || len(held) >= limits.MaxLineBytes {
			return held, DetectDelimiter(string(line)), nil
		}
	}
}

func emptyLine(line []byte) bool {
	return bytes.HasSuffix(line, []byte("\n")) && len(bytes.TrimRight(line, "\r\n")) == 0
}

func (s *Stream) readHeader() ([]string, error) {
	header, err := s.nextFields()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}

	var bad *rowError
	if errors.As(err, &bad) {
		return nil, &LineError{Line: s.line, Err: fmt.Errorf("the header is not valid %s: %w", s.modeName(), bad.err)}
	}

	if err != nil {
		return nil, err
	}

	header[0] = strings.TrimPrefix(header[0], "\ufeff")
	seen := make(map[string]bool, len(header))

	for _, name := range header {
		if strings.TrimSpace(name) == "" {
			return nil, &LineError{Line: s.line, Err: errors.New("the header has a blank column name")}
		}

		if seen[name] {
			return nil, &LineError{Line: s.line, Err: fmt.Errorf("the header names '%s' twice", name)}
		}

		seen[name] = true
	}

	return header, nil
}

func (s *Stream) nextFields() ([]string, error) {
	if s.mode == TSV {
		return s.nextTabbed()
	}

	s.budget.left = limits.MaxLineBytes

	fields, err := s.rows.Read()
	if errors.Is(err, io.EOF) {
		return nil, io.EOF
	}

	var parseErr *csv.ParseError
	if errors.As(err, &parseErr) {
		s.line = parseErr.StartLine

		if errors.Is(parseErr.Err, errRowTooLong) {
			return nil, &LineError{Line: s.line, Err: errRowTooLong}
		}

		return nil, &rowError{err: fmt.Errorf("row is not valid csv: %w", parseErr.Err)}
	}

	if errors.Is(err, errRowTooLong) {
		return nil, &LineError{Line: s.line + 1, Err: errRowTooLong}
	}

	if err != nil {
		return nil, &LineError{Err: err}
	}

	s.line, _ = s.rows.FieldPos(0)

	return fields, nil
}

func (s *Stream) nextTabbed() ([]string, error) {
	for {
		text, tooLong, err := s.read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, io.EOF
			}

			return nil, &LineError{Err: err}
		}

		s.line++

		if tooLong {
			return nil, &rowError{err: errRowTooLong}
		}

		if text == "" {
			continue
		}

		return delimiter.TSVFields(text), nil
	}
}

func (s *Stream) row(fields []string) Record {
	state := make(map[string]any, len(fields))

	var wire bytes.Buffer

	wire.WriteByte('{')

	for i, field := range fields {
		if !utf8.ValidString(field) {
			return s.fail("", errors.New("row is not valid UTF-8"))
		}

		state[s.header[i]] = field

		if i > 0 {
			wire.WriteByte(',')
		}

		name, err := json.Marshal(s.header[i])
		if err != nil {
			return s.fail("", err)
		}

		value, err := json.Marshal(field)
		if err != nil {
			return s.fail("", err)
		}

		wire.Write(name)
		wire.WriteByte(':')
		wire.Write(value)
	}

	wire.WriteByte('}')

	if wire.Len() > limits.MaxLineBytes {
		return s.fail("", errors.New("row is longer than the limit"))
	}

	record := s.ok(wire.String(), state, json.RawMessage(wire.Bytes()))
	record.Header = s.header

	return record
}

func (s *Stream) modeName() string {
	if s.mode == TSV {
		return "tsv"
	}

	return "csv"
}

func (s *Stream) read() (string, bool, error) {
	var (
		builder strings.Builder
		tooLong bool
	)

	for {
		chunk, more, err := s.reader.ReadLine()
		if err != nil {
			// A final line with no trailing newline comes back with a nil error and EOF is
			// reported on the call after it, so there is no partial line to rescue here.
			return "", false, err
		}

		// An oversized line is reported rather than buffered, so one such record can neither
		// exhaust memory nor stop the batch.
		if builder.Len()+len(chunk) > s.lineCap {
			tooLong = true
		}

		if !tooLong {
			builder.Write(chunk)
		}

		if !more {
			return builder.String(), tooLong, nil
		}
	}
}

func (s *Stream) record(line string) Record {
	if !utf8.ValidString(line) {
		return s.fail(line, errors.New("line is not valid UTF-8"))
	}

	if s.mode == Lines {
		return s.ok(line, line, line)
	}

	var value any
	if err := json.Unmarshal([]byte(line), &value); err != nil {
		return s.fail(line, unreadableJSON(err))
	}

	if err := s.check(value); err != nil {
		return s.fail(line, err)
	}

	return s.ok(line, value, json.RawMessage(line))
}

func unreadableJSON(err error) error {
	// encoding/json reports a number past float64 as a type mismatch on a Go struct field, which
	// names nothing the line holds.
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) {
		if number, isNumber := strings.CutPrefix(mismatch.Value, "number "); isNumber {
			return fmt.Errorf("line holds the number %s, which is too large to read", number)
		}
	}

	return fmt.Errorf("line is not one complete JSON value: %w", err)
}

func (s *Stream) check(value any) error {
	if s.mode != Request {
		return CheckState(value)
	}

	// A request body is forwarded opaquely, so only its outermost shape is checked. A body the API
	// will reject for its contents is still sent, and the 422 is the answer.
	if _, object := value.(map[string]any); object {
		return nil
	}

	return fmt.Errorf("a request body must be a JSON object, got %s", jsonNoun(value))
}

func jsonNoun(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "number"
	}
}

func (s *Stream) ok(line string, state, wire any) Record {
	record := Record{Index: s.index, Line: s.line, State: state, Raw: line, Wire: wire}
	s.index++

	return record
}

func (s *Stream) fail(line string, err error) Record {
	record := Record{
		Index:  s.index,
		Line:   s.line,
		Raw:    line,
		Header: s.header,
		Err:    &LineError{Line: s.line, Err: err},
	}
	s.index++

	return record
}

// Record is one input line, ready to evaluate.
type Record struct {
	// Index is the record's position in the input, counting from zero.
	Index int
	// Line is the input line number, counting from one. It differs from Index as soon as
	// --skip-blank drops a line, and it is the number a message must quote.
	Line int
	// State is the parsed value, which the pre flight checks read. It is nil when Err is set.
	State any
	// Wire is what reaches the API. It is the raw bytes for a JSON mode, keeping large integers and
	// key order, and the parsed value for a text mode.
	Wire any
	// Raw is the line exactly as read, which --merge folds the answers into. For a csv or tsv row
	// it is the row as a JSON object, which is what was sent.
	Raw string
	// Header is the csv or tsv header the row was read under, nil for every other mode.
	Header []string
	// Err marks an input error. No request is made for such a record.
	Err *LineError
}

// LineError is a line onesie could not read. It is reported per record and counted, and under
// --stop-on-error it exits 2 rather than taking a transport code.
type LineError struct {
	Line int
	Err  error
}

// Error names the line, so the message locates the record. It carries no onesie prefix, since the
// stderr writer adds one and inside a JSON error record it is noise.
func (e *LineError) Error() string {
	// Line zero is stdin itself failing rather than a bad line, and quoting a line number nothing
	// has would send the reader looking for it.
	if e.Line == 0 {
		return fmt.Sprintf("stdin: %v", e.Err)
	}

	return fmt.Sprintf("line %d: %v", e.Line, e.Err)
}

// Unwrap exposes the cause, so errors.Is reaches it.
func (e *LineError) Unwrap() error {
	return e.Err
}

type rowError struct {
	err error
}

func (e *rowError) Error() string {
	return e.err.Error()
}

type rowBudget struct {
	reader io.Reader
	left   int
}

func (b *rowBudget) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errRowTooLong
	}

	if len(p) > b.left {
		p = p[:b.left]
	}

	n, err := b.reader.Read(p)
	b.left -= n

	return n, err
}
