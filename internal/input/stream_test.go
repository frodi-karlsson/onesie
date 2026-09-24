package input_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/input"
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
			name: "should fail an empty or blank json string, an empty object and an empty array under jsonl",
			mode: input.JSONL,
			in:   "\"\"\n\"  \"\n{}\n[]\n{\"id\":1}\n",
			want: []want{
				{wantErr: true},
				{wantErr: true},
				{wantErr: true},
				{wantErr: true},
				{state: map[string]any{"id": 1.0}, raw: `{"id":1}`},
			},
		},
		{
			name: "should turn each csv row into an object keyed by the header",
			mode: input.CSV,
			in:   "id,body\n1,the site is down\n2,\"a, quoted\nvalue\"\n",
			want: []want{
				{state: map[string]any{"id": "1", "body": "the site is down"}, raw: `{"id":"1","body":"the site is down"}`},
				{state: map[string]any{"id": "2", "body": "a, quoted\nvalue"}, raw: `{"id":"2","body":"a, quoted\nvalue"}`},
			},
		},
		{
			name: "should split tsv on tabs and keep quotes as text",
			mode: input.TSV,
			in:   "id\tbody\n1\tsays \"hi\" twice\n",
			want: []want{
				{state: map[string]any{"id": "1", "body": `says "hi" twice`}, raw: `{"id":"1","body":"says \"hi\" twice"}`},
			},
		},
		{
			name: "should keep a leading quote as text under tsv",
			mode: input.TSV,
			in:   "id\tbody\n1\t\"quoted\" tail\n2\tnext\r\n",
			want: []want{
				{state: map[string]any{"id": "1", "body": `"quoted" tail`}, raw: `{"id":"1","body":"\"quoted\" tail"}`},
				{state: map[string]any{"id": "2", "body": "next"}, raw: `{"id":"2","body":"next"}`},
			},
		},
		{
			name: "should drop a byte order mark before the header",
			mode: input.CSV,
			in:   "\ufeffid,body\n1,x\n",
			want: []want{{state: map[string]any{"id": "1", "body": "x"}, raw: `{"id":"1","body":"x"}`}},
		},
		{
			name: "should fail a row with the wrong number of fields and carry on",
			mode: input.CSV,
			in:   "id,body\n1\n2,ok\n",
			want: []want{
				{wantErr: true},
				{state: map[string]any{"id": "2", "body": "ok"}, raw: `{"id":"2","body":"ok"}`},
			},
		},
		{
			name: "should yield nothing for a header with no rows",
			mode: input.CSV,
			in:   "id,body\n",
			want: nil,
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

	t.Run("should reject a request body that is not a json object", func(t *testing.T) {
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
	})

	t.Run("should count line numbers separately from the record index", func(t *testing.T) {
		t.Parallel()

		t.Run("should count lines past a skipped blank", func(t *testing.T) {
			t.Parallel()

			// Index and Line diverge as soon as a line is dropped, and Line is the one a message
			// must quote, since it is what the user can find in their file.
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
	})

	t.Run("should handle a line longer than the limit", func(t *testing.T) {
		t.Parallel()

		t.Run("should report an oversized line and keep reading", func(t *testing.T) {
			t.Parallel()

			// bufio.Scanner would stop the whole batch on a line this long, which is why Stream
			// reads through bufio.Reader.ReadLine instead.
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
	})

	t.Run("should surface a reader error", func(t *testing.T) {
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
			// onesie: stdin: reading stdin: disk fell over.
			if got := lineErr.Error(); got != "stdin: disk fell over" {
				t.Errorf("message = %q, want %q", got, "stdin: disk fell over")
			}
		})
	})

	t.Run("should carry json bytes verbatim regardless of mode", func(t *testing.T) {
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
	})
	t.Run("should keep the header order on the wire", func(t *testing.T) {
		t.Parallel()

		stream := input.NewStream(strings.NewReader("zebra,alpha\nz,a\n"), input.CSV, false)

		record, ok, err := stream.Next()
		if err != nil || !ok {
			t.Fatalf("Next = %v, %v", ok, err)
		}

		wire, isRaw := record.Wire.(json.RawMessage)
		if !isRaw || string(wire) != `{"zebra":"z","alpha":"a"}` {
			t.Errorf("wire = %v, want the header order", record.Wire)
		}

		if !slices.Equal(record.Header, []string{"zebra", "alpha"}) {
			t.Errorf("header = %v, want zebra, alpha", record.Header)
		}
	})

	t.Run("should report the line a failing row starts on", func(t *testing.T) {
		t.Parallel()

		stream := input.NewStream(strings.NewReader("id,body\n1,\"two\nlines\"\n3\n"), input.CSV, false)

		for range 2 {
			record, _, err := stream.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}

			if record.Err == nil {
				continue
			}

			if record.Err.Line != 4 {
				t.Errorf("line = %d, want 4", record.Err.Line)
			}
		}
	})

	for _, header := range []string{"id,,body\n", "id,id\n"} {
		t.Run("should refuse a header with a blank or repeated name: "+strings.TrimSpace(header), func(t *testing.T) {
			t.Parallel()

			_, _, err := input.NewStream(strings.NewReader(header+"1,2,3\n"), input.CSV, false).Next()

			var lineErr *input.LineError
			if !errors.As(err, &lineErr) || lineErr.Line != 1 {
				t.Errorf("error = %v, want a line 1 error", err)
			}
		})
	}

	t.Run("should stop at a csv row longer than the limit rather than read it all", func(t *testing.T) {
		t.Parallel()

		body := "id,body\n1,\"" + strings.Repeat("x", 9<<20)

		stream := input.NewStream(strings.NewReader(body), input.CSV, false)

		_, _, err := stream.Next()

		var lineErr *input.LineError
		if !errors.As(err, &lineErr) || !strings.Contains(err.Error(), "longer than the limit") {
			t.Errorf("error = %v, want a row too long error", err)
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("disk fell over")
}
