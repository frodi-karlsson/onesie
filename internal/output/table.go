package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// fallbackWidth is the conventional terminal width, used when nothing better is known.
	fallbackWidth = 80
	labelWidth    = 16
	// numberWidth covers the two leading spaces, the space before the number, and a value such as
	// 0.1234.
	numberWidth = 9
	// minBarWidth keeps a bar readable on a narrow terminal.
	minBarWidth = 8
)

// Width resolves the width for the probability bars. Both lookups are injected, so this package
// reads no environment and opens no terminal of its own.
func Width(lookupEnv func(string) (string, bool), terminalWidth func() (int, bool)) int {
	if raw, ok := lookupEnv("COLUMNS"); ok {
		if columns, err := strconv.Atoi(raw); err == nil && columns > 0 {
			return columns
		}
	}

	if columns, ok := terminalWidth(); ok && columns > 0 {
		return columns
	}

	return fallbackWidth
}

// WriteTable renders the human readable form at an explicit width, so the caller owns any
// environment lookup.
func WriteTable(w io.Writer, rec Record, columns int) error {
	bars := columns - labelWidth - numberWidth
	if bars < minBarWidth {
		bars = minBarWidth
	}

	if rec.Failure != nil {
		status := "none"
		if rec.Failure.Status != nil {
			status = strconv.Itoa(*rec.Failure.Status)
		}

		if _, err := fmt.Fprintf(w, "error  %s  status %s  %s\n",
			rec.Failure.Kind, status, rec.Failure.Message); err != nil {
			return err
		}
	}

	if err := writeHeader(w, rec); err != nil {
		return err
	}

	for _, named := range rec.Answers {
		if named.Answer == nil {
			continue
		}

		if err := writeBlock(w, named, bars); err != nil {
			return err
		}
	}

	return nil
}

func writeHeader(w io.Writer, rec Record) error {
	if rec.Model == "" {
		return nil
	}

	line := "model " + rec.Model
	if rec.Usage != nil {
		line += fmt.Sprintf("  %d in / %d out", rec.Usage.InputTokens, rec.Usage.OutputTokens)
	}

	_, err := fmt.Fprintln(w, line)

	return err
}

func writeBlock(w io.Writer, named Named, bars int) error {
	a := named.Answer

	headline := fmt.Sprintf("%v", scalar(a))

	// A bare probability reads better at a fixed four places than in Go's shortest form, and a
	// question with a distribution puts its numbers in the rows below instead.
	if probability, ok := a.Value.(float64); ok && a.P == nil {
		headline = strconv.FormatFloat(probability, 'f', 4, 64)
	}

	if _, err := fmt.Fprintf(w, "\n%s  %s\n", named.ID, headline); err != nil {
		return err
	}

	if a.Confidence != nil {
		if _, err := fmt.Fprintf(w, "  confidence %.2f\n", *a.Confidence); err != nil {
			return err
		}
	}

	if a.Score != nil && a.Norm != nil {
		if _, err := fmt.Fprintf(w, "  score %.4f  norm %.4f\n", *a.Score, *a.Norm); err != nil {
			return err
		}
	}

	if a.P == nil {
		return nil
	}

	for _, key := range a.P.Keys {
		// The index stays visible beside the legend text, since value reports the index and a
		// reader needs to match the two.
		label := key
		if text, ok := a.Legend[key]; ok {
			label = key + " " + text
		}

		value := a.P.Values[key]

		if _, err := fmt.Fprintf(w, "  %-*s %s %.4f\n",
			labelWidth, truncate(label), bar(value, bars), value); err != nil {
			return err
		}
	}

	return nil
}

func bar(probability float64, width int) string {
	// strings.Repeat panics on a negative count, and the width arrives from a caller that may have
	// read it out of the environment.
	if width < 0 {
		width = 0
	}

	filled := int(probability*float64(width) + 0.5)
	if filled < 0 {
		filled = 0
	}

	if filled > width {
		filled = width
	}

	return strings.Repeat("█", filled) + strings.Repeat(" ", width-filled)
}

func truncate(label string) string {
	runes := []rune(label)
	if len(runes) <= labelWidth {
		return label
	}

	return string(runes[:labelWidth-1]) + "…"
}
