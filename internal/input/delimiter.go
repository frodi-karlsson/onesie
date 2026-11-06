package input

import "github.com/frodi-karlsson/onesie/internal/delimiter"

// ParseDelimiter reads auto or one delimiter character.
func ParseDelimiter(value string) (Delimiter, error) { return delimiter.Parse(value) }

// Delimiter is a csv input field separator.
type Delimiter = delimiter.Delimiter

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
