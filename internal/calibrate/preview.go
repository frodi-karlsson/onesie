package calibrate

import (
	"strings"
	"unicode"
)

const previewRunes = 40

// Preview shortens a record's text to one line of about 40 characters for a worst miss, with its
// line breaks folded into spaces and its control characters dropped.
func Preview(text string) string {
	var line strings.Builder

	space := false

	for _, r := range text {
		switch {
		case unicode.IsSpace(r):
			space = line.Len() > 0
		case unicode.IsControl(r) || !unicode.IsPrint(r):
		default:
			if space {
				line.WriteByte(' ')
				space = false
			}

			line.WriteRune(r)
		}
	}

	folded := []rune(line.String())
	if len(folded) <= previewRunes {
		return string(folded)
	}

	return strings.TrimRight(string(folded[:previewRunes]), " ") + "..."
}
