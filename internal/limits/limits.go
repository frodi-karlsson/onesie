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
	// MaxRetries is the ceiling on --retries. Each retry waits out a backoff of up to 5 s, so a
	// hundred bounds the wait at minutes rather than days.
	MaxRetries = 100
	// MaxJobs is the ceiling on -j. Every job is a goroutine and a pooled connection started up
	// front, and far past this the API rate limits the run long before it goes any faster.
	MaxJobs = 256
	// MaxSeconds is the ceiling on a flag given in whole seconds. A day is past any sensible
	// timeout and far from the Duration overflow that shrinks a huge number.
	MaxSeconds = 86400

	// MaxLineBytes is the longest record onesie reads, and the most a --map result may encode to.
	// bufio's 64KiB is too small for a document, and no cap at all lets a line exhaust memory.
	MaxLineBytes = 8 << 20
	// MaxMapDepth is the deepest a --map result may nest. encoding/json refuses past 10000 levels,
	// and the request body wraps the state in one object more.
	MaxMapDepth = 10000 - 1
	// MaxIDBytes is the longest an --id may write out to. Every id is held for the run to find
	// repeats, and printed on every output line.
	MaxIDBytes = 1024
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
		{Name: "max-jobs", Value: strconv.Itoa(MaxJobs)},
		{Name: "max-retry-after", Value: DefaultMaxRetryAfter.String()},
		{Name: "max-seconds", Value: strconv.Itoa(MaxSeconds)},
		{Name: "max-line-bytes", Value: strconv.Itoa(MaxLineBytes)},
		{Name: "max-map-depth", Value: strconv.Itoa(MaxMapDepth)},
		{Name: "max-id-bytes", Value: strconv.Itoa(MaxIDBytes)},
	}
}

// Entry is one named constant, ready to print.
type Entry struct {
	Name  string
	Value string
}
