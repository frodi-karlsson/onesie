package input

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// NewStream reads one record per line. skipBlank drops blank lines entirely rather than reporting
// them, which breaks the one line per line alignment and is therefore opt in.
func NewStream(r io.Reader, mode Mode, skipBlank bool) *Stream {
	return &Stream{
		reader:    bufio.NewReaderSize(r, 64*1024),
		mode:      mode,
		skipBlank: skipBlank,
	}
}

// Stream yields one record per input line, in input order.
type Stream struct {
	reader    *bufio.Reader
	mode      Mode
	skipBlank bool
	index     int
	line      int
}

// Next returns the next record. The second result is false at end of input. A record carrying a
// non nil Err is an input error, which is reported and counted but does not stop the run.
func (s *Stream) Next() (Record, bool, error) {
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

		// Trimmed rather than compared to the empty string, since a line of spaces is a formatting
		// accident in exactly the same way and sending it would pay for a request the model cannot
		// answer.
		if strings.TrimSpace(text) == "" {
			if s.skipBlank {
				continue
			}

			return s.fail(text, errors.New(
				"blank line, an empty state is a request the model cannot answer")), true, nil
		}

		return s.record(text), true, nil
	}
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
	// Wire is what reaches the API and any merge wrapper. It is the raw bytes for a JSON mode, so
	// a large integer keeps its digits and an object keeps its key order, and the parsed value for
	// a text mode. State stays parsed, for the checks that need a Go value.
	Wire any
	// Raw is the line exactly as read, which --merge folds the answers into.
	Raw string
	// Err marks an input error. No request is made for such a record.
	Err *LineError
}

// LineError is a line jev could not read. It is reported per record and counted, and under
// --stop-on-error it exits 2 rather than taking a transport code.
type LineError struct {
	Line int
	Err  error
}

// Error names the line, since a message that locates nothing in a ten thousand line file is not
// worth printing. There is deliberately no jev prefix: the stderr writer adds one, and inside a
// JSON error record the prefix is noise.
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
		if builder.Len()+len(chunk) > maxLineBytes {
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
		return s.fail(line, fmt.Errorf("line is not one complete JSON value: %w", err))
	}

	if err := CheckState(value); err != nil {
		return s.fail(line, err)
	}

	return s.ok(line, value, json.RawMessage(line))
}

func (s *Stream) ok(line string, state, wire any) Record {
	record := Record{Index: s.index, Line: s.line, State: state, Raw: line, Wire: wire}
	s.index++

	return record
}

func (s *Stream) fail(line string, err error) Record {
	record := Record{
		Index: s.index,
		Line:  s.line,
		Raw:   line,
		Err:   &LineError{Line: s.line, Err: err},
	}
	s.index++

	return record
}
