package input_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/input"
)

func TestStream(t *testing.T) {
	t.Parallel()

	type want struct {
		state   any
		raw     string
		wantErr bool
	}

	tests := []struct {
		name      string
		mode      input.Mode
		skipBlank bool
		in        string
		want      []want
	}{
		{
			name: "should yield one record per line under lines",
			mode: input.Lines,
			in:   "first\nsecond\n",
			want: []want{{state: "first", raw: "first"}, {state: "second", raw: "second"}},
		},
		{
			name: "should not create a record for the trailing newline",
			mode: input.Lines,
			in:   "only\n",
			want: []want{{state: "only", raw: "only"}},
		},
		{
			name: "should yield nothing for empty input",
			mode: input.Lines,
			in:   "",
			want: nil,
		},
		{
			name: "should yield a final line with no trailing newline",
			mode: input.Lines,
			in:   "first\nsecond",
			want: []want{{state: "first", raw: "first"}, {state: "second", raw: "second"}},
		},
		{
			name: "should parse each line as json under jsonl",
			mode: input.JSONL,
			in:   "{\"id\":1}\n[\"a\"]\n",
			want: []want{
				{state: map[string]any{"id": float64(1)}, raw: `{"id":1}`},
				{state: []any{"a"}, raw: `["a"]`},
			},
		},
		{
			name: "should report a blank line as an input error",
			mode: input.Lines,
			in:   "first\n\nthird\n",
			want: []want{
				{state: "first", raw: "first"},
				{wantErr: true, raw: ""},
				{state: "third", raw: "third"},
			},
		},
		{
			name: "should report a whitespace only line as an input error",
			mode: input.Lines,
			in:   "first\n   \n",
			want: []want{
				{state: "first", raw: "first"},
				{wantErr: true},
			},
		},
		{
			name:      "should drop a blank line under skip blank",
			mode:      input.Lines,
			skipBlank: true,
			in:        "first\n\nthird\n",
			want:      []want{{state: "first", raw: "first"}, {state: "third", raw: "third"}},
		},
		{
			name: "should report a malformed json line as an input error and continue",
			mode: input.JSONL,
			in:   "{\"id\":1}\nnot json\n{\"id\":3}\n",
			want: []want{
				{state: map[string]any{"id": float64(1)}, raw: `{"id":1}`},
				{wantErr: true, raw: "not json"},
				{state: map[string]any{"id": float64(3)}, raw: `{"id":3}`},
			},
		},
		{
			name: "should reject a json line whose value is the wrong type",
			mode: input.JSONL,
			in:   "42\n\"ok\"\n",
			want: []want{
				{wantErr: true, raw: "42"},
				{state: "ok", raw: `"ok"`},
			},
		},
		{
			name: "should report invalid utf8 as an input error",
			mode: input.Lines,
			in:   "good\nbad \xff byte\n",
			want: []want{
				{state: "good", raw: "good"},
				{wantErr: true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stream := input.NewStream(strings.NewReader(tc.in), tc.mode, tc.skipBlank)

			var got []want

			for {
				record, ok, err := stream.Next()
				if err != nil {
					t.Fatalf("unexpected read error: %v", err)
				}

				if !ok {
					break
				}

				got = append(got, want{
					state: record.State, raw: record.Raw,
					wantErr: record.Err != nil,
				})
			}

			if len(got) != len(tc.want) {
				t.Fatalf("records = %d, want %d: %+v", len(got), len(tc.want), got)
			}

			for i := range got {
				if got[i].wantErr != tc.want[i].wantErr {
					t.Errorf("record %d error = %v, want %v", i, got[i].wantErr, tc.want[i].wantErr)
				}

				if tc.want[i].wantErr {
					continue
				}

				if !equalJSON(t, got[i].state, tc.want[i].state) {
					t.Errorf("record %d state = %#v, want %#v", i, got[i].state, tc.want[i].state)
				}

				if got[i].raw != tc.want[i].raw {
					t.Errorf("record %d raw = %q, want %q", i, got[i].raw, tc.want[i].raw)
				}
			}
		})
	}
}

func TestStreamRequestBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{
			name: "should accept an object",
			in:   `{"state":"x","questions":{}}`,
		},
		{
			name: "should accept an object the api will reject, since the 422 is the answer",
			in:   `{"hello":1}`,
		},
		{
			name:    "should reject a number",
			in:      "12",
			wantErr: "line 1: a request body must be a JSON object, got number",
		},
		{
			name:    "should reject an array",
			in:      "[1,2,3]",
			wantErr: "line 1: a request body must be a JSON object, got array",
		},
		{
			name:    "should reject a string",
			in:      `"just a string"`,
			wantErr: "line 1: a request body must be a JSON object, got string",
		},
		{
			name:    "should reject a boolean",
			in:      "true",
			wantErr: "line 1: a request body must be a JSON object, got boolean",
		},
		{
			name:    "should reject null",
			in:      "null",
			wantErr: "line 1: a request body must be a JSON object, got null",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stream := input.NewStream(strings.NewReader(tc.in+"\n"), input.Request, false)

			record, ok, err := stream.Next()
			if err != nil {
				t.Fatalf("unexpected read error: %v", err)
			}

			if !ok {
				t.Fatal("expected a record, got none")
			}

			if tc.wantErr == "" {
				if record.Err != nil {
					t.Fatalf("unexpected record error: %v", record.Err)
				}

				if record.Raw != tc.in {
					t.Errorf("raw = %q, want %q", record.Raw, tc.in)
				}

				return
			}

			if record.Err == nil {
				t.Fatal("expected a record error, got none")
			}

			if record.Err.Error() != tc.wantErr {
				t.Errorf("error = %q, want %q", record.Err.Error(), tc.wantErr)
			}
		})
	}
}

