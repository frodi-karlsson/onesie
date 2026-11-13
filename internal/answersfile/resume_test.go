package answersfile

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestLedger_TakeOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		prune bool
	}{
		{name: "should hand over the order it built without copying it", prune: true},
		{name: "should hand over the order with the unasked answers after it", prune: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			book := &Ledger{answered: map[ledgerKey]answeredLine{}}
			book.note("9", Span{Start: 0, End: 10}, Stored{})
			book.note("1", Span{Start: 10, End: 20}, Stored{})

			for _, id := range []string{"1", "2"} {
				book.Admit(id)
			}

			first := &book.lines[0]

			got := book.TakeOrder(tc.prune)

			want := []Span{{Start: 10, End: 20}, {}}
			if !tc.prune {
				want = append(want, Span{Start: 0, End: 10})
			}

			if !slices.Equal(got, want) {
				t.Errorf("order = %v, want %v", got, want)
			}

			if tc.prune && &got[0] != first {
				t.Errorf("order was copied, want the ledger's own slice handed over")
			}

			if book.lines != nil {
				t.Errorf("ledger still holds %v, want it handed over", book.lines)
			}
		})
	}
}

func TestLedger_Unclaimed(t *testing.T) {
	t.Parallel()

	t.Run("should count the lines no record admitted, kept or forgot", func(t *testing.T) {
		t.Parallel()

		book := &Ledger{answered: map[ledgerKey]answeredLine{}}
		for i, id := range []string{"1", "2", "3", "4"} {
			book.note(id, Span{Start: int64(i), End: int64(i + 1)}, Stored{})
		}

		book.Admit("1")
		book.Keep("2")
		book.Forget("3")
		book.Admit("5")

		if got := book.Unclaimed(); got != 1 {
			t.Errorf("Unclaimed = %d, want 1", got)
		}
	})
}

func TestCopyLines(t *testing.T) {
	t.Parallel()

	var file strings.Builder

	var forward []Span

	for i := range 2000 {
		start := int64(file.Len())
		fmt.Fprintf(&file, "{\"id\":%d,\"answer\":0.5}\n", i)
		forward = append(forward, Span{Start: start, End: int64(file.Len())})
	}

	backward := slices.Clone(forward)
	slices.Reverse(backward)

	source := file.String()

	tests := []struct {
		name      string
		header    int64
		lines     []Span
		want      string
		wantReads int
		wantErr   bool
	}{
		{
			name:      "should read spans that run forward in a few large reads",
			lines:     forward,
			want:      source,
			wantReads: 10,
		},
		{
			name:      "should copy spans that run backward",
			lines:     backward,
			want:      reversedLines(source),
			wantReads: 2 * len(backward),
		},
		{
			name:      "should write the header once and skip it inside a span",
			header:    forward[0].End,
			lines:     []Span{{Start: 0, End: forward[1].End}, forward[2]},
			want:      source[:forward[2].End],
			wantReads: 10,
		},
		{
			name:      "should fail when a span read on its own runs past a short source",
			lines:     []Span{backward[0], {Start: backward[1].Start, End: backward[0].End + 1}},
			wantReads: 4,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := &countingReaderAt{source: strings.NewReader(source)}

			var out bytes.Buffer

			err := copyLines(&out, reader, int64(len(source)), tc.header, tc.lines)
			if (err != nil) != tc.wantErr {
				t.Fatalf("copyLines error = %v, want an error %v", err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			if out.String() != tc.want {
				t.Errorf("copied %d bytes, want %d matching the source", out.Len(), len(tc.want))
			}

			if got := int(reader.reads.Load()); got > tc.wantReads {
				t.Errorf("reads = %d, want at most %d", got, tc.wantReads)
			}
		})
	}
}

type countingReaderAt struct {
	source io.ReaderAt
	reads  atomic.Int32
}

func (r *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	r.reads.Add(1)

	return r.source.ReadAt(p, off)
}

func reversedLines(text string) string {
	lines := strings.SplitAfter(text, "\n")
	slices.Reverse(lines)

	return strings.Join(lines, "")
}

func TestResumedVerdicts(t *testing.T) {
	t.Parallel()

	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("should preserve TSV verdicts with terminator %x", ending), func(t *testing.T) {
			content := "answer\terror" + ending + "yes\t" + ending + "\tboom" + ending + "partial\tignored"
			out := openTSV(t, content)
			resume, err := ResumedVerdicts(t.Context(), out, Format{Mode: output.TSV}, false, Resume{})
			if err != nil {
				t.Fatal(err)
			}
			if len(resume.Stored) != 2 {
				t.Fatalf("stored count = %d, want 2", len(resume.Stored))
			}
			if resume.Stored[0].Failure != nil {
				t.Errorf("successful row = %+v", resume.Stored[0])
			}
			if failure := resume.Stored[1].Failure; failure == nil || failure.Message != "boom" {
				t.Errorf("failure = %+v, want boom", failure)
			}
		})
	}

	tests := []struct {
		name      string
		delimiter input.Delimiter
	}{
		{name: "should read a semicolon file back with its comma", delimiter: input.Delimiter{Comma: ';'}},
		{name: "should detect the comma from the file's own header", delimiter: input.Delimiter{Detect: true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := openSemicolons(t, "u;error\n0.9;\n;onesie: boom, twice\n", tc.delimiter)

			resume, err := ResumedVerdicts(t.Context(), out, Format{Mode: output.CSV, Delimiter: tc.delimiter}, false, Resume{})
			if err != nil {
				t.Fatal(err)
			}

			if len(resume.Stored) != 2 || resume.Stored[0].Failure != nil || resume.Stored[1].Failure == nil ||
				resume.Stored[1].Failure.Message != "onesie: boom, twice" {
				t.Errorf("stored = %+v, want an answered row and the failure the second carries", resume.Stored)
			}
		})
	}
}

