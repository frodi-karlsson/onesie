package cli

import (
	setting "github.com/frodi-karlsson/onesie/internal/settings"

	"github.com/frodi-karlsson/onesie/internal/output"
)

func stopsOf(flags runConfig) verdictSet {
	return verdictsOf(flags, setting.StopsOn)
}

func keptOf(flags runConfig) verdictSet {
	return verdictsOf(flags, setting.Keeps)
}

func verdictsOf(flags runConfig, names func(*setting.Merged, string) bool) verdictSet {
	var set verdictSet

	for _, verdict := range []output.Verdict{output.Pass, output.Fail, output.Unsure, output.Error} {
		if names(flags.merged, verdict.String()) {
			set = set.with(verdict)
		}
	}

	return set
}

type verdictSet uint8

func (s verdictSet) with(verdict output.Verdict) verdictSet {
	return s | 1<<verdict
}

func (s verdictSet) has(verdict output.Verdict) bool {
	return s&(1<<verdict) != 0
}

func (s verdictSet) judges() bool {
	return s.has(output.Fail) || s.has(output.Unsure)
}

func (s verdictSet) empty() bool {
	return s == 0
}
