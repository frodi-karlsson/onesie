package cli_test

import (
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/cli"
)

func TestStatsRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stats cli.Stats
		want  string
	}{
		{
			name: "should render a single request",
			stats: cli.Stats{
				Requests: 1, Questions: 3, InputTokens: 2841, OutputTokens: 71,
				Models: []string{"onesie-1.13.0"}, Attempts: 3,
				Retries: map[int]int{429: 2}, AttemptTimeout: 10 * time.Second,
				Elapsed: 11400 * time.Millisecond,
			},
			want: "1 request, 3 questions, 2841 in / 71 out, model onesie-1.13.0, " +
				"3 attempts (2 retries: 429×2), 10s/attempt, 11.4s",
		},
		{
			name: "should render a stream with failures and two models",
			stats: cli.Stats{
				Requests: 40, Failed: 2, Questions: 80,
				InputTokens: 10000, OutputTokens: 400,
				Models: []string{"onesie-1.13.0", "onesie-1.14.0"}, Attempts: 41,
				Retries: map[int]int{429: 1}, AttemptTimeout: 10 * time.Second,
				Elapsed: 30 * time.Second,
			},
			want: "40 requests, 2 failed, 80 questions, 10000 in / 400 out, " +
				"models onesie-1.13.0, onesie-1.14.0, 41 attempts (1 retry: 429×1), " +
				"10s/attempt, 30s",
		},
		{
			name: "should name a statusless retry transport",
			stats: cli.Stats{
				Requests: 1, Records: 1, Questions: 1,
				Models: []string{"onesie-1.13.0"}, Attempts: 3,
				Retries: map[int]int{0: 1, 429: 1}, AttemptTimeout: 10 * time.Second,
				Elapsed: time.Second,
			},
			want: "1 request, 1 question, 0 in / 0 out, model onesie-1.13.0, " +
				"3 attempts (2 retries: transport×1, 429×1), 10s/attempt, 1s",
		},
		{
			name: "should count a record that never reached the client",
			stats: cli.Stats{
				Requests: 2, Records: 3, Failed: 1, Questions: 2,
				Models: []string{"onesie-1.13.0"}, Attempts: 3,
				Retries: map[int]int{429: 1}, AttemptTimeout: 10 * time.Second,
				Elapsed: time.Second,
			},
			want: "3 records, 1 failed, 2 requests, 2 questions, 0 in / 0 out, " +
				"model onesie-1.13.0, 3 attempts (1 retry: 429×1), 10s/attempt, 1s",
		},
		{
			name: "should round a sub second elapsed to milliseconds",
			stats: cli.Stats{
				Requests: 1, Questions: 1, Attempts: 1,
				AttemptTimeout: 10 * time.Second, Elapsed: 3040959 * time.Nanosecond,
			},
			want: "1 request, 1 question, 0 in / 0 out, 1 attempt, 10s/attempt, 3ms",
		},
		{
			name: "should round elapsed to a tenth of a second above one second",
			stats: cli.Stats{
				Requests: 1, Questions: 1, Attempts: 1,
				AttemptTimeout: 10 * time.Second, Elapsed: 11432123456 * time.Nanosecond,
			},
			want: "1 request, 1 question, 0 in / 0 out, 1 attempt, 10s/attempt, 11.4s",
		},
		{
			name: "should omit the retry clause when nothing was retried",
			stats: cli.Stats{
				Requests: 1, Questions: 1, Models: []string{"onesie-1.13.0"},
				Attempts: 1, AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "1 request, 1 question, 0 in / 0 out, model onesie-1.13.0, " +
				"1 attempt, 10s/attempt, 1s",
		},
		{
			name: "should render a false assertion count beside the failed count",
			stats: cli.Stats{
				Requests: 40, Failed: 2, FalseAsserts: 1, Questions: 80,
				InputTokens: 10000, OutputTokens: 400,
				Models: []string{"onesie-1.13.0"}, Attempts: 40,
				AttemptTimeout: 10 * time.Second, Elapsed: 30 * time.Second,
			},
			want: "40 requests, 2 failed, 1 false assertion, 80 questions, " +
				"10000 in / 400 out, model onesie-1.13.0, 40 attempts, 10s/attempt, 30s",
		},
		{
			name: "should omit the false assertion clause when every assertion held",
			stats: cli.Stats{
				Requests: 40, Questions: 80, InputTokens: 10000, OutputTokens: 400,
				Models: []string{"onesie-1.13.0"}, Attempts: 40,
				AttemptTimeout: 10 * time.Second, Elapsed: 30 * time.Second,
			},
			want: "40 requests, 80 questions, 10000 in / 400 out, model onesie-1.13.0, " +
				"40 attempts, 10s/attempt, 30s",
		},
		{
			name: "should omit the model clause when no model was seen",
			stats: cli.Stats{
				Requests: 1, Failed: 1, Questions: 1, Attempts: 1,
				AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "1 request, 1 failed, 1 question, 0 in / 0 out, " +
				"1 attempt, 10s/attempt, 1s",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.stats.String(); got != tc.want {
				t.Errorf("String() =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
