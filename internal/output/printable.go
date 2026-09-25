package output

import (
	"strings"
	"unicode"
)

// Printable drops every C0 and C1 control but newline and tab, so text from a server or an input
// cannot drive the terminal it is printed to. Invalid UTF-8 becomes U+FFFD first.
func Printable(text string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}

		return r
	}, strings.ToValidUTF8(text, "�"))
}
