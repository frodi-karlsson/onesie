package answersfile

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func FuzzLineVerdict(f *testing.F) {
	f.Add("http", "status 500", 500, true, uint8(1), "T-1", []byte(`{"error":{"kind":"http","status":500,"message":"x"}}`+"\n"))
	f.Add("input", "line 2: not json", 0, false, uint8(1), "", []byte(`{"id":"a","assert":false,"urgent":{"value":0.9}}`+"\n"))
	f.Add("", "", 0, false, uint8(0), "a,b", []byte(`{"error":{"kind":"input","status":null,"message":"x"},"extra":1}`+"\n"))
	f.Add("transport", "a\r\nb\t\"c\"", 0, false, uint8(2), "\n", []byte(`{"error":{"kind":"x","status":null,"message":"<&>"}}`))
	f.Add("response", "\xff", 200, true, uint8(3), "\"", []byte(`{"error":{"kind":"x","status":1e0,"message":"m"}}`+"\n"))
	f.Add("http", "x", -1, true, uint8(0), "id", []byte(`{"error":null}`+"\n"))

	f.Fuzz(func(t *testing.T, kind, message string, status int, hasStatus bool, outcome uint8, id string, line []byte) {
		rec := writtenRecord(kind, message, status, hasStatus, outcome, id)
		want := Verdict{Rejected: rec.AssertFailed, Abstained: rec.Abstained, Failure: rec.Failure}

		for _, mode := range []output.Mode{output.JSON, output.Values} {
			var written bytes.Buffer
			if err := output.Write(&written, mode, rec); err != nil {
				t.Fatalf("writing %+v: %v", rec, err)
			}

			got := onlyVerdict(t, Format{Mode: output.JSON, Gated: true}, written.Bytes())
			if !sameVerdict(got, want, true) {
				t.Fatalf("-o %v line %q reads back as %+v, want %+v", mode, written.Bytes(), got, want)
			}
		}

		for _, mode := range []output.Mode{output.CSV, output.TSV} {
			var written bytes.Buffer

			rows := output.NewDelimited(&written, mode, output.DelimitedOptions{
				IDs: []string{"urgent"}, ID: rec.ID != nil, Assert: true, Header: true,
			})
			if err := rows.Write(rec, nil, nil); err != nil {
				t.Fatalf("writing %+v: %v", rec, err)
			}

			got := onlyVerdict(t, Format{Mode: mode, Gated: true}, written.Bytes())
			if !sameVerdict(got, want, false) {
				t.Fatalf("-o %v rows %q read back as %+v, want %+v", mode, written.Bytes(), got, want)
			}
		}

		if rec.Failure != nil {
			encoded := append(output.EncodeFailure(rec.Failure), '\n')
			if got := ownFailure(encoded); got == nil || !sameFailure(got, rec.Failure) {
				t.Fatalf("forwarded error line %q reads back as %+v, want %+v", encoded, got, rec.Failure)
			}
		}

		if got := ownFailure(line); got != nil && !bytes.Equal(output.EncodeFailure(got), bytes.TrimSuffix(line, []byte("\n"))) {
			t.Fatalf("forwarded line %q was taken as onesie's error %+v, which onesie writes as %q",
				line, got, output.EncodeFailure(got))
		}

		Format{Mode: output.JSON, Gated: true}.lineVerdict(line)
		Format{Mode: output.JSON, Gated: true, Merge: true, MergeKey: "answers"}.lineVerdict(line)
	})
}

func writtenRecord(kind, message string, status int, hasStatus bool, outcome uint8, id string) output.Record {
	rec := output.Record{
		Answers: []output.Named{{ID: "urgent", Answer: &answer.Answer{Value: 0.25}}},
	}

	if id != "" {
		rec.ID = id
	}

	switch outcome % 4 {
	case 1:
		// Every message onesie writes is an error's text, which is never empty.
		rec.Failure = &output.Failure{Kind: output.Kind(kind), Message: "onesie: " + message}
		if hasStatus {
			rec.Failure.Status = &status
		}
	case 2:
		rec.AssertFailed = true
	case 3:
		rec.Abstained = true
	}

	return rec
}

func onlyVerdict(t *testing.T, format Format, written []byte) Verdict {
	t.Helper()

	var judged []Verdict

	if err := format.eachVerdict(bytes.NewReader(written), func(_ Span, stored Verdict) {
		judged = append(judged, stored)
	}); err != nil {
		t.Fatalf("reading %q back: %v", written, err)
	}

	if len(judged) != 1 {
		t.Fatalf("reading %q back found %d records, want 1", written, len(judged))
	}

	return judged[0]
}

func sameVerdict(got, want Verdict, whole bool) bool {
	if got.Rejected != want.Rejected || got.Abstained != want.Abstained || (got.Failure == nil) != (want.Failure == nil) {
		return false
	}

	// A row keeps only the message of a failure, so only a line is held to all of it.
	return !whole || got.Failure == nil || sameFailure(got.Failure, want.Failure)
}

func sameFailure(got, want *output.Failure) bool {
	// Compared as written, since encoding a message replaces the bytes that are not UTF-8.
	gotJSON, gotErr := json.Marshal(got)
	wantJSON, wantErr := json.Marshal(want)

	return gotErr == nil && wantErr == nil && bytes.Equal(gotJSON, wantJSON)
}
