package output

import (
	"encoding/json"
	"fmt"
	"io"
)

func writeRaw(w io.Writer, rec Record) error {
	// A failed record prints the fallback word if the question has one and an empty line
	// otherwise, so one input line still produces one output line.
	if len(rec.Answers) == 0 || rec.Answers[0].Answer == nil {
		_, err := fmt.Fprintln(w)

		return err
	}

	switch typed := scalar(rec.Answers[0].Answer).(type) {
	case string:
		_, err := fmt.Fprintln(w, typed)

		return err
	case bool:
		_, err := fmt.Fprintln(w, typed)

		return err
	case float64:
		_, err := fmt.Fprintf(w, "%g\n", typed)

		return err
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(w, string(encoded))

		return err
	}
}
