package jev

import (
	"math"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
		ok     bool
	}{
		{
			name:   "should prefer retry after ms over retry after",
			header: http.Header{"Retry-After-Ms": {"250"}, "Retry-After": {"30"}},
			want:   250 * time.Millisecond,
			ok:     true,
		},
		{
			name:   "should read retry after as whole seconds",
			header: http.Header{"Retry-After": {"7"}},
			want:   7 * time.Second,
			ok:     true,
		},
		{
			name:   "should read retry after as fractional seconds",
			header: http.Header{"Retry-After": {"1.5"}},
			want:   1500 * time.Millisecond,
			ok:     true,
		},
		{
			name:   "should read retry after as an http date",
			header: http.Header{"Retry-After": {"Sun, 20 Sep 2026 12:00:05 GMT"}},
			want:   5 * time.Second,
			ok:     true,
		},
		{
			name:   "should clamp a past http date to zero",
			header: http.Header{"Retry-After": {"Sun, 20 Sep 2026 11:59:00 GMT"}},
			want:   0,
			ok:     true,
		},
		{
			name:   "should reject a negative seconds value",
			header: http.Header{"Retry-After": {"-5"}},
			want:   0,
			ok:     false,
		},
		{
			name:   "should report absent when no header is present",
			header: http.Header{},
			want:   0,
			ok:     false,
		},
		{
			name:   "should report absent for an unparsable value",
			header: http.Header{"Retry-After": {"soon"}},
			want:   0,
			ok:     false,
		},
		{
			name:   "should clamp a value too large for a duration",
			header: http.Header{"Retry-After": {"1e300"}},
			want:   time.Duration(math.MaxInt64),
			ok:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := parseRetryAfter(tc.header, now)

			if ok != tc.ok {
				t.Fatalf("ok got %v, want %v", ok, tc.ok)
			}

			if got != tc.want {
				t.Errorf("duration got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	noJitter := func() float64 { return 0 }
	fullJitter := func() float64 { return 1 }

	t.Run("should double the backoff on each attempt", func(t *testing.T) {
		t.Parallel()

		policy := DefaultRetryPolicy()

		want := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
		for attempt, expected := range want {
			if got := retryDelay(attempt, nil, policy, now, noJitter); got != expected {
				t.Errorf("attempt %d got %v, want %v", attempt, got, expected)
			}
		}
	})

	t.Run("should cap the backoff at BackoffMax", func(t *testing.T) {
		t.Parallel()

		policy := DefaultRetryPolicy()

		if got := retryDelay(20, nil, policy, now, noJitter); got != policy.BackoffMax {
			t.Errorf("got %v, want %v", got, policy.BackoffMax)
		}
	})

	t.Run("should subtract jitter rather than adding it", func(t *testing.T) {
		t.Parallel()

		policy := DefaultRetryPolicy()

		got := retryDelay(0, nil, policy, now, fullJitter)

		if want := 375 * time.Millisecond; got != want {
			t.Errorf("got %v, want %v", got, want)
		}

		if got > policy.BackoffInitial {
			t.Errorf("jitter overshot the exponential delay")
		}
	})

	t.Run("should use the server delay when it is within MaxRetryAfter", func(t *testing.T) {
		t.Parallel()

		policy := DefaultRetryPolicy()
		header := http.Header{"Retry-After": {"2"}}

		if got := retryDelay(0, header, policy, now, noJitter); got != 2*time.Second {
			t.Errorf("got %v, want %v", got, 2*time.Second)
		}
	})

	t.Run("should ignore a server delay above MaxRetryAfter", func(t *testing.T) {
		t.Parallel()

		policy := DefaultRetryPolicy()
		header := http.Header{"Retry-After": {"120"}}

		if got := retryDelay(0, header, policy, now, noJitter); got != policy.BackoffInitial {
			t.Errorf("got %v, want the backoff %v", got, policy.BackoffInitial)
		}
	})

	t.Run("should ignore the server delay when RespectRetryAfter is off", func(t *testing.T) {
		t.Parallel()

		policy := DefaultRetryPolicy()
		policy.RespectRetryAfter = false
		header := http.Header{"Retry-After": {"2"}}

		if got := retryDelay(0, header, policy, now, noJitter); got != policy.BackoffInitial {
			t.Errorf("got %v, want the backoff %v", got, policy.BackoffInitial)
		}
	})

	t.Run("should jitter the server delay too", func(t *testing.T) {
		t.Parallel()

		policy := DefaultRetryPolicy()
		header := http.Header{"Retry-After": {"4"}}

		got := retryDelay(0, header, policy, now, fullJitter)

		if want := 3 * time.Second; got != want {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("should floor a zero server delay at BackoffInitial", func(t *testing.T) {
		t.Parallel()

		policy := DefaultRetryPolicy()

		for _, raw := range []string{"0", "Sun, 20 Sep 2026 11:00:00 GMT"} {
			header := http.Header{"Retry-After": {raw}}

			got := retryDelay(0, header, policy, now, noJitter)
			if got != policy.BackoffInitial {
				t.Errorf("%q got %v, want the floor %v", raw, got, policy.BackoffInitial)
			}
		}
	})

	t.Run("should keep a server delay above the floor unchanged", func(t *testing.T) {
		t.Parallel()

		policy := DefaultRetryPolicy()
		header := http.Header{"Retry-After-Ms": {"900"}}

		if got := retryDelay(0, header, policy, now, noJitter); got != 900*time.Millisecond {
			t.Errorf("got %v, want %v", got, 900*time.Millisecond)
		}
	})
}

func TestDefaultRetryStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status int
		want   bool
	}{
		{status: 408, want: true},
		{status: 429, want: true},
		{status: 500, want: true},
		{status: 529, want: true},
		{status: 599, want: true},
		{status: 200, want: false},
		{status: 400, want: false},
		{status: 422, want: false},
		{status: 600, want: false},
	}

	for _, tc := range tests {
		name := "should not retry " + strconv.Itoa(tc.status)
		if tc.want {
			name = "should retry " + strconv.Itoa(tc.status)
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := DefaultRetryStatus(tc.status); got != tc.want {
				t.Errorf("status %d got %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestRetryPolicyValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*RetryPolicy)
		wantErr bool
	}{
		{name: "should accept the defaults", mutate: func(*RetryPolicy) {}},
		{name: "should reject negative MaxRetries", mutate: func(p *RetryPolicy) { p.MaxRetries = -1 }, wantErr: true},
		{name: "should reject jitter above one", mutate: func(p *RetryPolicy) { p.BackoffJitter = 1.5 }, wantErr: true},
		{name: "should reject jitter below zero", mutate: func(p *RetryPolicy) { p.BackoffJitter = -0.1 }, wantErr: true},
		{name: "should reject a negative backoff", mutate: func(p *RetryPolicy) { p.BackoffInitial = -time.Second }, wantErr: true},
		{name: "should reject a nil RetryStatus", mutate: func(p *RetryPolicy) { p.RetryStatus = nil }, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policy := DefaultRetryPolicy()
			tc.mutate(&policy)

			err := policy.validate()

			if tc.wantErr && err == nil {
				t.Errorf("expected an error, got none")
			}

			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
