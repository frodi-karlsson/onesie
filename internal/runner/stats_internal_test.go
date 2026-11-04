package runner

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
)

func TestCollectorSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		workers int
		each    int
	}{
		{name: "should separate retries from terminals under concurrency", workers: 16, each: 50},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var c Collector

			var wg sync.WaitGroup

			for range tc.workers {
				wg.Add(1)

				go func() {
					defer wg.Done()

					for range tc.each {
						c.observe(onesie.Attempt{Index: 0, Status: http.StatusTooManyRequests})
						c.observe(onesie.Attempt{Index: 1, Status: http.StatusOK})
						c.Record("onesie-1.13.0", onesie.Usage{InputTokens: 2, OutputTokens: 1}, 1)

						c.observe(onesie.Attempt{Index: 0, Status: http.StatusBadRequest})
						c.TerminalAttempt(&onesie.APIError{Status: http.StatusBadRequest})
						c.RecordFailure(&onesie.APIError{Status: http.StatusBadRequest}, true, 1)
					}
				}()
			}

			wg.Wait()

			got := c.Snapshot(time.Second)
			each := tc.workers * tc.each

			if got.Attempts != 3*each {
				t.Errorf("Attempts = %d, want %d", got.Attempts, 3*each)
			}

			if got.Requests != 2*each {
				t.Errorf("Requests = %d, want %d", got.Requests, 2*each)
			}

			if got.Failed != each {
				t.Errorf("Failed = %d, want %d", got.Failed, each)
			}

			if got.Retries[http.StatusTooManyRequests] != each {
				t.Errorf("Retries[429] = %d, want %d",
					got.Retries[http.StatusTooManyRequests], each)
			}

			if _, named := got.Retries[http.StatusBadRequest]; named {
				t.Errorf("Retries names 400, which was never retried: %v", got.Retries)
			}

			if got.InputTokens != 2*each {
				t.Errorf("InputTokens = %d, want %d", got.InputTokens, 2*each)
			}
		})
	}
}

func TestCollectorObserve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		attempts []onesie.Attempt
		want     time.Duration
	}{
		{name: "should report no timeout before any attempt"},
		{
			name:     "should report the timeout each attempt ran with",
			attempts: []onesie.Attempt{{Status: http.StatusOK, Timeout: 10 * time.Second}},
			want:     10 * time.Second,
		},
		{
			name: "should report the longest timeout a mixed stream ran with",
			attempts: []onesie.Attempt{
				{Status: http.StatusOK, Timeout: 10 * time.Second},
				{Status: http.StatusOK, Timeout: time.Minute},
				{Status: http.StatusOK, Timeout: 10 * time.Second},
			},
			want: time.Minute,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var c Collector

			for _, attempt := range tc.attempts {
				c.observe(attempt)
			}

			if got := c.Snapshot(time.Second).AttemptTimeout; got != tc.want {
				t.Errorf("AttemptTimeout = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCollectorCachedRecord(t *testing.T) {
	t.Parallel()

	t.Run("should count a cached answer as a record and not a request, question or attempt", func(t *testing.T) {
		t.Parallel()

		var c Collector

		c.cachedRecord("onesie-1.13.0")
		c.cachedRecord("onesie-1.14.0")
		c.Record("onesie-1.13.0", onesie.Usage{InputTokens: 3, OutputTokens: 1}, 2)

		got := c.Snapshot(time.Second)

		if got.Records != 3 || got.Cached != 2 || got.Requests != 1 || got.Questions != 2 || got.Attempts != 0 {
			t.Errorf("snapshot = %+v, want 3 records, 2 cached, 1 request, 2 questions, 0 attempts", got)
		}

		if want := []string{"onesie-1.13.0", "onesie-1.14.0"}; !slices.Equal(got.Models, want) {
			t.Errorf("Models = %v, want %v", got.Models, want)
		}
	})
}

func TestCollectorRecordFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		cause        error
		wantRecords  int
		wantRequests int
		wantFailed   int
	}{
		{
			name:         "should count a record the server refused",
			cause:        &onesie.APIError{Status: http.StatusBadRequest},
			wantRecords:  1,
			wantRequests: 1,
			wantFailed:   1,
		},
		{
			name:  "should count nothing for a request the run cancelled",
			cause: fmt.Errorf("onesie: #1 POST /v1/one: %w", context.Canceled),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var c Collector

			c.RecordFailure(tc.cause, true, 1)

			got := c.Snapshot(time.Second)

			if got.Records != tc.wantRecords {
				t.Errorf("Records = %d, want %d", got.Records, tc.wantRecords)
			}

			if got.Requests != tc.wantRequests {
				t.Errorf("Requests = %d, want %d", got.Requests, tc.wantRequests)
			}

			if got.Failed != tc.wantFailed {
				t.Errorf("Failed = %d, want %d", got.Failed, tc.wantFailed)
			}
		})
	}
}

