package cli

import (
	setting "github.com/frodi-karlsson/onesie/internal/settings"

	"github.com/frodi-karlsson/onesie/internal/output"
)

func stopsOf(flags runConfig) stopSet {
	var stops stopSet

	for _, verdict := range []output.Verdict{output.Fail, output.Unsure, output.Error} {
		if setting.StopsOn(flags.merged, verdict.String()) {
			stops = stops.with(verdict)
		}
	}

	return stops
}

type stopSet uint8

func (s stopSet) with(verdict output.Verdict) stopSet {
	return s | 1<<verdict
}

func (s stopSet) has(verdict output.Verdict) bool {
	return s&(1<<verdict) != 0
}

func (s stopSet) judges() bool {
	return s.has(output.Fail) || s.has(output.Unsure)
}
