package input_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/interrupt"
	"github.com/frodi-karlsson/onesie/internal/limits"
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
			name: "should preserve a data CR before the TSV terminator",
			mode: input.TSV,
			in:   "body\nx\r\r\n",
			want: []want{{state: map[string]any{"body": "x\r"}, raw: `{"body":"x\r"}`}},
		},
		{
			name: "should preserve a final CR without a following LF as data",
			mode: input.TSV,
			in:   "body\nx\r",
			want: []want{{state: map[string]any{"body": "x\r"}, raw: `{"body":"x\r"}`}},
		},
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
			name: "should refuse a json line whose value is the wrong type",
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

			stream := input.NewStream(strings.NewReader(tc.in), tc.mode, input.StreamOptions{SkipBlank: tc.skipBlank})

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

	t.Run("should name a json number too large to read in plain words", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			in   string
			want string
		}{
			{
				name: "should name the number under a key",
				in:   `{"id":1e400}`,
				want: "line 1: line holds the number 1e400, which is too large to read",
			},
			{
				name: "should name the number in an array",
				in:   `[1e400]`,
				want: "line 1: line holds the number 1e400, which is too large to read",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				record, ok, err := input.NewStream(strings.NewReader(tc.in+"\n"), input.JSONL, input.StreamOptions{}).Next()
				if err != nil || !ok {
					t.Fatalf("Next = %v, %v, want one record", ok, err)
				}

				if record.Err == nil || record.Err.Error() != tc.want {
					t.Errorf("error = %v, want %q", record.Err, tc.want)
				}
			})
		}
	})

	t.Run("should forward a request line as written, since onesie-go reads the body", func(t *testing.T) {
		t.Parallel()

		for _, in := range []string{`{"state":"x","questions":{}}`, "12", "not json", `{"a":"\ud800"}`} {
			record, ok, err := input.NewStream(strings.NewReader(in+"\n"), input.Request, input.StreamOptions{}).Next()
			if err != nil || !ok || record.Err != nil || record.Raw != in {
				t.Errorf("%q: record = %+v, %v, %v, want it forwarded as written", in, record, ok, err)
			}
		}
	})

	t.Run("should split a csv by its delimiter", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			mode      input.Mode
			in        string
			delimiter input.Delimiter
			want      map[string]any
			wantComma rune
		}{
			{
				name: "should split a csv on a given semicolon", mode: input.CSV, in: "id;body\n1;a,b\n",
				delimiter: input.Delimiter{Comma: ';'}, want: map[string]any{"id": "1", "body": "a,b"}, wantComma: ';',
			},
			{
				name: "should detect a semicolon under auto", mode: input.CSV, in: "id;body\n1;a,b\n",
				delimiter: input.Delimiter{Detect: true}, want: map[string]any{"id": "1", "body": "a,b"}, wantComma: ';',
			},
			{
				name: "should detect from the first line that is not blank under auto", mode: input.CSV,
				in: "\n\nid;text\n1;a\n", delimiter: input.Delimiter{Detect: true},
				want: map[string]any{"id": "1", "text": "a"}, wantComma: ';',
			},
			{
				name: "should skip blank crlf lines before the header under auto", mode: input.CSV,
				in: "\r\n\r\nid;text\r\n1;a\r\n", delimiter: input.Delimiter{Detect: true},
				want: map[string]any{"id": "1", "text": "a"}, wantComma: ';',
			},
			{
				name: "should keep commas when no delimiter is given", mode: input.CSV, in: "id,body\n1,a;b\n",
				want: map[string]any{"id": "1", "body": "a;b"}, wantComma: ',',
			},
			{
				name: "should ignore a delimiter for tsv", mode: input.TSV, in: "id\tbody\n1\ta;b\n",
				delimiter: input.Delimiter{Comma: ';'}, want: map[string]any{"id": "1", "body": "a;b"}, wantComma: ',',
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				stream := input.NewStream(strings.NewReader(tc.in), tc.mode, input.StreamOptions{Delimiter: tc.delimiter})

				record, ok, err := stream.Next()
				if err != nil || !ok || record.Err != nil {
					t.Fatalf("Next = %+v, %v, %v, want a record", record, ok, err)
				}

				if !equalJSON(t, record.State, tc.want) {
					t.Errorf("state = %#v, want %#v", record.State, tc.want)
				}

				if got := stream.Comma(); got != tc.wantComma {
					t.Errorf("Comma = %q, want %q", got, tc.wantComma)
				}
			})
		}
	})

	t.Run("should read nothing until Next is called", func(t *testing.T) {
		t.Parallel()

		watched := &watchedReader{Reader: strings.NewReader("id;body\n1;a\n")}
		stream := input.NewStream(watched, input.CSV, input.StreamOptions{Delimiter: input.Delimiter{Detect: true}})

		if watched.reads != 0 {
			t.Fatalf("NewStream read the input %d times, want none", watched.reads)
		}

		if _, ok, err := stream.Next(); err != nil || !ok || watched.reads == 0 {
			t.Errorf("Next = %v, %v after %d reads, want a record read from the input", ok, err, watched.reads)
		}
	})

	t.Run("should count line numbers separately from the record index", func(t *testing.T) {
		t.Parallel()

		t.Run("should count lines past a skipped blank", func(t *testing.T) {
			t.Parallel()

			stream := input.NewStream(strings.NewReader("first\n\nthird\n"), input.Lines, input.StreamOptions{SkipBlank: true})

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
			stream := input.NewStream(strings.NewReader(in), input.Lines, input.StreamOptions{})

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

			want := []string{"first", "line 2: line is longer than 8388608 bytes, the max-line-bytes limit", "third"}
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

			stream := input.NewStream(failingReader{}, input.Lines, input.StreamOptions{})

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

				stream := input.NewStream(strings.NewReader(tc.line+"\n"), tc.mode, input.StreamOptions{})

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

		stream := input.NewStream(strings.NewReader("zebra,alpha\nz,a\n"), input.CSV, input.StreamOptions{})

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

		stream := input.NewStream(strings.NewReader("id,body\n1,\"two\nlines\"\n3\n"), input.CSV, input.StreamOptions{})

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

			_, _, err := input.NewStream(strings.NewReader(header+"1,2,3\n"), input.CSV, input.StreamOptions{}).Next()

			var lineErr *input.LineError
			if !errors.As(err, &lineErr) || lineErr.Line != 1 {
				t.Errorf("error = %v, want a line 1 error", err)
			}
		})
	}

	t.Run("should stop at a csv row longer than the limit rather than read it all", func(t *testing.T) {
		t.Parallel()

		body := "id,body\n1,\"" + strings.Repeat("x", 9<<20)

		stream := input.NewStream(strings.NewReader(body), input.CSV, input.StreamOptions{})

		_, _, err := stream.Next()

		var lineErr *input.LineError
		if !errors.As(err, &lineErr) || !strings.Contains(err.Error(), "the max-line-bytes limit") {
			t.Errorf("error = %v, want a row too long error", err)
		}
	})

	t.Run("should read an -i request line past max-line-bytes up to max-request-line-bytes", func(t *testing.T) {
		t.Parallel()

		long := `{"state":"` + strings.Repeat("a", limits.MaxLineBytes) + `","questions":{}}`
		stream := input.NewStream(strings.NewReader(long+"\n"), input.Request, input.StreamOptions{})

		rec, ok, err := stream.Next()
		if err != nil || !ok {
			t.Fatalf("Next = %v, %v", ok, err)
		}

		if rec.Err != nil {
			t.Fatalf("record error = %v, want the line read whole", rec.Err)
		}
	})

	t.Run("should still cap a jsonl line at max-line-bytes", func(t *testing.T) {
		t.Parallel()

		long := `"` + strings.Repeat("a", limits.MaxLineBytes) + `"`
		stream := input.NewStream(strings.NewReader(long+"\n"), input.JSONL, input.StreamOptions{})

		rec, _, err := stream.Next()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if rec.Err == nil {
			t.Fatal("record error = nil, want the line refused as too long")
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("disk fell over")
}

func (r *watchedReader) Read(p []byte) (int, error) {
	r.reads++

	return r.Reader.Read(p)
}

type watchedReader struct {
	io.Reader

	reads int
}

func TestReadHeader(t *testing.T) {
	t.Run("should refuse invalid header bytes before returning records", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			mode      input.Mode
			separator byte
		}{
			{"should refuse CSV headers", input.CSV, ','},
			{"should refuse TSV headers", input.TSV, '\t'},
		} {
			t.Run(tc.name, func(t *testing.T) {
				text := string([]byte{0xff, tc.separator, 0xfe, '\n', 'a', tc.separator, 'b', '\n'})
				want := "line 1: the " + tc.mode.String() + " header name at column 1 is not valid UTF-8"
				stream := input.NewStream(strings.NewReader(text), tc.mode, input.StreamOptions{})
				h, err := stream.ReadHeader()
				var lineErr *input.LineError
				if !errors.As(err, &lineErr) || err.Error() != want || h.Names != nil || h.Separator != 0 {
					t.Fatalf("header: %#v, %v", h, err)
				}
				for _, reader := range []*input.Stream{stream, input.NewStream(strings.NewReader(text), tc.mode, input.StreamOptions{})} {
					rec, ok, err := reader.Next()
					if err == nil || err.Error() != want || ok || rec.Raw != "" || rec.State != nil {
						t.Fatalf("record: %#v, %v, %v", rec, ok, err)
					}
				}
			})
		}
	})
	t.Run("should leave invalid data bytes as record errors", func(t *testing.T) {
		for _, mode := range []input.Mode{input.CSV, input.TSV} {
			stream := input.NewStream(strings.NewReader("body\n"+string([]byte{0xff, '\n'})), mode, input.StreamOptions{})
			if _, err := stream.ReadHeader(); err != nil {
				t.Fatal(err)
			}
			rec, ok, err := stream.Next()
			if err != nil || !ok || rec.Err == nil || rec.Line != 2 || !strings.Contains(rec.Err.Error(), "row is not valid UTF-8") {
				t.Fatalf("record: %#v, %v, %v", rec, ok, err)
			}
		}
	})
	t.Run("should read a detected header while the producer waits to send data", func(t *testing.T) {
		r, w := io.Pipe()
		t.Cleanup(func() {
			_ = r.Close()
			_ = w.Close()
		})
		stream := input.NewStream(r, input.CSV, input.StreamOptions{Delimiter: input.Delimiter{Detect: true}})
		written := make(chan error, 1)
		go func() {
			_, err := io.WriteString(w, "id;body\n")
			written <- err
		}()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		h, err := interrupt.Wait(ctx, stream.ReadHeader)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(h.Names, []string{"id", "body"}) || h.Separator != ';' {
			t.Fatalf("header: %#v", h)
		}
		if writeErr := <-written; writeErr != nil {
			t.Fatal(writeErr)
		}
		go func() {
			_, writeErr := io.WriteString(w, "1;hello\n")
			written <- writeErr
		}()
		rec, ok, err := stream.Next()
		if err != nil || !ok || rec.Err != nil || rec.Line != 2 || rec.Raw != `{"id":"1","body":"hello"}` {
			t.Fatalf("record: %#v, %v, %v", rec, ok, err)
		}
		if writeErr := <-written; writeErr != nil {
			t.Fatal(writeErr)
		}
	})
	tests := []struct {
		name    string
		mode    input.Mode
		text    string
		names   []string
		comma   rune
		wantErr string
	}{
		{"should cache empty input", input.CSV, "", nil, 0, "EOF"},
		{"should read header only input", input.CSV, "id,body\n", []string{"id", "body"}, ',', ""},
		{"should read non ASCII headers", input.CSV, "namn,ålder\n", []string{"namn", "ålder"}, ',', ""},
		{"should strip a header BOM", input.CSV, "\ufeffid,body\n", []string{"id", "body"}, ',', ""},
		{"should read TSV headers", input.TSV, "id\tbody\n", []string{"id", "body"}, '\t', ""},
		{"should read multiline headers", input.CSV, "\"first\nname\",body\n", []string{"first\nname", "body"}, ',', ""},
		{"should cache duplicate header errors", input.CSV, "id,id\nnext,row\n", nil, 0, "line 1: the header names 'id' twice"},
		{"should cache blank header errors", input.CSV, "id, \nnext,row\n", nil, 0, "line 1: the header has a blank column name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stream := input.NewStream(strings.NewReader(tc.text), tc.mode, input.StreamOptions{})
			var firstErr error
			for i := 0; i < 3; i++ {
				h, err := stream.ReadHeader()
				if tc.wantErr != "" {
					if err == nil || err.Error() != tc.wantErr {
						t.Fatalf("error: %v", err)
					}
					if i > 0 && !errors.Is(err, firstErr) {
						t.Fatal("error was not cached")
					}
					firstErr = err
					continue
				}
				if err != nil || !slices.Equal(h.Names, tc.names) || h.Separator != tc.comma {
					t.Fatalf("header: %#v, %v", h, err)
				}
				h.Names[0] = "changed"
			}
			rec, ok, err := stream.Next()
			if tc.wantErr != "" && tc.wantErr != "EOF" {
				if !errors.Is(err, firstErr) {
					t.Fatalf("Next error: %v", err)
				}
				return
			}
			if err != nil || ok {
				t.Fatalf("Next: %#v, %v, %v", rec, ok, err)
			}
		})
	}
	t.Run("should preserve the header after Next and leave malformed data to Next", func(t *testing.T) {
		stream := input.NewStream(strings.NewReader("id,body\n1,hello\n2\n"), input.CSV, input.StreamOptions{})
		_, ok, err := stream.Next()
		if err != nil || !ok {
			t.Fatal(err)
		}
		h, err := stream.ReadHeader()
		if err != nil || !slices.Equal(h.Names, []string{"id", "body"}) {
			t.Fatalf("header: %#v, %v", h, err)
		}
		rec, ok, err := stream.Next()
		if err != nil || !ok || rec.Err == nil || rec.Line != 3 {
			t.Fatalf("record: %#v, %v, %v", rec, ok, err)
		}
	})
	t.Run("should refuse other modes without consuming input", func(t *testing.T) {
		for _, mode := range []input.Mode{input.Lines, input.JSONL, input.Request} {
			reader := strings.NewReader("hello\n")
			stream := input.NewStream(reader, mode, input.StreamOptions{})
			if _, err := stream.ReadHeader(); err == nil {
				t.Fatal("expected error")
			}
			if reader.Len() != len("hello\n") {
				t.Fatal("consumed input")
			}
		}
	})
	t.Run("should allow cancellation while the producer has sent no header", func(t *testing.T) {
		r, w := io.Pipe()
		t.Cleanup(func() {
			_ = r.Close()
			_ = w.Close()
		})
		stream := input.NewStream(r, input.CSV, input.StreamOptions{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		started := make(chan struct{})
		finished := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := interrupt.Wait(ctx, func() (input.Header, error) {
				close(started)
				defer close(finished)
				return stream.ReadHeader()
			})
			done <- err
		}()
		<-started
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("cancellation blocked")
		}
		_ = w.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("read did not finish")
		}
	})
}
