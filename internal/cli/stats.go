package cli

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

// Stats is a finished summary, ready to render. It is a snapshot, so it needs no locking.
type Stats struct {
	// Records is every input record. Requests is the subset that reached the client, which is
	// smaller whenever a line failed to parse.
	Records        int
	Requests       int
	Failed         int
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
	parts := make([]string, 0, 8)

	// A non streaming run has one record and one request and says so once. A stream reports both
	// only when they differ, which is exactly when a record never became a request.
	split := s.Records > s.Requests

	if split {
		parts = append(parts, plural(s.Records, "record"))
	} else {
		parts = append(parts, plural(s.Requests, "request"))
	}

	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", s.Failed))
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
		s.Elapsed.String()), ", ")
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

func withStats(cmd *cobra.Command, flags *runFlags, body func(*collector) error) error {
	stats := &collector{}
	started := time.Now()

	err := body(stats)

	if !flags.stats {
		return err
	}

	summary := stats.snapshot(time.Duration(flags.timeout)*time.Second, time.Since(started))

	// Stderr, because stdout carries the answers. A summary on stdout would corrupt every
	// pipeline the flag exists to measure.
	if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), summary); printErr != nil {
		return printErr
	}

	return err
}

type collector struct {
	mu sync.Mutex

	records      int
	requests     int
	failed       int
	questions    int
	inputTokens  int
	outputTokens int
	models       map[string]struct{}
	attempts     int
	retries      map[int]int
	// pending holds the status of every failed attempt that has not yet been matched to the retry
	// it caused, keyed by the index it failed at. A run's last failure is never matched, which is
	// what keeps a terminal failure out of the retry breakdown.
	pending map[int][]int
}

func (c *collector) observe(a jev.Attempt) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.attempts++

	// Index counts from zero, so anything above it is a retry. The status the breakdown wants is
	// the one that caused the retry rather than the one the retry itself came back with, since a
	// successful retry returns 200 and section 10 asks whether rate limiting or transport ate the
	// time.
	if a.Index > 0 {
		c.credit(a.Index - 1)
	}

	if a.Err == nil && a.Status >= 200 && a.Status < 300 {
		return
	}

	if c.pending == nil {
		c.pending = make(map[int][]int)
	}

	c.pending[a.Index] = append(c.pending[a.Index], a.Status)
}

func (c *collector) credit(index int) {
	if c.retries == nil {
		c.retries = make(map[int]int)
	}

	queue := c.pending[index]
	if len(queue) == 0 {
		// Unreachable while the observer sees every attempt, since a retry only ever follows a
		// failure. Counted as statusless rather than dropped, so the total stays honest.
		c.retries[0]++

		return
	}

	c.retries[queue[0]]++
	c.pending[index] = queue[1:]
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

func (c *collector) recordFailure(reached bool, questions int) {
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

func (c *collector) snapshot(attemptTimeout, elapsed time.Duration) Stats {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Copied rather than handed out, because the collector keeps running until the process exits
	// and a caller holding the live map would read it without the lock.
	retries := make(map[int]int, len(c.retries))
	maps.Copy(retries, c.retries)

	return Stats{
		Records: c.records, Requests: c.requests, Failed: c.failed, Questions: c.questions,
		InputTokens: c.inputTokens, OutputTokens: c.outputTokens,
		Models: slices.Sorted(maps.Keys(c.models)), Attempts: c.attempts, Retries: retries,
		AttemptTimeout: attemptTimeout, Elapsed: elapsed,
	}
}

func observing(c *collector) []jev.Option {
	return []jev.Option{jev.WithAttemptObserver(c.observe)}
}
