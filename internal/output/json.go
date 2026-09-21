package output

import (
	"encoding/json"
	"io"

	"github.com/frodi-karlsson/jev-cli/internal/answer"
)

// Answer is an alias so the encoders read cleanly without qualifying every use.
type Answer = answer.Answer

func writeJSON(w io.Writer, rec Record) error {
	var buf []byte

	buf = append(buf, '{')
	buf = appendFailure(buf, rec)

	if rec.Model != "" {
		buf = appendKey(buf, "model")

		model, err := json.Marshal(rec.Model)
		if err != nil {
			return err
		}

		buf = append(buf, model...)
	}

	if rec.Usage != nil {
		buf = appendKey(buf, "usage")

		usage, err := json.Marshal(rec.Usage)
		if err != nil {
			return err
		}

		buf = append(buf, usage...)
	}

	for _, named := range rec.Answers {
		if named.Answer == nil {
			continue
		}

		buf = appendKey(buf, named.ID)

		encoded, err := json.Marshal(named.Answer)
		if err != nil {
			return err
		}

		buf = append(buf, encoded...)
	}

	buf = append(buf, '}', '\n')

	_, err := w.Write(buf)

	return err
}

func writeValues(w io.Writer, rec Record) error {
	var buf []byte

	buf = append(buf, '{')
	buf = appendFailure(buf, rec)

	for _, named := range rec.Answers {
		if named.Answer == nil {
			continue
		}

		buf = appendKey(buf, named.ID)

		encoded, err := json.Marshal(scalar(named.Answer))
		if err != nil {
			return err
		}

		buf = append(buf, encoded...)
	}

	buf = append(buf, '}', '\n')

	_, err := w.Write(buf)

	return err
}

func appendFailure(buf []byte, rec Record) []byte {
	if rec.Failure == nil {
		return buf
	}

	// The reserved error key goes first, so a consumer reading a stream can branch on it before
	// parsing the rest of the line.
	encoded, err := json.Marshal(rec.Failure)
	if err != nil {
		encoded = []byte(`{"kind":"input","status":null,"message":"unencodable failure"}`)
	}

	buf = appendKey(buf, "error")

	return append(buf, encoded...)
}

func appendKey(buf []byte, name string) []byte {
	if len(buf) > 1 {
		buf = append(buf, ',')
	}

	// Through json.Marshal rather than wrapping in quotes. Question ids are not character checked
	// anywhere, so an id containing a quote would otherwise produce a line no consumer can parse.
	encoded, err := json.Marshal(name)
	if err != nil {
		encoded = []byte(`"?"`)
	}

	buf = append(buf, encoded...)

	return append(buf, ':')
}

func scalar(a *Answer) any {
	// The decision when policy set one, the value otherwise. This is what makes --fallback usable
	// in a shell conditional.
	if a.Decided {
		return a.Decision
	}

	return a.Value
}
