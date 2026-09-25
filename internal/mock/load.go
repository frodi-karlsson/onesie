// Package mock reads a file of canned answers and checks it against the questions a run asks, so
// the run can answer from it with no key and no network. It performs no IO of its own.
package mock

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Flag is the spelling a message uses when Options names none.
const Flag = "--mock"

var errTooLong = errors.New("line too long")

// Load reads a mock file and checks every entry against the questions. Lines are matched by id
// when Options.ByID is set and by record position otherwise.
func Load(r io.Reader, questions []plan.Question, opts Options) (*Answers, error) {
	if opts.Spelled == "" {
		opts.Spelled = Flag
	}

	parse := parser{questions: questions, opts: opts, prefix: "onesie: " + opts.Spelled}

	lines, err := parse.readLines(r)
	if err != nil {
		return nil, err
	}

	if len(lines) == 0 {
		return nil, errors.New(parse.prefix + ": the file is empty")
	}

	whole, isObject, err := parse.oneObject(lines)
	if err != nil {
		return nil, err
	}

	if isObject {
		entry, err := parse.entry(whole, parse.prefix)
		if err != nil {
			return nil, err
		}

		return &Answers{spelled: opts.Spelled, shared: true, every: slot{entry: entry}}, nil
	}

	return parse.lines(lines)
}

func (p parser) readLines(r io.Reader) ([][]byte, error) {
	reader := bufio.NewReader(r)

	var lines [][]byte

	for number := 1; ; number++ {
		line, err := readLine(reader)
		if errors.Is(err, errTooLong) {
			return nil, fmt.Errorf("%s line %d is longer than %d bytes, the max-line-bytes cap -V lists",
				p.prefix, number, limits.MaxLineBytes)
		}

		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: %w", p.prefix, err)
		}

		if errors.Is(err, io.EOF) && line == nil {
			break
		}

		lines = append(lines, line)

		if errors.Is(err, io.EOF) {
			break
		}
	}

	// Blank lines at the end are the trailing newlines an editor leaves, and answer no record.
	for len(lines) > 0 && len(bytes.TrimSpace(lines[len(lines)-1])) == 0 {
		lines = lines[:len(lines)-1]
	}

	return lines, nil
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte

	for {
		chunk, err := reader.ReadSlice('\n')
		if len(line)+len(chunk) > limits.MaxLineBytes+1 {
			return nil, errTooLong
		}

		line = append(line, chunk...)

		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}

		if errors.Is(err, io.EOF) && len(line) == 0 {
			return nil, io.EOF
		}

		line = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		if len(line) > limits.MaxLineBytes {
			return nil, errTooLong
		}

		return line, err
	}
}

func (p parser) oneObject(lines [][]byte) (map[string]any, bool, error) {
	fields, isObject := wholeObject(lines)
	if !isObject {
		return nil, false, nil
	}

	if _, named := fields["id"]; !named {
		return fields, true, nil
	}

	// A lone line that names its record is an answers line, so a one line --out file under --id
	// still matches by id.
	if len(lines) == 1 {
		return nil, false, nil
	}

	return nil, false, fmt.Errorf("%s: the file is one object over several lines that carries an id. "+
		"Write each answers line on a line of its own, or drop the id to answer every record", p.prefix)
}

func wholeObject(lines [][]byte) (map[string]any, bool) {
	fields, err := decodeObject(bytes.Join(lines, []byte("\n")))

	return fields, err == nil
}

func decodeObject(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}

	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data")
	}

	fields, isObject := value.(map[string]any)
	if !isObject {
		return nil, errors.New("not an object")
	}

	return fields, nil
}

func (p parser) lines(lines [][]byte) (*Answers, error) {
	answers := &Answers{spelled: p.opts.Spelled, byID: p.opts.ByID, ids: map[string]slot{}}

	for i, raw := range lines {
		number := i + 1
		where := fmt.Sprintf("%s line %d", p.prefix, number)

		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, fmt.Errorf("%s is blank", where)
		}

		fields, err := decodeObject(raw)
		if err != nil {
			return nil, fmt.Errorf("%s is not a JSON object", where)
		}

		entry, err := p.entry(fields, where)
		if err != nil {
			return nil, err
		}

		answers.lines = append(answers.lines, slot{entry: entry, line: number})

		if !p.opts.ByID {
			continue
		}

		text := jq.IDText(fields["id"])
		if text == "" {
			return nil, fmt.Errorf("%s has no id, and --id matches the file by id", where)
		}

		if first, taken := answers.ids[text]; taken {
			return nil, fmt.Errorf("%s: id '%s' is also the id of line %d", where, text, first.line)
		}

		answers.ids[text] = slot{entry: entry, line: number}
	}

	return answers, nil
}

type parser struct {
	questions []plan.Question
	opts      Options
	prefix    string
}

// Lookup returns the entry for the record at a position counted from one, or for an id when the
// file is matched by id. The second result is false for a record the file does not answer.
func (a *Answers) Lookup(position int, id any) (Entry, bool) {
	found, _ := a.find(position, id)
	if found.entry == nil {
		return Entry{}, false
	}

	return *found.entry, true
}

// Missing says why the file answers no record at a position or id, naming the record by its input
// line.
func (a *Answers) Missing(position int, id any, line int) string {
	found, exists := a.find(position, id)
	prefix := "onesie: " + a.spelled

	switch {
	case exists && a.shared:
		return fmt.Sprintf("%s replays a line onesie could not read, so input line %d has no answer. "+
			"Give it answers", prefix, line)
	case exists:
		return fmt.Sprintf("%s line %d replays a line onesie could not read, so input line %d has no answer. "+
			"Give it answers", prefix, found.line, line)
	case a.byID:
		return fmt.Sprintf("%s has no line with id '%s', so input line %d has no answer. Add one",
			prefix, jq.IDText(id), line)
	default:
		return fmt.Sprintf("%s has no line %d, so input line %d has no answer. Add one", prefix, position, line)
	}
}

func (a *Answers) find(position int, id any) (slot, bool) {
	switch {
	case a.shared:
		return a.every, true
	case a.byID:
		found, exists := a.ids[jq.IDText(id)]

		return found, exists
	case position >= 1 && position <= len(a.lines):
		return a.lines[position-1], true
	default:
		return slot{}, false
	}
}

// Answers is a checked mock file, ready to answer records by position or by id.
type Answers struct {
	spelled string
	shared  bool
	every   slot
	byID    bool
	lines   []slot
	ids     map[string]slot
}

type slot struct {
	entry *Entry // Nil for a replayed line onesie could not read, which answers nothing.
	line  int
}

// Options says how a mock file is matched and named.
type Options struct {
	// ByID matches lines by their id rather than by record position.
	ByID bool
	// Spelled names where the file came from in every message, --mock unless set.
	Spelled string
	// Timeout is the attempt timeout a timeout entry reports.
	Timeout time.Duration
}
