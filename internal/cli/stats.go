package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

func withStats(
	cmd *cobra.Command,
	now func() time.Time,
	flags *runFlags,
	body func(*collector) error,
) error {
	stats := &collector{}
	started := now()

	err := body(stats)

	if !flags.stats {
		return err
	}

	summary := stats.snapshot(time.Duration(flags.timeout)*time.Second, now().Sub(started))

	// A run that failed before it read a record or made an attempt never started, and a line of
	// zeros beside the error reads as a run that was made and came back empty. An empty stream is
	// the other case: it succeeded, and 0 records is the measurement. A resume that skipped every
	// record read them all, so its gate's exit code gets the line that explains it.
	if err != nil && summary.Records == 0 && summary.Attempts == 0 && summary.Skipped == 0 {
		return err
	}

	// Stderr, because stdout carries the answers.
	//
	// The run's own error wins, since a summary that failed to print is the smaller loss.
	if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), summary); printErr != nil && err == nil {
		return printErr
	}

	return err
}

func observing(c *collector) []jev.Option {
	return []jev.Option{jev.WithAttemptObserver(c.observe)}
}

type collector struct {
	mu sync.Mutex

	records        int
	skipped        int
	requests       int
	failed         int
	falseAsserts   int
	abstainCount   int
	questions      int
	inputTokens    int
	outputTokens   int
	models         map[string]struct{}
	attempts       int
	failedAttempts map[int]int // Non 2xx attempts by status. Less terminal, it is the retry breakdown.
	terminal       map[int]int
}

func (c *collector) observe(a jev.Attempt) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.attempts++

	if a.Err == nil && a.Status >= 200 && a.Status < 300 {
		return
	}

	if c.failedAttempts == nil {
		c.failedAttempts = make(map[int]int)
	}

	c.failedAttempts[a.Status]++
}

func (c *collector) terminalAttempt(err error) {
	status, counted := terminalStatus(err)
	if !counted {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.terminal == nil {
		c.terminal = make(map[int]int)
	}

	c.terminal[status]++
}

func terminalStatus(err error) (int, bool) {
	var api *jev.APIError
	if errors.As(err, &api) {
		return api.Status, true
	}

	var connection *jev.ConnectionError
	if errors.As(err, &connection) {
		// A transport failure has no status, which is the key its attempt was counted under.
		return 0, true
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// An interrupt reaches the caller as the context error itself, and the attempt it cut
		// short was observed with no status like any other transport failure.
		return 0, true
	}

	// Anything else either never reached the wire or came back 2xx, so its attempt was never
	// counted as failed. Subtracting it would hide a real retry.
	return 0, false
}

func (c *collector) record(model string, usage jev.Usage, questions int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.records++
	c.requests++
	c.questions += questions
	c.inputTokens += usage.InputTokens
	c.outputTokens += usage.OutputTokens

	if model != "" {
		if c.models == nil {
			c.models = make(map[string]struct{})
		}

		c.models[model] = struct{}{}
	}
}

func (c *collector) recordFailure(cause error, reached bool, questions int) {
	// A stop or an interrupt cancels whatever was in flight, and those requests come back
	// context.Canceled. Nothing about them failed, so counting them reports failed records for a
	// run in which nothing failed. writeFailure skips the same error for the same reason.
	if errors.Is(cause, context.Canceled) {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.records++
	c.failed++
	c.questions += questions

	// An input error never reached the client, so it is a record and not a request. Counting it
	// as both is what made the retry total wrong when it was derived by subtraction.
	if reached {
		c.requests++
	}
}

func (c *collector) skip(skips tally) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.skipped += skips.records
	c.falseAsserts += skips.rejected
	c.abstainCount += skips.abstained
}

func (c *collector) assertFailed() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.falseAsserts++
}

func (c *collector) falseAssertions() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.falseAsserts
}

func (c *collector) abstained() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.abstainCount++
}

func (c *collector) abstains() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.abstainCount
}

