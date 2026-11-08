package settings

import "time"

// NoAliasTTL disables reading and storing alias cache entries.
const NoAliasTTL time.Duration = -1

func autoOutput(v View) (any, bool) {
	if name := Get(v.Merged, Output); name != "" && name != "auto" {
		return nil, false
	}
	if v.Facts.StdoutTTY && Get(v.Merged, Out) == "" && !inputModeOf(v).Streaming() && !merging(v) {
		return "table", true
	}
	return "json", true
}

func aliasLifetime(v View) (any, bool) {
	if v.Merged.Source(CacheTTL.Name()) == SourceDefault || Get(v.Merged, CacheTTL) != 0 {
		return nil, false
	}
	return NoAliasTTL, true
}
