// Package limits holds every API limit jev enforces locally and every constant it defaults to, so
// a binary built against stale numbers is diagnosable through --version.
package limits

import (
	"strconv"
	"time"
)

const (
	// MinChoiceOptions is jev's own floor. A one option choice has nothing to decide.
	MinChoiceOptions = 2
	// MaxChoiceOptions is the API's ceiling on options per choice question.
	MaxChoiceOptions = 255
	// MinScoreLevels is the API's floor on levels per score question.
	MinScoreLevels = 2
	// MaxScoreLevels is the API's ceiling on levels per score question.
	MaxScoreLevels = 10

	// DefaultAttemptTimeout bounds one HTTP attempt, not the whole call.
	DefaultAttemptTimeout = 10 * time.Second
	// DefaultRetries is how many times a retryable failure is retried.
	DefaultRetries = 2
	// DefaultMaxRetryAfter is the longest server requested delay jev will wait out.
	DefaultMaxRetryAfter = 60 * time.Second
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
		{Name: "max-retry-after", Value: DefaultMaxRetryAfter.String()},
	}
}

// Entry is one named constant, ready to print.
type Entry struct {
	Name  string
	Value string
}
