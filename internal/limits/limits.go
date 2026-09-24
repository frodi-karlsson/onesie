// Package limits holds every API limit onesie enforces locally and every constant it defaults to, so
// a binary built against stale numbers is diagnosable through --version.
package limits

import (
	"strconv"
	"time"
)

const (
	// MinChoiceOptions is onesie's own floor. A one option choice has nothing to decide.
	MinChoiceOptions = 2
	// MaxChoiceOptions is the API's ceiling on options per choice question.
	MaxChoiceOptions = 255
	// MinScoreLevels is onesie's own floor. A one level score has nothing to rank.
	MinScoreLevels = 2
	// MaxScoreLevels is the API's ceiling on levels per score question.
	MaxScoreLevels = 10

	// DefaultAttemptTimeout bounds one HTTP attempt, not the whole call.
	DefaultAttemptTimeout = 10 * time.Second
	// DefaultRetries is how many times a retryable failure is retried.
	DefaultRetries = 2
	// DefaultMaxRetryAfter is the longest server requested delay onesie will wait out.
	DefaultMaxRetryAfter = 60 * time.Second
	// MaxRetries is the ceiling on --retries. Each retry waits out the backoff, capped at 5 s, so a
	// large count is a run that never ends rather than a policy. A hundred is already far past any
	// useful policy and bounds the wait at minutes rather than days.
	MaxRetries = 100
	// MaxSeconds is the ceiling on a flag given in whole seconds. A day is beyond any sensible
	// attempt timeout or server requested delay, and it leaves the conversion to a Duration far
	// from the overflow that would turn a huge number into a fraction of a second.
	MaxSeconds = 86400
)

// Report lists every constant in this package, for the --version dump.
func Report() []Entry {
	return []Entry{
		{Name: "min-choice-options", Value: strconv.Itoa(MinChoiceOptions)},
		{Name: "max-choice-options", Value: strconv.Itoa(MaxChoiceOptions)},
		{Name: "min-score-levels", Value: strconv.Itoa(MinScoreLevels)},
		{Name: "max-score-levels", Value: strconv.Itoa(MaxScoreLevels)},
		{Name: "attempt-timeout", Value: DefaultAttemptTimeout.String()},
		{Name: "retries", Value: strconv.Itoa(DefaultRetries)},
		{Name: "max-retries", Value: strconv.Itoa(MaxRetries)},
		{Name: "max-retry-after", Value: DefaultMaxRetryAfter.String()},
		{Name: "max-seconds", Value: strconv.Itoa(MaxSeconds)},
	}
}

// Entry is one named constant, ready to print.
type Entry struct {
	Name  string
	Value string
}
