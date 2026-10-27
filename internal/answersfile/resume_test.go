package answersfile

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
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
			book.note("9", Span{Start: 0, End: 10}, Verdict{})
			book.note("1", Span{Start: 10, End: 20}, Verdict{})

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
