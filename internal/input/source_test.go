package input_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/input"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		req         input.Request
		wantSource  input.Source
		wantState   any
		wantErr     bool
		wantMessage string
	}{
		{
			name: "should read stdin when it is a pipe",
			req: input.Request{
				Mode:     input.Text,
				Stdin:    strings.NewReader("a ticket body"),
				StdinTTY: false,
			},
			wantSource: input.SourceStdin,
			wantState:  "a ticket body",
		},
		{
			name: "should strip one trailing newline under text",
			req: input.Request{
				Mode:  input.Text,
				Stdin: strings.NewReader("a ticket body\n"),
			},
			wantSource: input.SourceStdin,
			wantState:  "a ticket body",
		},
		{
			name: "should report no state for an empty pipe",
			req: input.Request{
				Mode:  input.Text,
				Stdin: strings.NewReader(""),
			},
			wantSource: input.SourceNone,
		},
		{
			name: "should report no state for a terminal",
			req: input.Request{
				Mode:     input.Text,
				Stdin:    strings.NewReader("ignored"),
				StdinTTY: true,
			},
			wantSource: input.SourceNone,
		},
		{
			name: "should read a terminal when state is a dash",
			req: input.Request{
				Mode:     input.Text,
				Stdin:    strings.NewReader("typed by hand"),
				StdinTTY: true,
				State:    "-",
				HasState: true,
			},
			wantSource: input.SourceStdin,
			wantState:  "typed by hand",
		},
		{
			name: "should prefer an explicit state over stdin",
			req: input.Request{
				Mode:     input.Text,
				Stdin:    strings.NewReader("ignored"),
				State:    "from the flag",
				HasState: true,
			},
			wantSource: input.SourceState,
			wantState:  "from the flag",
		},
		{
			name: "should keep a trailing newline given through --state",
			req: input.Request{
				Mode:     input.Text,
				State:    "kept\n",
				HasState: true,
			},
			wantSource: input.SourceState,
			wantState:  "kept\n",
		},
		{
			name: "should read the state from a file",
			req: input.Request{
				Mode:         input.Text,
				StateFile:    "ticket.txt",
				HasStateFile: true,
				ReadFile: func(string) ([]byte, error) {
					return []byte("a ticket body\n"), nil
				},
			},
			wantSource: input.SourceStateFile,
			wantState:  "a ticket body",
		},
		{
			name: "should report a missing state file",
			req: input.Request{
				Mode:         input.Text,
				StateFile:    "gone.txt",
				HasStateFile: true,
				ReadFile: func(string) ([]byte, error) {
					return nil, os.ErrNotExist
				},
			},
			wantErr: true,
		},
		{
			name: "should parse json mode into a value",
			req: input.Request{
				Mode:  input.JSON,
				Stdin: strings.NewReader(`{"id":7}`),
			},
			wantSource: input.SourceStdin,
			wantState:  map[string]any{"id": float64(7)},
		},
		{
			name: "should reject a bare number under json",
			req: input.Request{
				Mode:  input.JSON,
				Stdin: strings.NewReader("42"),
			},
			wantErr: true,
		},
		{
			name: "should reject null under json",
			req: input.Request{
				Mode:  input.JSON,
				Stdin: strings.NewReader("null"),
			},
			wantErr:     true,
			wantMessage: "jev: state must be a string, object or array, got null",
		},
		{
			name: "should reject a boolean under json",
			req: input.Request{
				Mode:  input.JSON,
				Stdin: strings.NewReader("true"),
			},
			wantErr: true,
		},
		{
			name: "should accept an array under json",
			req: input.Request{
				Mode:  input.JSON,
				Stdin: strings.NewReader(`["a"]`),
			},
			wantSource: input.SourceStdin,
			wantState:  []any{"a"},
		},
		{
			name: "should accept a bare string under json",
			req: input.Request{
				Mode:  input.JSON,
				Stdin: strings.NewReader(`"just a string"`),
			},
			wantSource: input.SourceStdin,
			wantState:  "just a string",
		},
		{
			name: "should reject invalid utf8 under text",
			req: input.Request{
				Mode:  input.Text,
				Stdin: strings.NewReader("bad \xff byte"),
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := input.Resolve(tc.req)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
				}

				if tc.wantMessage != "" && err.Error() != tc.wantMessage {
					t.Errorf("error = %q, want %q", err.Error(), tc.wantMessage)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got.Source != tc.wantSource {
				t.Errorf("source = %v, want %v", got.Source, tc.wantSource)
			}

			if tc.wantState == nil {
				return
			}

			if !equalJSON(t, got.State, tc.wantState) {
				t.Errorf("state = %#v, want %#v", got.State, tc.wantState)
			}
		})
	}
}

func equalJSON(t *testing.T, got, want any) bool {
	t.Helper()

	gotBytes, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshalling got: %v", err)
	}

	wantBytes, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshalling want: %v", err)
	}

	return string(gotBytes) == string(wantBytes)
}

func TestParseMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		flag     string
		wantMode input.Mode
		wantErr  string
	}{
		{name: "should default an empty flag to text", flag: "", wantMode: input.Text},
		{name: "should parse text", flag: "text", wantMode: input.Text},
		{name: "should parse json", flag: "json", wantMode: input.JSON},
		{name: "should parse jsonl", flag: "jsonl", wantMode: input.JSONL},
		{name: "should parse lines", flag: "lines", wantMode: input.Lines},
		{
			name:    "should report request as not available yet",
			flag:    "request",
			wantErr: "jev: -i request is not available yet",
		},
		{
			name:    "should reject an unknown mode",
			flag:    "yaml",
			wantErr: "jev: -i takes text, json, jsonl or lines, got 'yaml'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mode, err := input.ParseMode(tc.flag)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got none", tc.wantErr)
				}

				if err.Error() != tc.wantErr {
					t.Errorf("error = %q, want %q", err.Error(), tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if mode != tc.wantMode {
				t.Errorf("mode = %v, want %v", mode, tc.wantMode)
			}
		})
	}
}

func TestModeStreaming(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode input.Mode
		want bool
	}{
		{name: "should not stream under text", mode: input.Text},
		{name: "should not stream under json", mode: input.JSON},
		{name: "should stream under jsonl", mode: input.JSONL, want: true},
		{name: "should stream under lines", mode: input.Lines, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.mode.Streaming(); got != tc.want {
				t.Errorf("Streaming() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveWire(t *testing.T) {
	t.Parallel()

	const big = `{"ticket_id":12345678901234567890,"zebra":1,"alpha":2}`

	tests := []struct {
		name string
		mode input.Mode
		text string
		want string
	}{
		{
			name: "should carry json bytes verbatim so a large integer keeps its digits",
			mode: input.JSON,
			text: big,
			want: big,
		},
		{
			name: "should carry the text itself in a text mode",
			mode: input.Text,
			text: "a ticket",
			want: `"a ticket"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := input.Resolve(input.Request{
				Mode:     tc.mode,
				State:    tc.text,
				HasState: true,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			encoded, err := json.Marshal(got.Wire)
			if err != nil {
				t.Fatalf("marshalling wire: %v", err)
			}

			if string(encoded) != tc.want {
				t.Errorf("wire = %s, want %s", encoded, tc.want)
			}
		})
	}
}
