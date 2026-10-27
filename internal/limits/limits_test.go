package limits_test

import (
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
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
		{name: "should report the longest an --id may write out to", want: "max-id-bytes 1024"},
		{name: "should report the most records calibrate reads", want: "max-calibrate-records 100000"},
		{name: "should report the largest question file -f reads", want: "max-question-file-bytes 33554432"},
		{name: "should report the longest -i request line", want: "max-request-line-bytes 33554432"},
		{name: "should report the largest image file", want: "max-image-bytes 16777216"},
		{name: "should report the image side range", want: "image-side 280 to 1568"},
		{name: "should report the image pixel range", want: "image-pixels 78400 to 2458624"},
		{name: "should report the largest request body", want: "max-body-bytes 33554432"},
		{name: "should report the most distinct requests a stream deduplicates", want: "max-dedup-requests 100000"},
		{name: "should report the size the response cache is held to", want: "max-cache-bytes 104857600"},
		{name: "should report how long an alias entry lives in the cache", want: "cache-ttl 24h0m0s"},
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

func TestMaxChoiceOptions(t *testing.T) {
	t.Parallel()

	t.Run("should be the ceiling onesie-go enforces", func(t *testing.T) {
		t.Parallel()

		if limits.MaxChoiceOptions != onesie.MaxChoiceOptions {
			t.Errorf("MaxChoiceOptions = %d, want onesie-go's %d", limits.MaxChoiceOptions, onesie.MaxChoiceOptions)
		}
	})
}

func TestMaxScoreLevels(t *testing.T) {
	t.Parallel()

	t.Run("should be the ceiling onesie-go enforces", func(t *testing.T) {
		t.Parallel()

		if limits.MaxScoreLevels != onesie.MaxScoreLevels {
			t.Errorf("MaxScoreLevels = %d, want onesie-go's %d", limits.MaxScoreLevels, onesie.MaxScoreLevels)
		}
	})
}