func TestResumeLedger(t *testing.T) {
	t.Parallel()

	namer, err := jq.Compile(".id")
	if err != nil {
		t.Fatal(err)
	}

	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("should retain TSV spans and retry failed IDs with terminator %x", ending), func(t *testing.T) {
			row := "A\tyes\t" + ending
			content := "id\tanswer\terror" + ending + row + "B\t\tboom" + ending + "C\tyes\t"
			out := openTSV(t, content)
			book, err := ResumeLedger(t.Context(), out, Format{Mode: output.TSV, Namer: namer})
			if err != nil {
				t.Fatal(err)
			}
			at, found := book.StoredAt("A")
			if !found {
				t.Fatal("successful ID has no stored span")
			}
			if got := content[at.Start:at.End]; got != row {
				t.Errorf("stored bytes = %q, want %q", got, row)
			}
			if _, _, answered := book.Admit("B"); answered {
				t.Error("failed ID should be retried")
			}
			if _, found := book.StoredAt("C"); found {
				t.Error("incomplete row should not be counted")
			}
		})
	}

	tests := []struct {
		name      string
		delimiter input.Delimiter
	}{
		{name: "should read a semicolon file back with its comma", delimiter: input.Delimiter{Comma: ';'}},
		{name: "should detect the comma from the file's own header", delimiter: input.Delimiter{Detect: true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := openSemicolons(t, "id;u\nA,1;0.9\n", tc.delimiter)

			book, err := ResumeLedger(t.Context(), out, Format{Mode: output.CSV, Namer: namer, Delimiter: tc.delimiter})
			if err != nil {
				t.Fatal(err)
			}

			if _, found := book.StoredAt("A,1"); !found {
				t.Errorf("the ledger holds no answer for A,1, want the row the file stores")
			}
		})
	}
}

func openSemicolons(t *testing.T, content string, delimiter input.Delimiter) *File {
	t.Helper()

	path := filepath.Join(t.TempDir(), "answers.csv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := Open(Options{}, Config{Path: path, Resume: true, Output: "csv", Delimiter: delimiter})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if releaseErr := out.Release(); releaseErr != nil {
			t.Errorf("Release = %v", releaseErr)
		}
	})

	return out
}

func TestHeaderLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		delimiter input.Delimiter
		want      int64
	}{
		{
			name: "should split the header by a known comma that detection would miss", content: "a,b;\"c\"\n1;2\n",
			delimiter: input.Delimiter{Comma: ';'}, want: 8,
		},
		{
			name: "should detect the comma under auto", content: "a;\"b,\nc\"\n1;2\n",
			delimiter: input.Delimiter{Detect: true}, want: 9,
		},
		{name: "should split by commas under the zero delimiter", content: "a,b\n1,2\n", want: 4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := headerLength(strings.NewReader(tc.content), true, tc.delimiter)
			if err != nil || got != tc.want {
				t.Errorf("headerLength = %d, %v, want %d", got, err, tc.want)
			}
		})
	}
}

func openTSV(t *testing.T, content string) *File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "answers.tsv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := Open(Options{}, Config{Path: path, Resume: true, Output: "tsv"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := out.Release(); err != nil {
			t.Error(err)
		}
	})
	return out
}

func TestFormat_eachVerdict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		format  Format
		written string
		want    []output.Verdict
	}{
		{
			name:   "should read one verdict per json line, the error key before the verdict",
			format: Format{Mode: output.JSON, Gated: true},
			written: `{"error":{"kind":"http","status":503,"message":"x"},"verdict":"fail"}` + "\n" +
				`{"verdict":"fail","u":0.1}` + "\n" +
				`{"verdict":"unsure","u":0.4}` + "\n" +
				`{"verdict":"pass","u":0.9}` + "\n" +
				`{"u":0.9}` + "\n",
			want: []output.Verdict{output.Error, output.Fail, output.Unsure, output.Pass, output.Pass},
		},
		{
			name:    "should read every line but an error as answered when the run has no gate",
			format:  Format{Mode: output.JSON},
			written: `{"verdict":"fail","u":0.1}` + "\n" + `{"error":{"kind":"input","status":null,"message":"x"}}` + "\n",
			want:    []output.Verdict{output.Answered, output.Error},
		},
		{
			name:    "should read an error row before its verdict cell",
			format:  Format{Mode: output.CSV, Gated: true},
			written: "u,verdict,error\n0.1,fail,boom\n0.4,unsure,\n0.9,pass,\n0.2,fail,\n",
			want:    []output.Verdict{output.Error, output.Unsure, output.Pass, output.Fail},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got []output.Verdict

			err := tc.format.eachVerdict(strings.NewReader(tc.written), func(_ Span, stored Stored) {
				got = append(got, stored.Verdict)
			})
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("verdicts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTally_Count(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stored []Stored
		want   Tally
	}{
		{
			name: "should count each record under its one verdict",
			stored: []Stored{
				{Verdict: output.Error, Failure: &output.Failure{Message: "x"}},
				{Verdict: output.Fail},
				{Verdict: output.Unsure},
				{Verdict: output.Pass},
			},
			want: Tally{Records: 4, Verdicts: output.Tally{output.Pass: 1, output.Fail: 1, output.Unsure: 1, output.Error: 1}},
		},
		{name: "should count nothing for no records"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got Tally
			for _, stored := range tc.stored {
				got.Count(stored)
			}

			if got != tc.want {
				t.Errorf("tally = %+v, want %+v", got, tc.want)
			}
		})
	}
}
