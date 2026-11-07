package output

import (
	"encoding/csv"
	"errors"
	"io"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/delimiter"
)

func newFieldWriter(w io.Writer, mode Mode, comma rune) *fieldWriter {
	destination := checkedFieldSink{writer: w}
	rows := csv.NewWriter(destination)
	if comma != 0 {
		rows.Comma = comma
	}
	return &fieldWriter{destination: destination, rows: rows, mode: mode}
}

type fieldWriter struct {
	destination checkedFieldSink
	rows        *csv.Writer
	mode        Mode
}

func (w *fieldWriter) WriteRow(fields []string) error {
	if len(fields) == 0 {
		return errors.New("a delimited row requires at least one field")
	}
	switch w.mode {
	case CSV:
		if len(fields) == 1 && fields[0] == "" {
			if _, err := delimiter.Parse(string(w.rows.Comma)); err != nil {
				return err
			}
			if _, err := w.destination.Write([]byte("\"\"\n")); err != nil {
				return err
			}
		} else {
			if err := w.rows.Write(fields); err != nil {
				return err
			}
			w.rows.Flush()
			if err := w.rows.Error(); err != nil {
				return err
			}
		}
	case TSV:
		if len(fields) == 1 && fields[0] == "" {
			return errors.New("a tsv row cannot contain only an empty field")
		}
		for _, field := range fields {
			if strings.ContainsAny(field, "\t\n") {
				return errors.New("a tsv field cannot contain tabs or line feeds")
			}
		}
		terminator := "\n"
		if strings.HasSuffix(fields[len(fields)-1], "\r") {
			terminator = "\r\n"
		}
		if _, err := w.destination.Write([]byte(strings.Join(fields, "\t") + terminator)); err != nil {
			return err
		}
	default:
		return errors.New("field encoding requires csv or tsv mode")
	}
	return nil
}

type checkedFieldSink struct {
	writer io.Writer
}

func (s checkedFieldSink) Write(p []byte) (int, error) {
	n, err := s.writer.Write(p)
	if n < len(p) && err == nil {
		err = io.ErrShortWrite
	}
	return n, err
}