func TestCollectorDeduplicated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cause       error
		wantRecords int
		wantDedups  int
		wantFailed  int
	}{
		{name: "should count an answered duplicate as a record and a dedup", wantRecords: 1, wantDedups: 1},
		{
			name:        "should count a duplicate of a failed request as failed too",
			cause:       &onesie.APIError{Status: http.StatusServiceUnavailable},
			wantRecords: 1, wantDedups: 1, wantFailed: 1,
		},
		{
			name:  "should count nothing for a duplicate the run cancelled",
			cause: fmt.Errorf("onesie: #1: %w", context.Canceled),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var c Collector

			c.deduplicated(tc.cause)

			got := c.Snapshot(time.Second)

			if got.Records != tc.wantRecords || got.Dedups != tc.wantDedups || got.Failed != tc.wantFailed {
				t.Errorf("records, dedups, failed = %d, %d, %d, want %d, %d, %d",
					got.Records, got.Dedups, got.Failed, tc.wantRecords, tc.wantDedups, tc.wantFailed)
			}

			if got.Requests != 0 || got.Questions != 0 || got.Attempts != 0 {
				t.Errorf("requests, questions, attempts = %d, %d, %d, want none",
					got.Requests, got.Questions, got.Attempts)
			}
		})
	}
}

func TestTerminalStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCounted bool
	}{
		{
			name: "should take the status from an API error",
			err:  &onesie.APIError{Status: http.StatusBadRequest}, wantStatus: 400, wantCounted: true,
		},
		{
			name: "should reach the API error a retry after error embeds",
			err: &onesie.RetryAfterError{
				APIError: onesie.APIError{Status: http.StatusTooManyRequests},
			},
			wantStatus: 429, wantCounted: true,
		},
		{
			name: "should count a transport failure as statusless",
			err:  &onesie.ConnectionError{Err: errors.New("reset")}, wantCounted: true,
		},
		{
			name:        "should count a timeout as statusless",
			err:         &onesie.TimeoutError{ConnectionError: onesie.ConnectionError{Err: errors.New("x")}},
			wantCounted: true,
		},
		{
			name: "should count an interrupt as statusless",
			err:  fmt.Errorf("onesie: #1: %w", context.Canceled), wantCounted: true,
		},
		{
			// Its attempt came back 2xx and was counted as a success, so subtracting it here
			// would hide a real transport retry.
			name: "should not count a 2xx body that could not be used",
			err:  &onesie.ResponseError{Status: http.StatusOK, Err: errors.New("bad json")},
		},
		{
			name: "should not count a request rejected before it was sent",
			err:  &onesie.ValidationError{Message: "no API key"},
		},
		{
			name: "should not count an answer the response was missing",
			err:  &onesie.AnswerError{Name: "urgent", Missing: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, counted := terminalStatus(tc.err)
			if status != tc.wantStatus || counted != tc.wantCounted {
				t.Errorf("terminalStatus() = (%d, %t), want (%d, %t)",
					status, counted, tc.wantStatus, tc.wantCounted)
			}
		})
	}
}
