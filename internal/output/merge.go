package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrMergeKeyTaken reports an input object that already carries the merge key. The caller decides
// whether that ends the run or fails the one record.
var ErrMergeKeyTaken = errors.New("the input already has the merge key")

// WriteMerged folds a record's answers into the input line under key, so a pipeline keeps the
// fields it came in with. raw is the input line exactly as read, and state is what was sent to the
// API, which is nil for a record onesie could not read.
func WriteMerged(w io.Writer, mode Mode, rec Record, raw string, state any, key string) error {
	answers, err := encode(mode, rec)
	if err != nil {
		return err
	}

	name, err := json.Marshal(key)
	if err != nil {
		return err
	}

	container, err := merge(raw, state, key, name, answers)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(w, container)

	return err
}

func merge(raw string, state any, key string, name, answers []byte) (string, error) {
	// The shape follows what was sent, not what the line looks like. Under -i lines a line reading
	// {"a":1} was sent as a string, so it wraps rather than folds. An empty raw has no line to fold
	// into and always wraps.
	if raw != "" && sentObject(state) {
		return splice(raw, key, name, answers)
	}

	encoded, err := json.Marshal(stateFor(state, raw))
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`{"state":%s,%s:%s}`, encoded, name, answers), nil
}

func sentObject(state any) bool {
	switch typed := state.(type) {
	case map[string]any:
		return true
	case json.RawMessage:
		// The bytes are only ever set from a value that already parsed, so the first token decides
		// the shape without a second parse.
		trimmed := bytes.TrimLeft(typed, " \t\r\n")

		return len(trimmed) > 0 && trimmed[0] == '{'
	default:
		return false
	}
}

func splice(raw string, key string, name, answers []byte) (string, error) {
	trimmed := strings.TrimSpace(raw)

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil {
		return "", err
	}

	if _, taken := fields[key]; taken {
		return "", ErrMergeKeyTaken
	}

	// Compacted before splicing, since a pretty printed object under -i json is routinely multi
	// line and would otherwise break the one line per record contract. Compact works on bytes and
	// preserves key order, so the input's field order survives.
	var flat bytes.Buffer
	if err := json.Compact(&flat, []byte(trimmed)); err != nil {
		return "", err
	}

	body := strings.TrimSuffix(flat.String(), "}")
	if len(fields) > 0 {
		body += ","
	}

	return fmt.Sprintf(`%s%s:%s}`, body, name, answers), nil
}

func stateFor(state any, raw string) any {
	if state != nil {
		// A record onesie could not read has no parsed state, so the line itself stands in.
		return state
	}

	return raw
}
