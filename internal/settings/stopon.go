package settings

import (
	"slices"
	"strings"
)

// StopsOn reports whether --stop-on names verdict, so the run ends at the first record with it.
func StopsOn(m *Merged, verdict string) bool {
	return slices.Contains(strings.Split(Get(m, StopOn), ","), verdict)
}
