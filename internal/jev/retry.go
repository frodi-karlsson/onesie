package jev

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

const (
	retryAfterHeader   = "Retry-After"
	retryAfterMsHeader = "Retry-After-Ms"
)

// DefaultRetryPolicy returns the policy the API's own SDK uses. The numbers are theirs.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:        limits.DefaultRetries,
		BackoffInitial:    500 * time.Millisecond,
		BackoffMax:        5 * time.Second,
		BackoffJitter:     0.25,
		RetryStatus:       DefaultRetryStatus,
		RespectRetryAfter: true,
		MaxRetryAfter:     limits.DefaultMaxRetryAfter,
		RetryConnection:   true,
		RetryTimeout:      true,
	}
}

// DefaultRetryStatus retries 408, 429 and every 5xx, which covers the 529 the API uses for overload.
func DefaultRetryStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		(status >= 500 && status <= 599)
}

func retryAfterTooLong(header http.Header, policy RetryPolicy, now time.Time) (time.Duration, bool) {
	if !policy.RespectRetryAfter || header == nil {
		return 0, false
	}

	delay, ok := parseRetryAfter(header, now)
	if !ok {
		return 0, false
	}

	return delay, delay > policy.MaxRetryAfter
}

func retryDelay(
	attempt int,
	header http.Header,
	policy RetryPolicy,
	now time.Time,
	random func() float64,
) time.Duration {
	if policy.RespectRetryAfter && header != nil {
		if delay, ok := parseRetryAfter(header, now); ok && delay <= policy.MaxRetryAfter {
			// Floor and jitter the server delay. A Retry-After of zero, or a date already in the
			// past, would otherwise release every waiting client at the same instant, which is the
			// stampede the jitter exists to prevent.
			if delay < policy.BackoffInitial {
				delay = policy.BackoffInitial
			}

			return jitter(delay, policy, random)
		}
	}

	return jitter(exponential(attempt, policy), policy, random)
}

func exponential(attempt int, policy RetryPolicy) time.Duration {
	// Doubling in a loop rather than shifting, so a large attempt count cannot overflow.
	delay := policy.BackoffInitial
	for i := 0; i < attempt && delay < policy.BackoffMax; i++ {
		delay *= 2
	}

	if delay > policy.BackoffMax {
		return policy.BackoffMax
	}

	return delay
}

func jitter(delay time.Duration, policy RetryPolicy, random func() float64) time.Duration {
	// Subtracted, so a delay never exceeds the value it was computed from, which holds because
	// random returns a value at or above zero. The upper bound does the work at the other edge,
	// since random staying below one keeps a fully jittered delay off zero.
	return time.Duration(math.Round(float64(delay) * (1 - random()*policy.BackoffJitter)))
}

// RetryPolicy controls which failures are retried and how long the client waits between attempts.
// A RetryStatus predicate must be safe for concurrent use.
type RetryPolicy struct {
	MaxRetries        int
	BackoffInitial    time.Duration
	BackoffMax        time.Duration
	BackoffJitter     float64
	RetryStatus       func(int) bool
	RespectRetryAfter bool
	MaxRetryAfter     time.Duration
	RetryConnection   bool
	RetryTimeout      bool
}

func (p RetryPolicy) validate() error {
	switch {
	case p.MaxRetries < 0:
		return &ValidationError{Message: "retry MaxRetries must not be negative"}
	case p.BackoffInitial < 0:
		return &ValidationError{Message: "retry BackoffInitial must not be negative"}
	case p.BackoffMax < 0:
		return &ValidationError{Message: "retry BackoffMax must not be negative"}
	case p.BackoffJitter < 0 || p.BackoffJitter > 1:
		return &ValidationError{Message: "retry BackoffJitter must be between 0 and 1"}
	case p.MaxRetryAfter < 0:
		return &ValidationError{Message: "retry MaxRetryAfter must not be negative"}
	case p.RetryStatus == nil:
		return &ValidationError{Message: "retry RetryStatus must not be nil"}
	default:
		return nil
	}
}

func parseRetryAfter(header http.Header, now time.Time) (time.Duration, bool) {
	// Retry-After-Ms wins because it is exact.
	if raw := header.Get(retryAfterMsHeader); raw != "" {
		if ms, err := strconv.ParseFloat(raw, 64); err == nil && ms >= 0 {
			return seconds(ms / 1000), true
		}
	}

	raw := header.Get(retryAfterHeader)
	if raw == "" {
		return 0, false
	}

	if value, err := strconv.ParseFloat(raw, 64); err == nil {
		if value < 0 {
			return 0, false
		}

		return seconds(value), true
	}

	if at, err := http.ParseTime(raw); err == nil {
		delay := at.Sub(now)
		if delay < 0 {
			return 0, true
		}

		return delay, true
	}

	return 0, false
}

func seconds(value float64) time.Duration {
	const maxSeconds = float64(math.MaxInt64) / float64(time.Second)

	// Clamped rather than relying on the conversion of a float that does not fit a Duration, which
	// the language spec leaves implementation dependent.
	if value >= maxSeconds {
		return time.Duration(math.MaxInt64)
	}

	return time.Duration(value * float64(time.Second))
}
