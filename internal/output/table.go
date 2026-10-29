package output

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/num"
)

const (
	fallbackWidth = 80
	maxWidth      = 1000
	labelWidth    = 16
	rowPadding    = 4 // Two leading spaces, the space after the label and the space before the number.
	minBarWidth   = 8
)

// Width resolves the width for the probability bars. Both lookups are injected, so this package
// reads no environment and opens no terminal of its own.
func Width(lookupEnv func(string) (string, bool), terminalWidth func() (int, bool)) int {
	if raw, ok := lookupEnv("COLUMNS"); ok {
		if columns, err := strconv.Atoi(raw); err == nil && columns > 0 {
			return min(columns, maxWidth)
		}
	}

	if columns, ok := terminalWidth(); ok && columns > 0 {
		return min(columns, maxWidth)
	}

	return fallbackWidth
}

// WriteTable renders the human readable form at an explicit width, so the caller owns any
// environment lookup.
func WriteTable(w io.Writer, rec Record, columns int) error {
	bars := columns - labelWidth - rowPadding - widestProbability(rec)
	if bars < minBarWidth {
		bars = minBarWidth
	}

	if rec.Failure != nil {
		if _, err := fmt.Fprintln(w, "error: "+Printable(failureText(rec.Failure))); err != nil {
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

func failureText(failure *Failure) string {
	if failure.Sentence == "" {
		return failure.Message
	}

	return failure.Sentence
}

func widestProbability(rec Record) int {
	widest := 0

	for _, named := range rec.Answers {
		if named.Answer == nil || named.Answer.P == nil {
			continue
		}

		for _, value := range named.Answer.P.Values {
			widest = max(widest, len(num.Format(value)))
		}
	}

	return widest
}

func writeHeader(w io.Writer, rec Record) error {
	if rec.Model != "" {
		line := "model: " + Printable(rec.Model)
		if rec.Usage != nil {
			line += fmt.Sprintf(", %d in / %d out", rec.Usage.InputTokens, rec.Usage.OutputTokens)

			if rec.Usage.Cost != nil {
				line += ", " + Cost(*rec.Usage.Cost)
			}
		}

		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	switch {
	case rec.AssertFailed:
		_, err := fmt.Fprintln(w, "assert: false")

		return err
	case rec.Abstained:
		_, err := fmt.Fprintln(w, "abstain")

		return err
	default:
		return nil
	}
}

func writeBlock(w io.Writer, named Named, bars int) error {
	a := named.Answer

	if _, err := fmt.Fprintf(w, "\n%s: %s\n", Printable(named.ID), headline(a)); err != nil {
		return err
	}

	if a.Confidence != nil {
		if _, err := fmt.Fprintf(w, "  confidence: %s\n", num.Format(*a.Confidence)); err != nil {
			return err
		}
	}

	if a.Score != nil && a.Norm != nil {
		if _, err := fmt.Fprintf(w, "  score: %s, norm: %s\n", num.Format(*a.Score), num.Format(*a.Norm)); err != nil {
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

		if _, err := fmt.Fprintf(w, "  %-*s %s %s\n",
			labelWidth, truncate(Printable(label)), bar(value, bars), num.Format(value)); err != nil {
			return err
		}
	}

	return nil
}

func headline(a *Answer) string {
	// A question with a distribution puts its numbers in the rows below instead. The decision is
	// what the exit code follows, and the probability after it is how close the call was.
	if probability, ok := a.Value.(float64); ok && a.P == nil {
		if a.Decided {
			return decisionWord(a.Decision) + " (" + num.Format(probability) + ")"
		}

		return num.Format(probability)
	}

	// Without the mark the line reads as the model's answer.
	if a.Fallback != "" {
		return Printable(decisionWord(a.Decision)) + " (fallback)"
	}

	return Printable(fmt.Sprintf("%v", scalar(a)))
}

func decisionWord(decision any) string {
	if yes, isBool := decision.(bool); isBool {
		if yes {
			return "yes"
		}

		return "no"
	}

	return fmt.Sprint(decision)
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
