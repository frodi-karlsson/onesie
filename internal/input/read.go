package input

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Resolve decides where the state comes from and reads it. It performs no network call and reads
// stdin at most once.
func Resolve(req Query) (Resolved, error) {
	switch {
	case req.HasStateFile:
		body, err := req.ReadFile(req.StateFile)
		if err != nil {
			return Resolved{}, fmt.Errorf("jev: reading %s: %w", req.StateFile, err)
		}

		label := fmt.Sprintf("--state-file '%s'", req.StateFile)

		return fromText(SourceStateFile, label, string(body), req.Mode, true)
	case req.HasState && req.State != "-":
		// No newline is stripped here. One is stripped from stdin and from a file, where a
		// trailing newline is an artefact of how the bytes arrived. A --state argument has no such
		// artefact.
		return fromText(SourceState, "--state", req.State, req.Mode, false)
	case req.HasState, !req.StdinTTY:
		return fromStdin(req)
	default:
		return Resolved{Source: SourceNone}, nil
	}
}

// Query is everything Resolve needs. Stdin, the tty answer and the file reader are injected so a
// test controls all three without a real terminal or a fixture on disk.
type Query struct {
	Mode         Mode
	Stdin        io.Reader
	StdinTTY     bool
	State        string
	HasState     bool
	StateFile    string
	HasStateFile bool
	ReadFile     func(string) ([]byte, error)
}

// Resolved is the outcome. State is nil when Source is SourceNone, and Raw is the text the state
// was read from, which --merge splices its answers into.
type Resolved struct {
	Source Source
	State  any
	Raw    string

	// Wire is what reaches the API and any merge wrapper. It is the raw bytes for a JSON mode, so
	// a large integer keeps its digits and an object keeps its key order, and the parsed value for
	// a text mode. State stays parsed, for the checks that need a Go value.
	Wire any
}

func fromStdin(req Query) (Resolved, error) {
	if req.Stdin == nil {
		return Resolved{Source: SourceNone}, nil
	}

	data, err := io.ReadAll(req.Stdin)
	if err != nil {
		return Resolved{}, fmt.Errorf("jev: reading stdin: %w", err)
	}

	// Zero bytes is not an empty state. Under cron or CI stdin may be an empty pipe, and sending
	// state "" would pay for a request the model cannot answer.
	if len(data) == 0 {
		return Resolved{Source: SourceNone}, nil
	}

	return fromText(SourceStdin, "stdin", string(data), req.Mode, true)
}

func fromText(source Source, label, text string, mode Mode, stripNewline bool) (Resolved, error) {
	if !utf8.ValidString(text) {
		return Resolved{}, fmt.Errorf("jev: %s is not valid UTF-8", label)
	}

	if mode == Text {
		if stripNewline {
			text = strings.TrimSuffix(text, "\n")
		}

		return Resolved{Source: source, State: text, Raw: text, Wire: text}, nil
	}

	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return Resolved{}, fmt.Errorf("jev: %s is not valid JSON: %w", label, err)
	}

	if err := CheckState(value); err != nil {
		return Resolved{}, fmt.Errorf("jev: %w", err)
	}

	return Resolved{
		Source: source, State: value, Raw: text, Wire: json.RawMessage(text),
	}, nil
}

// CheckState rejects a state the API would refuse, so a caller holding a state that never passed
// through Resolve can still check it. Its message carries no jev prefix, so a caller adds one.
func CheckState(value any) error {
	// The API refuses these, so rejecting them locally saves a request that would come back a 422.
	switch value.(type) {
	case string, map[string]any, []any:
		return nil
	case nil:
		return errors.New("state must be a string, object or array, got null")
	case bool:
		return errors.New("state must be a string, object or array, got boolean")
	default:
		return errors.New("state must be a string, object or array, got number")
	}
}
