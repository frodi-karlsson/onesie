package limits_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

func TestReport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{name: "should report the choice option ceiling", want: "max-choice-options 255"},
		{name: "should report the choice option floor", want: "min-choice-options 2"},
		{name: "should report the score level ceiling", want: "max-score-levels 10"},
		{name: "should report the score level floor", want: "min-score-levels 2"},
		{name: "should report the attempt timeout", want: "attempt-timeout 10s"},
		{name: "should report the retry count", want: "retries 2"},
		{name: "should report the retry after cap", want: "max-retry-after 1m0s"},
		{name: "should report the seconds ceiling", want: "max-seconds 86400"},
		{name: "should report the longest line read", want: "max-line-bytes 8388608"},
		{name: "should report the deepest a --map result may nest", want: "max-map-depth 9999"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var lines []string
			for _, entry := range limits.Report() {
				lines = append(lines, entry.Name+" "+entry.Value)
			}

			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("report missing %q\ngot:\n%s", tc.want, joined)
			}
		})
	}
}
