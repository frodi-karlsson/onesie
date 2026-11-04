package input

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ParseDelimiter reads a delimiter setting, auto to detect it from the header row or one character
// to split on.
func ParseDelimiter(value string) (Delimiter, error) {
	if value == "auto" {
		return Delimiter{Detect: true}, nil
	}

	comma, size := utf8.DecodeRuneInString(value)
	if size == 0 || size != len(value) || comma == utf8.RuneError {
		return Delimiter{}, fmt.Errorf("takes auto or one character, got '%s'", escaped(value))
	}

	if comma == '"' || comma == '\n' || comma == '\r' {
		return Delimiter{}, fmt.Errorf("cannot be a quote or a line break, got '%s'", escaped(value))
	}

	return Delimiter{Comma: comma}, nil
}

func escaped(value string) string {
	// The escapes show a line break inside the single quotes every other message uses. Go quotes with
	// double quotes, so its escaped double quote is swapped for an escaped single quote.
	quoted := strconv.Quote(value)

	return strings.NewReplacer(`\"`, `"`, `'`, `\'`).Replace(quoted[1 : len(quoted)-1])
}

// Delimiter is a csv input's field separator. The zero value is a comma, and Detect picks one from
// the header row.
type Delimiter struct {
	// Comma is the separator, zero meaning a comma.
	Comma  rune
	Detect bool
}

// DetectDelimiter picks the most frequent of a comma, a semicolon and a tab outside quotes in a
// header row, and a comma on a tie or when the row holds none.
func DetectDelimiter(header string) rune {
	counts := map[rune]int{}
	quoted := false

	for _, char := range header {
		switch {
		case char == '"':
			quoted = !quoted
		case !quoted && (char == ',' || char == ';' || char == '\t'):
			counts[char]++
		}
	}

	best := ','

	for _, candidate := range []rune{';', '\t'} {
		if counts[candidate] > counts[best] {
			best = candidate
		}
	}

	if best != ',' && counts[';'] == counts['\t'] {
		return ','
	}

	return best
}
