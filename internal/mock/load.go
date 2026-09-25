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

	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const prefix = "onesie: --mock"

var errTooLong = errors.New("line too long")

// Load reads a mock file and checks every entry against the questions. Lines are matched by id
// when byID is set and by record position otherwise.
func Load(r io.Reader, questions []plan.Question, byID bool) (*Answers, error) {
	lines, err := readLines(r)
	if err != nil {
		return nil, err
	}

	if len(lines) == 0 {
		return nil, errors.New(prefix + ": the file is empty")
	}

	if whole, isObject := oneObject(lines); isObject {
		entry, answers, entryErr := parseEntry(whole, questions, prefix)
		if entryErr != nil {
			return nil, entryErr
		}

		if !answers {
			entry = nil
		}

		return &Answers{every: entry, shared: true}, nil
	}

	return loadLines(lines, questions, byID)
}

func readLines(r io.Reader) ([][]byte, error) {
	reader := bufio.NewReader(r)

	var lines [][]byte

	blank := true

	for number := 1; ; number++ {
		line, err := readLine(reader)
		if errors.Is(err, errTooLong) {
			return nil, fmt.Errorf("%s line %d is longer than %d bytes, the max-line-bytes cap -V lists",
				prefix, number, limits.MaxLineBytes)
		}

		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: %w", prefix, err)
		}

		if errors.Is(err, io.EOF) && line == nil {
			break
		}

		if len(bytes.TrimSpace(line)) > 0 {
			blank = false
		}

		lines = append(lines, line)

		if errors.Is(err, io.EOF) {
			break
		}
	}

	if blank {
		return nil, nil
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

func oneObject(lines [][]byte) (map[string]any, bool) {
	whole := bytes.Join(lines, []byte("\n"))

	fields, err := decodeObject(whole)
	if err != nil {
		return nil, false
	}

	// A lone line that names its record is an answers line, so a one line --out file under --id
	// still matches by id.
	if _, named := fields["id"]; named {
		return nil, false
	}

	return fields, true
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

func loadLines(lines [][]byte, questions []plan.Question, byID bool) (*Answers, error) {
	answers := &Answers{byID: byID, ids: map[string]*Entry{}}
	firstLine := map[string]int{}

	for i, raw := range lines {
		number := i + 1
		where := fmt.Sprintf("%s line %d", prefix, number)

		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, fmt.Errorf("%s is blank", where)
		}

		fields, err := decodeObject(raw)
		if err != nil {
			return nil, fmt.Errorf("%s is not a JSON object", where)
		}

		entry, answered, err := parseEntry(fields, questions, where)
		if err != nil {
			return nil, err
		}

		if !answered {
			entry = nil
		}

		answers.lines = append(answers.lines, entry)

		if !byID {
			continue
		}

		text := jq.IDText(fields["id"])
		if text == "" {
			return nil, fmt.Errorf("%s has no id, and --id matches the file by id", where)
		}

		if first, taken := firstLine[text]; taken {
			return nil, fmt.Errorf("%s: id '%s' is also the id of line %d", where, text, first)
		}

		firstLine[text] = number
		answers.ids[text] = entry
	}

	return answers, nil
}

// Lookup returns the entry for the record at a position counted from one, or for an id when the
// file is matched by id. The second result is false for a record the file does not answer.
func (a *Answers) Lookup(position int, id any) (Entry, bool) {
	var found *Entry

	switch {
	case a.shared:
		found = a.every
	case a.byID:
		found = a.ids[jq.IDText(id)]
	case position >= 1 && position <= len(a.lines):
		found = a.lines[position-1]
	}

	if found == nil {
		return Entry{}, false
	}

	return *found, true
}

// Answers is a checked mock file, ready to answer records by position or by id.
type Answers struct {
	shared bool
	every  *Entry
	byID   bool
	lines  []*Entry
	ids    map[string]*Entry
}
