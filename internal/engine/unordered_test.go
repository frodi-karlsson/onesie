package engine_test

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
)

func TestRunUnordered(t *testing.T) {
	t.Parallel()

	t.Run("should emit every record exactly once", func(t *testing.T) {
		t.Parallel()

		var (
			mu      sync.Mutex
			written []string
		)

		result, err := engine.Run(t.Context(), engine.Config[input.Record, string]{
			Source: &counting{total: 100},
			Evaluate: func(_ context.Context, rec input.Record) (string, error) {
				return strconv.Itoa(rec.Index), nil
			},
			Write: func(line string) error {
				mu.Lock()
				defer mu.Unlock()

				written = append(written, line)

				return nil
			},
			Jobs:      8,
			Unordered: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Records != 100 {
			t.Errorf("records = %d, want 100", result.Records)
		}

		mu.Lock()
		defer mu.Unlock()

		sort.Strings(written)

		seen := map[string]int{}
		for _, line := range written {
			seen[line]++
		}

		for i := range 100 {
			if seen[strconv.Itoa(i)] != 1 {
				t.Errorf("record %d written %d times, want once", i, seen[strconv.Itoa(i)])
			}
		}
	})

	t.Run("should let a later record overtake a slow one", func(t *testing.T) {
		t.Parallel()

		// Record zero is slow, so seeing anything before it is the only assertion that tells the
		// two modes apart. Without it an --unordered that behaved as ordered would pass every case.
		var (
			mu      sync.Mutex
			written []string
		)

		if _, err := engine.Run(t.Context(), engine.Config[input.Record, string]{
			Source: &counting{total: 20},
			Evaluate: func(_ context.Context, rec input.Record) (string, error) {
				if rec.Index == 0 {
					time.Sleep(80 * time.Millisecond)
				}

				return strconv.Itoa(rec.Index), nil
			},
			Write: func(line string) error {
				mu.Lock()
				defer mu.Unlock()

				written = append(written, line)

				return nil
			},
			Jobs:      8,
			Unordered: true,
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()

		if len(written) == 0 || written[0] == "0" {
			t.Errorf("record zero was slowest and still came first, so nothing overtook it: %v",
				written)
		}
	})

	t.Run("should never exceed the job count in flight", func(t *testing.T) {
		t.Parallel()

		var (
			live atomic.Int64
			peak atomic.Int64
		)

		const jobs = 4

		if _, err := engine.Run(t.Context(), engine.Config[input.Record, string]{
			Source: &counting{total: 200},
			Evaluate: func(_ context.Context, rec input.Record) (string, error) {
				current := live.Add(1)
				defer live.Add(-1)

				for {
					best := peak.Load()
					if current <= best || peak.CompareAndSwap(best, current) {
						break
					}
				}

				time.Sleep(time.Millisecond)

				return strconv.Itoa(rec.Index), nil
			},
			Write:     func(string) error { return nil },
			Jobs:      jobs,
			Unordered: true,
		}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got := peak.Load(); got > jobs {
			t.Errorf("peak in flight = %d, want at most %d", got, jobs)
		}
	})

	t.Run("should stop at an abort worthy failure", func(t *testing.T) {
		t.Parallel()

		fatal := errors.New("unauthorized")

		var written atomic.Int64

		result, err := engine.Run(t.Context(), engine.Config[input.Record, string]{
			Source: &counting{total: 300},
			Evaluate: func(ctx context.Context, rec input.Record) (string, error) {
				if rec.Index == 10 {
					return strconv.Itoa(rec.Index), fatal
				}

				time.Sleep(2 * time.Millisecond)

				if ctx.Err() != nil {
					return "", ctx.Err()
				}

				return strconv.Itoa(rec.Index), nil
			},
			Write: func(string) error {
				written.Add(1)

				return nil
			},
			Jobs:      8,
			Unordered: true,
			Abort:     func(err error) bool { return errors.Is(err, fatal) },
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !result.Aborted {
			t.Error("an abort worthy failure must set Aborted")
		}

		if got := written.Load(); got == 300 {
			t.Error("the abort wrote every record, so it did not abort")
		}
	})

	t.Run("should treat a closed pipe as success and not hang", func(t *testing.T) {
		t.Parallel()

		done := make(chan struct{})

		var (
			result engine.Result
			runErr error
		)

		go func() {
			defer close(done)

			// Nothing is asserted in here. Logging to a test that has already failed its timeout
			// panics, which would mask the hang with an unrelated failure.
			result, runErr = engine.Run(t.Context(), engine.Config[input.Record, string]{
				Source: &counting{total: 500},
				Evaluate: func(_ context.Context, rec input.Record) (string, error) {
					return strconv.Itoa(rec.Index), nil
				},
				Write: func(string) error {
					// The pause makes this a real guard. Drain stops at the first write and without
					// the pause usually returns before the workers fill the completion channel, so
					// a missing select would go unnoticed.
					time.Sleep(50 * time.Millisecond)

					return syscall.EPIPE
				},
				Jobs:      4,
				Unordered: true,
			})
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not return, a worker is blocked on the completion channel")
		}

		if runErr != nil {
			t.Errorf("a closed pipe is not an error, got %v", runErr)
		}

		if !result.Broken {
			t.Error("a closed pipe must set Broken")
		}
	})

	t.Run("should end an interrupted stream", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name          string
			sourceResumes bool
		}{
			{name: "should report an interrupt that lands while the source is waiting", sourceResumes: true},
			{name: "should return while the source is still blocked", sourceResumes: false},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				result, written, err := interruptStream(t, true, tc.sourceResumes)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if !result.Aborted || !errors.Is(result.Cause, context.Canceled) {
					t.Errorf("aborted = %v, cause = %v, want an interrupted run", result.Aborted, result.Cause)
				}

				sort.Strings(written)

				if !slices.Equal(written, []string{"0", "1"}) {
					t.Errorf("written = %v, want the two records read before the interrupt", written)
				}
			})
		}
	})

	t.Run("should read no further ahead than the job count", func(t *testing.T) {
		t.Parallel()

		const jobs = 3

		if reads := readAhead(t, jobs, true); reads > jobs+1 {
			t.Errorf("read %d records with %d jobs busy, want at most %d", reads, jobs, jobs+1)
		}
	})
}