func TestStreamLineNumbers(t *testing.T) {
	t.Parallel()

	t.Run("should count lines past a skipped blank", func(t *testing.T) {
		t.Parallel()

		// Index and Line diverge as soon as a line is dropped, and Line is the one a message must
		// quote, since it is what the user can find in their file.
		stream := input.NewStream(strings.NewReader("first\n\nthird\n"), input.Lines, true)

		var lines, indexes []int

		for {
			record, ok, err := stream.Next()
			if err != nil {
				t.Fatalf("unexpected read error: %v", err)
			}

			if !ok {
				break
			}

			lines = append(lines, record.Line)
			indexes = append(indexes, record.Index)
		}

		if len(lines) != 2 {
			t.Fatalf("records = %d, want 2", len(lines))
		}

		if lines[0] != 1 || lines[1] != 3 {
			t.Errorf("lines = %v, want 1 and 3", lines)
		}

		if indexes[0] != 0 || indexes[1] != 1 {
			t.Errorf("indexes = %v, want 0 and 1", indexes)
		}
	})
}

func TestStreamLongLine(t *testing.T) {
	t.Parallel()

	t.Run("should report an oversized line and keep reading", func(t *testing.T) {
		t.Parallel()

		// bufio.Scanner would stop the whole batch on a line this long, which is why Stream reads
		// through bufio.Reader.ReadLine instead.
		in := "first\n" + strings.Repeat("x", 9<<20) + "\nthird\n"
		stream := input.NewStream(strings.NewReader(in), input.Lines, false)

		var got []string

		for {
			record, ok, err := stream.Next()
			if err != nil {
				t.Fatalf("unexpected read error: %v", err)
			}

			if !ok {
				break
			}

			if record.Err != nil {
				got = append(got, record.Err.Error())

				continue
			}

			got = append(got, record.Raw)
		}

		want := []string{"first", "line 2: line is longer than the limit", "third"}
		if len(got) != len(want) {
			t.Fatalf("records = %d, want %d: %q", len(got), len(want), got)
		}

		for i := range got {
			if got[i] != want[i] {
				t.Errorf("record %d = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

func TestStreamReadError(t *testing.T) {
	t.Parallel()

	t.Run("should report a failing reader against stdin rather than a line", func(t *testing.T) {
		t.Parallel()

		stream := input.NewStream(failingReader{}, input.Lines, false)

		record, ok, err := stream.Next()
		if err == nil {
			t.Fatalf("expected a read error, got record %+v ok %v", record, ok)
		}

		if ok {
			t.Errorf("ok = true, want false")
		}

		var lineErr *input.LineError
		if !errors.As(err, &lineErr) {
			t.Fatalf("error = %T, want *input.LineError", err)
		}

		if lineErr.Line != 0 {
			t.Errorf("line = %d, want 0", lineErr.Line)
		}

		// Named once. The wrap used to add a spelling of its own, so the user read
		// jev: stdin: reading stdin: disk fell over.
		if got := lineErr.Error(); got != "stdin: disk fell over" {
			t.Errorf("message = %q, want %q", got, "stdin: disk fell over")
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("disk fell over")
}

func TestStreamWire(t *testing.T) {
	t.Parallel()

	const big = `{"ticket_id":12345678901234567890,"zebra":1,"alpha":2}`

	tests := []struct {
		name string
		mode input.Mode
		line string
		want string
	}{
		{
			name: "should carry json bytes verbatim so a large integer keeps its digits",
			mode: input.JSONL,
			line: big,
			want: big,
		},
		{
			name: "should carry json bytes verbatim so an object keeps its key order",
			mode: input.JSONL,
			line: `{"zebra":1,"alpha":2}`,
			want: `{"zebra":1,"alpha":2}`,
		},
		{
			name: "should carry the line itself in a text mode",
			mode: input.Lines,
			line: `{"zebra":1}`,
			want: `"{\"zebra\":1}"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stream := input.NewStream(strings.NewReader(tc.line+"\n"), tc.mode, false)

			record, ok, err := stream.Next()
			if err != nil || !ok {
				t.Fatalf("next = %v, %v", ok, err)
			}

			encoded, err := json.Marshal(record.Wire)
			if err != nil {
				t.Fatalf("marshalling wire: %v", err)
			}

			if string(encoded) != tc.want {
				t.Errorf("wire = %s, want %s", encoded, tc.want)
			}
		})
	}
}
