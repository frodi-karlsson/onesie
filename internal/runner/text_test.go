package runner

import "testing"

func TestPlural(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		count int
		noun  string
		want  string
	}{
		{name: "should leave a single request bare", count: 1, noun: "request", want: "1 request"},
		{name: "should add an s to two requests", count: 2, noun: "request", want: "2 requests"},
		{
			name: "should leave a single question bare", count: 1, noun: "question",
			want: "1 question",
		},
		{
			name: "should add an s to two questions", count: 2, noun: "question",
			want: "2 questions",
		},
		{name: "should leave a single attempt bare", count: 1, noun: "attempt", want: "1 attempt"},
		{name: "should add an s to two attempts", count: 2, noun: "attempt", want: "2 attempts"},
		{
			name: "should leave a single false assertion bare", count: 1, noun: "false assertion",
			want: "1 false assertion",
		},
		{
			name: "should add an s to two false assertions", count: 2, noun: "false assertion",
			want: "2 false assertions",
		},
		{name: "should leave a single retry bare", count: 1, noun: "retry", want: "1 retry"},
		{name: "should spell two retries irregularly", count: 2, noun: "retry", want: "2 retries"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := plural(tc.count, tc.noun); got != tc.want {
				t.Errorf("plural(%d, %q) = %q, want %q", tc.count, tc.noun, got, tc.want)
			}
		})
	}
}
