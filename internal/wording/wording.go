// Package wording holds the small rules onesie follows when it writes English for people.
package wording

import "strconv"

// Plural writes count and the form of the noun that fits it, as in 1 error or 2 errors.
func Plural(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}

	return strconv.Itoa(count) + " " + plural
}
