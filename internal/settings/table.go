package settings

import "github.com/frodi-karlsson/onesie/internal/cache"

// NoAliasTTL disables reading and storing alias cache entries.
const NoAliasTTL = cache.NoAliasTTL

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

func keptBlank(v View) (any, bool) {
	// A blank line is not a record --keep could print, so it is dropped unless --skip-blank is given.
	if v.Merged.Source(SkipBlank.Name()) != SourceDefault || Get(v.Merged, Keep) == "" {
		return nil, false
	}
	return true, true
}
