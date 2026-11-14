package settings

import (
	"slices"
	"strings"
)

// StopsOn reports whether --stop-on names verdict, so the run ends at the first record with it.
func StopsOn(m *Merged, verdict string) bool {
	return names(m, StopOn, verdict)
}

// Keeps reports whether --keep names verdict, so a record with it prints as read.
func Keeps(m *Merged, verdict string) bool {
	return names(m, Keep, verdict)
}

func names(m *Merged, list Key[string], verdict string) bool {
	return slices.Contains(strings.Split(Get(m, list), ","), verdict)
}
