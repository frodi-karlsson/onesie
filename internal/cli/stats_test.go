package cli_test

import (
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/cli"
)

func TestStatsString(t *testing.T) {
	t.Parallel()

	cost := 0.000012

	tests := []struct {
		name  string
		stats cli.Stats
		want  string
	}{
		{
			name: "should render a single request",
			stats: cli.Stats{
				Records: 1, Requests: 1, Questions: 3, InputTokens: 2841, OutputTokens: 71,
				Models: []string{"onesie-1.13.0"}, Attempts: 3,
				Retries: map[int]int{429: 2}, AttemptTimeout: 10 * time.Second,
				Elapsed: 11400 * time.Millisecond,
			},
			want: "1 request, 3 questions asked, 2841 in / 71 out tokens, model onesie-1.13.0, " +
				"3 attempts (2 retries: 429×2), 10s/attempt, 11.4s",
		},
		{
			name: "should render a stream with failures and two models",
			stats: cli.Stats{
				Records: 40, Requests: 40, Failed: 2, Questions: 80,
				InputTokens: 10000, OutputTokens: 400,
				Models: []string{"onesie-1.13.0", "onesie-1.14.0"}, Attempts: 41,
				Retries: map[int]int{429: 1}, AttemptTimeout: 10 * time.Second,
				Elapsed: 30 * time.Second,
			},
			want: "40 records, 2 failed, 40 requests, 80 questions asked, 10000 in / 400 out tokens, " +
				"models onesie-1.13.0, onesie-1.14.0, 41 attempts (1 retry: 429×1), " +
				"10s/attempt, 30s",
		},
		{
			name: "should lead a stream with its records when each became a request",
			stats: cli.Stats{
				Records: 4, Requests: 4, Questions: 4,
				Models: []string{"onesie-1.13.0"}, Attempts: 4,
				AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "4 records, 4 requests, 4 questions asked, 0 in / 0 out tokens, " +
				"model onesie-1.13.0, 4 attempts, 10s/attempt, 1s",
		},
		{
			name:  "should lead an empty stream with its records",
			stats: cli.Stats{AttemptTimeout: 10 * time.Second, Elapsed: time.Second},
			want:  "0 records, 0 requests, 0 questions asked, 0 in / 0 out tokens, 0 attempts, 1s",
		},
		{
			name: "should count the records a stream deduplicated apart from its requests",
			stats: cli.Stats{
				Records: 12, Dedups: 3, Requests: 9, Questions: 9,
				Models: []string{"onesie-1.13.0"}, Attempts: 9,
				AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "12 records, 3 deduplicated, 9 requests, 9 questions asked, 0 in / 0 out tokens, " +
				"model onesie-1.13.0, 9 attempts, 10s/attempt, 1s",
		},
		{
			name: "should count the records the cache answered after the deduplicated ones",
			stats: cli.Stats{
				Records: 12, Dedups: 3, Cached: 4, Requests: 5, Questions: 5,
				Models: []string{"onesie-1.13.0"}, Attempts: 5,
				AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "12 records, 3 deduplicated, 4 cached, 5 requests, 5 questions asked, 0 in / 0 out tokens, " +
				"model onesie-1.13.0, 5 attempts, 10s/attempt, 1s",
		},
		{
			name: "should render the cost the provider reported after the tokens",
			stats: cli.Stats{
				Records: 1, Requests: 1, Questions: 1, InputTokens: 10, OutputTokens: 2, Cost: &cost,
				Models: []string{"typesafe/jev"}, Attempts: 1,
				AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "1 request, 1 question asked, 10 in / 2 out tokens, $0.000012, model typesafe/jev, " +
				"1 attempt, 10s/attempt, 1s",
		},
		{
			name: "should render a one record run the cache answered",
			stats: cli.Stats{
				Records: 1, Cached: 1, Models: []string{"onesie-1.13.0"},
				AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "1 record, 1 cached, 0 requests, 0 questions asked, 0 in / 0 out tokens, " +
				"model onesie-1.13.0, 0 attempts, 1s",
		},
		{
			name: "should report the records a resume skipped",
			stats: cli.Stats{
				Requests: 2, Records: 2, Skipped: 18, Questions: 2,
				Models: []string{"onesie-1.13.0"}, Attempts: 2,
				AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "20 records, 18 skipped by --resume, 2 requests, 2 questions asked, 0 in / 0 out tokens, " +
				"model onesie-1.13.0, 2 attempts, 10s/attempt, 1s",
		},
		{
			name: "should count abstains beside false assertions",
			stats: cli.Stats{
				Requests: 3, Records: 3, FalseAsserts: 1, Abstains: 1, Questions: 3,
				Models: []string{"onesie-1.13.0"}, Attempts: 3,
				AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "3 records, 1 false assertion, 1 abstain, 3 requests, 3 questions asked, 0 in / 0 out tokens, " +
				"model onesie-1.13.0, 3 attempts, 10s/attempt, 1s",
		},
		{
			name: "should name a statusless retry transport",
			stats: cli.Stats{
				Requests: 1, Records: 1, Questions: 1,
				Models: []string{"onesie-1.13.0"}, Attempts: 3,
				Retries: map[int]int{0: 1, 429: 1}, AttemptTimeout: 10 * time.Second,
				Elapsed: time.Second,
			},
			want: "1 request, 1 question asked, 0 in / 0 out tokens, model onesie-1.13.0, " +
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
			want: "3 records, 1 failed, 2 requests, 2 questions asked, 0 in / 0 out tokens, " +
				"model onesie-1.13.0, 3 attempts (1 retry: 429×1), 10s/attempt, 1s",
		},
		{
			name: "should round a sub second elapsed to milliseconds",
			stats: cli.Stats{
				Records: 1, Requests: 1, Questions: 1, Attempts: 1,
				AttemptTimeout: 10 * time.Second, Elapsed: 3040959 * time.Nanosecond,
			},
			want: "1 request, 1 question asked, 0 in / 0 out tokens, 1 attempt, 10s/attempt, 3ms",
		},
		{
			name: "should round elapsed to a tenth of a second above one second",
			stats: cli.Stats{
				Records: 1, Requests: 1, Questions: 1, Attempts: 1,
				AttemptTimeout: 10 * time.Second, Elapsed: 11432123456 * time.Nanosecond,
			},
			want: "1 request, 1 question asked, 0 in / 0 out tokens, 1 attempt, 10s/attempt, 11.4s",
		},
		{
			name: "should omit the retry clause when nothing was retried",
			stats: cli.Stats{
				Records: 1, Requests: 1, Questions: 1, Models: []string{"onesie-1.13.0"},
				Attempts: 1, AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "1 request, 1 question asked, 0 in / 0 out tokens, model onesie-1.13.0, " +
				"1 attempt, 10s/attempt, 1s",
		},
		{
			name: "should render a false assertion count beside the failed count",
			stats: cli.Stats{
				Records: 40, Requests: 40, Failed: 2, FalseAsserts: 1, Questions: 80,
				InputTokens: 10000, OutputTokens: 400,
				Models: []string{"onesie-1.13.0"}, Attempts: 40,
				AttemptTimeout: 10 * time.Second, Elapsed: 30 * time.Second,
			},
			want: "40 records, 2 failed, 1 false assertion, 40 requests, 80 questions asked, " +
				"10000 in / 400 out tokens, model onesie-1.13.0, 40 attempts, 10s/attempt, 30s",
		},
		{
			name: "should omit the false assertion clause when every assertion held",
			stats: cli.Stats{
				Records: 40, Requests: 40, Questions: 80, InputTokens: 10000, OutputTokens: 400,
				Models: []string{"onesie-1.13.0"}, Attempts: 40,
				AttemptTimeout: 10 * time.Second, Elapsed: 30 * time.Second,
			},
			want: "40 records, 40 requests, 80 questions asked, 10000 in / 400 out tokens, model onesie-1.13.0, " +
				"40 attempts, 10s/attempt, 30s",
		},
		{
			name: "should omit the model clause when no model was seen",
			stats: cli.Stats{
				Records: 1, Requests: 1, Failed: 1, Questions: 1, Attempts: 1,
				AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "1 request, 1 failed, 1 question asked, 0 in / 0 out tokens, " +
				"1 attempt, 10s/attempt, 1s",
		},
		{
			name: "should say the answers came from the mock and count its attempts and no tokens",
			stats: cli.Stats{
				Mocked: true, Requests: 2, Questions: 2, Models: []string{"jev-latest"}, Attempts: 2,
				AttemptTimeout: 10 * time.Second, Elapsed: 0,
			},
			want: "2 records answered from the mock, 2 questions asked, 0 in / 0 out tokens, model jev-latest, " +
				"2 attempts, 10s/attempt, 0s",
		},
		{
			name: "should say one record came from the mock",
			stats: cli.Stats{
				Mocked: true, Requests: 1, Questions: 3, Models: []string{"jev-latest"}, Attempts: 3,
				Retries: map[int]int{503: 2}, AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "1 record answered from the mock, 3 questions asked, 0 in / 0 out tokens, model jev-latest, " +
				"3 attempts (2 retries: 503×2), 10s/attempt, 1s",
		},
		{
			name: "should count no record the mock failed as answered from it",
			stats: cli.Stats{
				Mocked: true, Records: 2, Requests: 2, Failed: 1, FailedRequests: 1, Questions: 2,
				Models: []string{"jev-latest"}, Attempts: 2, AttemptTimeout: 10 * time.Second, Elapsed: time.Second,
			},
			want: "2 records, 1 failed, 1 answered from the mock, 2 questions asked, 0 in / 0 out tokens, " +
				"model jev-latest, 2 attempts, 10s/attempt, 1s",
		},
		{
			name: "should count a mock run's records apart when a line never reached the mock",
			stats: cli.Stats{
				Mocked: true, Records: 3, Requests: 2, Failed: 2, Skipped: 1, FalseAsserts: 1,
				Questions: 2, Models: []string{"jev-latest"}, Attempts: 2, AttemptTimeout: 10 * time.Second,
				Elapsed: time.Second,
			},
			want: "4 records, 1 skipped by --resume, 2 failed, 1 false assertion, 2 answered from the mock, " +
				"2 questions asked, 0 in / 0 out tokens, model jev-latest, 2 attempts, 10s/attempt, 1s",
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