func (c *collector) snapshot(attemptTimeout, elapsed time.Duration) Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Built here rather than handed out, because the collector keeps running until the process
	// exits and a caller holding a live map would read it without the lock.
	retries := make(map[int]int, len(c.failedAttempts))

	// Every failed attempt either caused a retry or ended its record, so what is left after the
	// terminals are taken out is exactly what was retried.
	for status, count := range c.failedAttempts {
		if left := count - c.terminal[status]; left > 0 {
			retries[status] = left
		}
	}

	return Stats{
		Records: c.records, Skipped: c.skipped, Requests: c.requests, Failed: c.failed,
		FalseAsserts: c.falseAsserts, Abstains: c.abstainCount, Questions: c.questions,
		InputTokens: c.inputTokens, OutputTokens: c.outputTokens,
		Models: slices.Sorted(maps.Keys(c.models)), Attempts: c.attempts, Retries: retries,
		AttemptTimeout: attemptTimeout, Elapsed: elapsed,
	}
}

// Stats is a finished summary, ready to render. It is a snapshot, so it needs no locking.
type Stats struct {
	// Records is every input record a resume did not skip. Requests is the subset that reached the
	// client, which is smaller whenever a line failed to parse.
	Records int
	// Skipped counts the records a resume left alone, since the file already answered them.
	Skipped  int
	Requests int
	Failed   int
	// FalseAsserts counts the records whose assertion did not hold. §17.6 counts them apart from
	// Failed, since a false assertion judges a complete record.
	FalseAsserts int
	// Abstains counts the records whose assertion did not hold and whose abstain expression did.
	Abstains       int
	Questions      int
	InputTokens    int
	OutputTokens   int
	Models         []string
	Attempts       int
	Retries        map[int]int
	AttemptTimeout time.Duration
	Elapsed        time.Duration
}

// String renders the summary as the single line --stats writes to stderr.
func (s Stats) String() string {
	parts := make([]string, 0, 10)

	// A non streaming run has one record and one request and says so once. A stream reports both
	// only when they differ, which is exactly when a record never became a request.
	split := s.Records > s.Requests

	if split {
		parts = append(parts, plural(s.Records, "record"))
	} else {
		parts = append(parts, plural(s.Requests, "request"))
	}

	if s.Skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", s.Skipped))
	}

	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.Failed))
	}

	if s.FalseAsserts > 0 {
		parts = append(parts, plural(s.FalseAsserts, "false assertion"))
	}

	if s.Abstains > 0 {
		parts = append(parts, plural(s.Abstains, "abstain"))
	}

	if split {
		parts = append(parts, plural(s.Requests, "request"))
	}

	parts = append(parts,
		plural(s.Questions, "question"),
		fmt.Sprintf("%d in / %d out", s.InputTokens, s.OutputTokens))

	if model := s.modelClause(); model != "" {
		parts = append(parts, model)
	}

	return strings.Join(append(parts,
		s.attemptClause(),
		s.AttemptTimeout.String()+"/attempt",
		roundElapsed(s.Elapsed)), ", ")
}

func roundElapsed(d time.Duration) string {
	// A Duration prints every digit it holds, which puts nanoseconds in a summary section 10
	// writes as 11.4s. Rounded to the resolution a reader can act on.
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}

	return d.Round(100 * time.Millisecond).String()
}

func (s Stats) modelClause() string {
	switch len(s.Models) {
	case 0:
		return ""
	case 1:
		return "model " + s.Models[0]
	default:
		return "models " + strings.Join(s.Models, ", ")
	}
}

func (s Stats) attemptClause() string {
	// Summed from the breakdown rather than derived as Attempts minus Requests. A record that
	// failed to parse never reached the client, so it raises the record count without raising the
	// attempt count, and the subtraction would under report every retry that run made.
	retries := 0
	for _, count := range s.Retries {
		retries += count
	}

	if retries == 0 {
		return plural(s.Attempts, "attempt")
	}

	return fmt.Sprintf("%s (%s: %s)",
		plural(s.Attempts, "attempt"), plural(retries, "retry"), s.retryBreakdown())
}

func (s Stats) retryBreakdown() string {
	codes := slices.Sorted(maps.Keys(s.Retries))
	parts := make([]string, 0, len(codes))

	for _, code := range codes {
		// A connection error or a timeout has no status. Section 10 wants this breakdown to
		// separate rate limiting from transport, and a literal 0 answers neither question.
		label := strconv.Itoa(code)
		if code == 0 {
			label = "transport"
		}

		parts = append(parts, fmt.Sprintf("%s×%d", label, s.Retries[code]))
	}

	return strings.Join(parts, ", ")
}
