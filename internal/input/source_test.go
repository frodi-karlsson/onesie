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
		name       string
		req        input.Request
		wantSource input.Source
		wantState  any
		wantErr    bool
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
			wantErr: true,
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
