package output

import (
	"fmt"
	"io"
)

// fallbackWidth is the conventional terminal width, used when nothing better is known.
const fallbackWidth = 80

// WriteTable renders the human readable form at an explicit width, so the caller owns any
// environment lookup.
func WriteTable(w io.Writer, rec Record, columns int) error {
	for _, named := range rec.Answers {
		if named.Answer == nil {
			continue
		}

		if _, err := fmt.Fprintf(w, "%-*s %v\n",
			columns/4, named.ID, scalar(named.Answer)); err != nil {
			return err
		}
	}

	return nil
}
