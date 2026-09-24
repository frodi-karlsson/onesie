package input

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// ErrEmptyState reports a state that is empty or only whitespace, which the model cannot answer.
var ErrEmptyState = errors.New("an empty state is a request the model cannot answer")

// Resolve decides where the state comes from and reads it. It performs no network call and reads
// stdin at most once.
func Resolve(req Query) (Resolved, error) {
	switch {
	case req.HasStateFile:
		body, err := req.ReadFile(req.StateFile)
		if err != nil {
			return Resolved{}, fmt.Errorf("onesie: reading %s: %w", req.StateFile, err)
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

func fromStdin(req Query) (Resolved, error) {
	if req.Stdin == nil {
		return Resolved{Source: SourceNone}, nil
	}

	data, err := io.ReadAll(req.Stdin)
	if err != nil {
		return Resolved{}, fmt.Errorf("onesie: reading stdin: %w", err)
	}

	// Zero bytes is not an empty state. Under cron or CI stdin may be an empty pipe, and sending
	// state "" would pay for a request the model cannot answer.
	if len(data) == 0 {
		return Resolved{Source: SourceNone}, nil
	}

	return fromText(SourceStdin, "stdin", string(data), req.Mode, true)
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

func fromText(source Source, label, text string, mode Mode, stripNewline bool) (Resolved, error) {
	if !utf8.ValidString(text) {
		return Resolved{}, fmt.Errorf("onesie: %s is not valid UTF-8", label)
	}

	if mode == Text {
		if stripNewline {
			text = strings.TrimSuffix(strings.TrimSuffix(text, "\r\n"), "\n")
		}

		if strings.TrimSpace(text) == "" {
			return Resolved{}, fmt.Errorf("onesie: %s is blank, %w", label, ErrEmptyState)
		}

		return Resolved{Source: source, State: text, Raw: text, Wire: text}, nil
	}

	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return Resolved{}, fmt.Errorf("onesie: %s is not valid JSON: %w", label, err)
	}

	if err := CheckState(value); err != nil {
		return Resolved{}, fmt.Errorf("onesie: %w", err)
	}

	return Resolved{
		Source: source, State: value, Raw: text, Wire: json.RawMessage(text),
	}, nil
}

// Resolved is the outcome. State is nil when Source is SourceNone, and Raw is the text the state
// was read from, which --merge splices its answers into.
type Resolved struct {
	Source Source
	State  any
	Raw    string

	// Wire is what reaches the API. It is the raw bytes for a JSON mode, keeping large integers and
	// key order, and the parsed value for a text mode.
	Wire any
}

// CheckState rejects a state the API would refuse or the model cannot answer, so a caller holding a
// state that never passed through Resolve can still check it. Its message carries no onesie prefix.
func CheckState(value any) error {
	// Rejected locally, since the API refuses the wrong types and the model cannot answer an empty one.
	switch typed := value.(type) {
	case string:
		return emptyUnless(strings.TrimSpace(typed) != "", "string")
	case map[string]any:
		return emptyUnless(len(typed) > 0, "object")
	case []any:
		return emptyUnless(len(typed) > 0, "array")
	case nil:
		return errors.New("state must be a string, object or array, got null")
	case bool:
		return errors.New("state must be a string, object or array, got boolean")
	default:
		return errors.New("state must be a string, object or array, got number")
	}
}

func emptyUnless(filled bool, noun string) error {
	if filled {
		return nil
	}

	return fmt.Errorf("empty %s, %w", noun, ErrEmptyState)
}
