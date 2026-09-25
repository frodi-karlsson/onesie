package cache

import "regexp"

var versioned = regexp.MustCompile(`^.+-[0-9]+\.[0-9]+(\.[0-9]+)?$`)

// Pinned reports whether a model name names one fixed model rather than an alias that can move. Only
// a typesafe name ending in a two or three part version is pinned.
func Pinned(provider, model string) bool {
	return provider == "typesafe" && versioned.MatchString(model)
}
