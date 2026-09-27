package input_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/input"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		req         input.Query
		wantSource  input.Source
		wantState   any
		wantErr     bool
		wantMessage string
	}{
		{
			name: "should read stdin when it is a pipe",
			req: input.Query{
				Mode:     input.Text,
				Stdin:    strings.NewReader("a ticket body"),
				StdinTTY: false,
			},
			wantSource: input.SourceStdin,
			wantState:  "a ticket body",
		},
		{
			name: "should strip one trailing windows line ending under text",
			req: input.Query{
				Mode:  input.Text,
				Stdin: strings.NewReader("a ticket body\r\n"),
			},
			wantSource: input.SourceStdin,
			wantState:  "a ticket body",
		},
		{
			name: "should strip one trailing newline under text",
			req: input.Query{
				Mode:  input.Text,
				Stdin: strings.NewReader("a ticket body\n"),
			},
			wantSource: input.SourceStdin,
			wantState:  "a ticket body",
		},
		{
			name: "should report no state for an empty pipe",
			req: input.Query{
				Mode:  input.Text,
				Stdin: strings.NewReader(""),
			},
			wantSource: input.SourceNone,
		},
		{
			name: "should report no state for a terminal",
			req: input.Query{
				Mode:     input.Text,
				Stdin:    strings.NewReader("ignored"),
				StdinTTY: true,
			},
			wantSource: input.SourceNone,
		},
		{
			name: "should read a terminal when state is a dash",
			req: input.Query{
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
			req: input.Query{
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
			req: input.Query{
				Mode:     input.Text,
				State:    "kept\n",
				HasState: true,
			},
			wantSource: input.SourceState,
			wantState:  "kept\n",
		},
		{
			name: "should read the state from a file",
			req: input.Query{
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
			req: input.Query{
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
			req: input.Query{
				Mode:  input.JSON,
				Stdin: strings.NewReader(`{"id":7}`),
			},
			wantSource: input.SourceStdin,
			wantState:  map[string]any{"id": float64(7)},
		},
		{
			name: "should reject a bare number under json",
			req: input.Query{
				Mode:  input.JSON,
				Stdin: strings.NewReader("42"),
			},
			wantErr: true,
		},
		{
			name: "should reject null under json",
			req: input.Query{
				Mode:  input.JSON,
				Stdin: strings.NewReader("null"),
			},
			wantErr:     true,
			wantMessage: "onesie: state must be a string, object or array, got null",
		},
		{
			name: "should reject an empty json string on --state",
			req: input.Query{
				Mode:     input.JSON,
				State:    `""`,
				HasState: true,
			},
			wantErr:     true,
			wantMessage: "onesie: empty string, an empty state is a request the model cannot answer",
		},
		{
			name: "should reject a json string of spaces on stdin",
			req: input.Query{
				Mode:  input.JSON,
				Stdin: strings.NewReader(`"  "` + "\n"),
			},
			wantErr:     true,
			wantMessage: "onesie: empty string, an empty state is a request the model cannot answer",
		},
		{
			name: "should reject an empty object under json",
			req: input.Query{
				Mode:  input.JSON,
				Stdin: strings.NewReader("{}"),
			},
			wantErr:     true,
			wantMessage: "onesie: empty object, an empty state is a request the model cannot answer",
		},
		{
			name: "should reject an empty array in a state file under json",
			req: input.Query{
				Mode:         input.JSON,
				StateFile:    "empty.json",
				HasStateFile: true,
				ReadFile: func(string) ([]byte, error) {
					return []byte("[ ]\n"), nil
				},
			},
			wantErr:     true,
			wantMessage: "onesie: empty array, an empty state is a request the model cannot answer",
		},
		{
			name: "should reject a boolean under json",
			req: input.Query{
				Mode:  input.JSON,
				Stdin: strings.NewReader("true"),
			},
			wantErr: true,
		},
		{
			name: "should accept an array under json",
			req: input.Query{
				Mode:  input.JSON,
				Stdin: strings.NewReader(`["a"]`),
			},
			wantSource: input.SourceStdin,
			wantState:  []any{"a"},
		},
		{
			name: "should accept a bare string under json",
			req: input.Query{
				Mode:  input.JSON,
				Stdin: strings.NewReader(`"just a string"`),
			},
			wantSource: input.SourceStdin,
			wantState:  "just a string",
		},
		{
			name: "should reject an empty --state under text",
			req: input.Query{
				Mode:     input.Text,
				State:    "",
				HasState: true,
			},
			wantErr:     true,
			wantMessage: "onesie: --state is blank, an empty state is a request the model cannot answer",
		},
		{
			name: "should reject a --state of spaces under text",
			req: input.Query{
				Mode:     input.Text,
				State:    "  ",
				HasState: true,
			},
			wantErr:     true,
			wantMessage: "onesie: --state is blank, an empty state is a request the model cannot answer",
		},
		{
			name: "should reject an empty state file under text",
			req: input.Query{
				Mode:         input.Text,
				StateFile:    "empty.txt",
				HasStateFile: true,
				ReadFile: func(string) ([]byte, error) {
					return nil, nil
				},
			},
			wantErr: true,
			wantMessage: "onesie: --state-file 'empty.txt' is blank, " +
				"an empty state is a request the model cannot answer",
		},
		{
			name: "should reject a stdin holding only a newline under text",
			req: input.Query{
				Mode:  input.Text,
				Stdin: strings.NewReader("\n"),
			},
			wantErr:     true,
			wantMessage: "onesie: stdin is blank, an empty state is a request the model cannot answer",
		},
		{
			name: "should reject invalid utf8 under text",
			req: input.Query{
				Mode:  input.Text,
				Stdin: strings.NewReader("bad \xff byte"),
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := input.Resolve(t.Context(), tc.req)

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

	t.Run("should stop waiting on stdin once the context ends", func(t *testing.T) {
		t.Parallel()

		held, write := io.Pipe()
		t.Cleanup(func() { _ = write.Close() })

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := input.Resolve(ctx, input.Query{Mode: input.Text, Stdin: held})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("should hint on stdin only when nothing arrives in time", func(t *testing.T) {
		t.Parallel()

		const hint = "onesie: waiting for text on stdin. Pass --state, or close stdin.\n"

		tests := []struct {
			name      string
			tty       bool
			fire      bool
			firstPart bool
			wantHint  string
			wantSleep bool
		}{
			{name: "should hint once when stdin stays silent", fire: true, wantHint: hint, wantSleep: true},
			{name: "should not hint when the text arrives in time", wantSleep: true},
			{name: "should not hint once the first bytes arrived in time", fire: true, firstPart: true, wantSleep: true},
			{name: "should not hint while reading a terminal", tty: true, fire: true},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				written := make(chan struct{})
				hinted := &signalWriter{signal: written}
				clock := &fakeClock{fire: tc.fire}

				var stdin io.Reader = strings.NewReader("late text")

				switch {
				case tc.firstPart:
					clock.after = make(chan struct{})
					stdin = &splitReader{parts: []string{"late ", "text"}, between: clock.after, fired: clock.fired()}
				case tc.fire && !tc.tty:
					stdin = &gatedReader{data: strings.NewReader("late text"), gate: written}
				}

				got, err := input.Resolve(t.Context(), input.Query{
					Mode: input.Text, Stdin: stdin, StdinTTY: tc.tty, State: "-", HasState: tc.tty,
					Hint: hinted, Clock: clock,
				})
				if err != nil {
					t.Fatalf("Resolve: %v", err)
				}

				if got.State != "late text" {
					t.Errorf("state = %#v, want %q", got.State, "late text")
				}

				if hinted.String() != tc.wantHint {
					t.Errorf("hint = %q, want %q", hinted.String(), tc.wantHint)
				}

				if slept := clock.slept(); tc.wantSleep && slept != 2*time.Second || !tc.wantSleep && slept != 0 {
					t.Errorf("slept %v, want the wait only off a terminal, of 2s", slept)
				}
			})
		}
	})

	t.Run("should hint to close stdin when --state - asked for it", func(t *testing.T) {
		t.Parallel()

		written := make(chan struct{})
		hinted := &signalWriter{signal: written}

		_, err := input.Resolve(t.Context(), input.Query{
			Mode: input.Text, State: "-", HasState: true,
			Stdin: &gatedReader{data: strings.NewReader("late text"), gate: written},
			Hint:  hinted, Clock: &fakeClock{fire: true},
		})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		const want = "onesie: waiting for text on stdin. Close stdin when the text is complete.\n"
		if hinted.String() != want {
			t.Errorf("hint = %q, want %q", hinted.String(), want)
		}
	})

	t.Run("should keep a good read when the hint cannot be written", func(t *testing.T) {
		t.Parallel()

		written := make(chan struct{})

		got, err := input.Resolve(t.Context(), input.Query{
			Mode:  input.Text,
			Stdin: &gatedReader{data: strings.NewReader("late text"), gate: written},
			Hint:  failingWriter{signal: written},
			Clock: &fakeClock{fire: true},
		})
		if err != nil {
			t.Fatalf("Resolve: %v, want the hint's write error dropped", err)
		}

		if got.State != "late text" {
			t.Errorf("state = %#v, want %q", got.State, "late text")
		}
	})

	t.Run("should carry the wire form of the state", func(t *testing.T) {
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

				got, err := input.Resolve(t.Context(), input.Query{
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
	})
}

type fakeClock struct {
	fire  bool
	after chan struct{}

	mu       sync.Mutex
	duration time.Duration
	firedCh  chan struct{}
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.duration = d
	c.mu.Unlock()

	if c.after != nil {
		<-c.after
	}

	if !c.fire {
		<-ctx.Done()

		return ctx.Err()
	}

	close(c.fired())

	return nil
}

func (c *fakeClock) fired() chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.firedCh == nil {
		c.firedCh = make(chan struct{})
	}

	return c.firedCh
}

func (c *fakeClock) slept() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.duration
}

type signalWriter struct {
	signal chan struct{}

	mu   sync.Mutex
	text strings.Builder
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.text.Len() == 0 {
		close(w.signal)
	}

	return w.text.Write(p)
}

func (w *signalWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.text.String()
}

type failingWriter struct {
	signal chan struct{}
}

func (w failingWriter) Write([]byte) (int, error) {
	close(w.signal)

	return 0, errors.New("stderr is closed")
}

type gatedReader struct {
	data io.Reader
	gate <-chan struct{}
}

func (r *gatedReader) Read(p []byte) (int, error) {
	select {
	case <-r.gate:
	case <-time.After(5 * time.Second):
	}

	return r.data.Read(p)
}

type splitReader struct {
	parts   []string
	between chan struct{}
	fired   <-chan struct{}
	read    int
}

func (r *splitReader) Read(p []byte) (int, error) {
	if r.read == len(r.parts) {
		return 0, io.EOF
	}

	if r.read == 1 {
		close(r.between)
		<-r.fired
	}

	n := copy(p, r.parts[r.read])
	r.read++

	return n, nil
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

func TestCheckState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   any
		wantErr string
	}{
		{name: "should accept a string", value: "a ticket"},
		{name: "should accept an object", value: map[string]any{"id": 1.0}},
		{name: "should accept an array", value: []any{"a"}},
		{
			name:    "should reject an empty string",
			value:   "",
			wantErr: "empty string, an empty state is a request the model cannot answer",
		},
		{
			name:    "should reject a string of whitespace",
			value:   " \t\n",
			wantErr: "empty string, an empty state is a request the model cannot answer",
		},
		{
			name:    "should reject an empty object",
			value:   map[string]any{},
			wantErr: "empty object, an empty state is a request the model cannot answer",
		},
		{
			name:    "should reject an empty array",
			value:   []any{},
			wantErr: "empty array, an empty state is a request the model cannot answer",
		},
		{
			name:    "should reject null",
			value:   nil,
			wantErr: "state must be a string, object or array, got null",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := input.CheckState(tc.value)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("CheckState(%v) = %v, want nil", tc.value, err)
				}

				return
			}

			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("CheckState(%v) = %v, want %q", tc.value, err, tc.wantErr)
			}

			if strings.HasPrefix(tc.wantErr, "empty") && !errors.Is(err, input.ErrEmptyState) {
				t.Errorf("CheckState(%v) = %v, want it to wrap ErrEmptyState", tc.value, err)
			}
		})
	}
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
		{name: "should parse request", flag: "request", wantMode: input.Request},
		{name: "should parse csv", flag: "csv", wantMode: input.CSV},
		{name: "should parse tsv", flag: "tsv", wantMode: input.TSV},
		{
			name:    "should reject an unknown mode",
			flag:    "yaml",
			wantErr: "onesie: -i takes text, json, jsonl, lines, csv, tsv or request, got 'yaml'",
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

func TestMode_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode input.Mode
		want string
	}{
		{name: "should name text as -i spells it", mode: input.Text, want: "text"},
		{name: "should name json as -i spells it", mode: input.JSON, want: "json"},
		{name: "should name jsonl as -i spells it", mode: input.JSONL, want: "jsonl"},
		{name: "should name lines as -i spells it", mode: input.Lines, want: "lines"},
		{name: "should name request as -i spells it", mode: input.Request, want: "request"},
		{name: "should name csv as -i spells it", mode: input.CSV, want: "csv"},
		{name: "should name tsv as -i spells it", mode: input.TSV, want: "tsv"},
		{name: "should name a mode outside the set by its number", mode: input.Mode(99), want: "Mode(99)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.mode.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
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
		{name: "should stream under request", mode: input.Request, want: true},
		{name: "should stream under csv", mode: input.CSV, want: true},
		{name: "should stream under tsv", mode: input.TSV, want: true},
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
