package output

import (
	"fmt"
	"io"
)

// Prefix starts every line onesie writes to stderr about itself, so a reader tells it from the
// output of a command it runs beside.
const Prefix = "onesie: "

// Notice writes text to w as one line after Prefix.
func Notice(w io.Writer, text string) error {
	_, err := fmt.Fprintln(w, Prefix+text)

	return err
}

// Warning writes text to w as a Notice that starts with warning, for a problem the run carries on
// past.
func Warning(w io.Writer, text string) error {
	return Notice(w, "warning: "+text)
}
