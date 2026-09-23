package engine_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/frodi-karlsson/jev-cli/internal/engine"
	"github.com/frodi-karlsson/jev-cli/internal/input"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		records     int
		jobs        int
		failAt      map[int]bool
		stopOnError bool
		wantLines   []string
		wantFailed  int
		wantAborted bool
	}{
		{
			name:      "should write one line per record",
			records:   5,
			jobs:      1,
			wantLines: []string{"0", "1", "2", "3", "4"},
		},
		{
			name:      "should keep input order with several workers",
			records:   50,
			jobs:      8,
			wantLines: countUp(50),
		},
		{
			name:      "should write nothing for an empty source",
			records:   0,
			jobs:      4,
			wantLines: nil,
		},
		{
			name:       "should count a failed record and continue",
			records:    5,
			jobs:       4,
			failAt:     map[int]bool{2: true},
			wantLines:  []string{"0", "1", "2", "3", "4"},
			wantFailed: 1,
		},
		{
			name:        "should stop at the first failure under stop on error",
			records:     20,
			jobs:        1,
			failAt:      map[int]bool{3: true},
			stopOnError: true,
			wantLines:   []string{"0", "1", "2", "3"},
			wantFailed:  1,
			wantAborted: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu      sync.Mutex
				written []string
			)

			result, err := engine.Run(t.Context(), engine.Config[input.Record, string]{
				Source: &counting{total: tc.records},
				Evaluate: func(_ context.Context, rec input.Record) (string, error) {
					if tc.failAt[rec.Index] {
						return strconv.Itoa(rec.Index), errors.New("boom")
					}

					return strconv.Itoa(rec.Index), nil
				},
				Write: func(line string) error {
					mu.Lock()
					defer mu.Unlock()

					written = append(written, line)

					return nil
				},
				Jobs:        tc.jobs,
				StopOnError: tc.stopOnError,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()

			if len(written) != len(tc.wantLines) {
				t.Fatalf("lines = %v, want %v", written, tc.wantLines)
			}

			for i := range written {
				if written[i] != tc.wantLines[i] {
					t.Errorf("line %d = %q, want %q", i, written[i], tc.wantLines[i])
				}
			}

			if result.Failed != tc.wantFailed {
				t.Errorf("failed = %d, want %d", result.Failed, tc.wantFailed)
			}

			if result.Aborted != tc.wantAborted {
				t.Errorf("aborted = %v, want %v", result.Aborted, tc.wantAborted)
			}
		})
	}

	t.Run("should run over a source of any record type", func(t *testing.T) {
		t.Parallel()

		source := &sliceSource[string]{items: []string{"a", "b", "c"}}

		var written []string

		result, err := engine.Run(t.Context(), engine.Config[string, string]{
			Source:   source,
			Evaluate: func(_ context.Context, rec string) (string, error) { return rec + "!", nil },
			Write:    func(line string) error { written = append(written, line); return nil },
			Jobs:     1,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		if result.Records != 3 {
			t.Errorf("Records = %d, want 3", result.Records)
		}

		want := []string{"a!", "b!", "c!"}
		if !slices.Equal(written, want) {
			t.Errorf("written = %v, want %v", written, want)
		}
	})

	t.Run("should write output in input order regardless of completion order", func(t *testing.T) {
		t.Parallel()

		t.Run("should write in input order when later records finish first", func(t *testing.T) {
			t.Parallel()

			// Record zero is the slowest, so a pipeline that wrote on completion rather than on
			// position would put it last. Every other record has to wait for it.
			var (
				mu      sync.Mutex
				written []string
			)

			result, err := engine.Run(t.Context(), engine.Config[input.Record, string]{
				Source: &counting{total: 12},
				Evaluate: func(_ context.Context, rec input.Record) (string, error) {
					if rec.Index == 0 {
						time.Sleep(60 * time.Millisecond)
					}

					return strconv.Itoa(rec.Index), nil
				},
				Write: func(line string) error {
					mu.Lock()
					defer mu.Unlock()

					written = append(written, line)

					return nil
				},
				Jobs: 8,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result.Records != 12 {
				t.Errorf("records = %d, want 12", result.Records)
			}

			mu.Lock()
			defer mu.Unlock()

			for i, line := range written {
				if line != strconv.Itoa(i) {
					t.Fatalf("line %d = %q, want %q. Written: %v", i, line, strconv.Itoa(i), written)
				}
			}
		})
	})

	t.Run("should bound concurrency to the job count", func(t *testing.T) {
		t.Parallel()

		t.Run("should never exceed the job count in flight", func(t *testing.T) {
			t.Parallel()

			var (
				live atomic.Int64
				peak atomic.Int64
			)

			const jobs = 4

			_, err := engine.Run(t.Context(), engine.Config[input.Record, string]{
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
				Write: func(string) error { return nil },
				Jobs:  jobs,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := peak.Load(); got > jobs {
				t.Errorf("peak in flight = %d, want at most %d", got, jobs)
			}

			if got := peak.Load(); got < 2 {
				t.Errorf("peak in flight = %d, the test never exercised concurrency", got)
			}
		})
	})

	t.Run("should surface a write failure to the caller", func(t *testing.T) {
		t.Parallel()

		t.Run("should treat a closed pipe as success and not deadlock", func(t *testing.T) {
			t.Parallel()

			done := make(chan struct{})

			var (
				result engine.Result
				runErr error
			)

			go func() {
				defer close(done)

				// Nothing is asserted in here. Logging to a test that has already failed its timeout
				// panics, which would mask the deadlock message with an unrelated failure.
				result, runErr = engine.Run(t.Context(), engine.Config[input.Record, string]{
					Source: &counting{total: 500},
					Evaluate: func(_ context.Context, rec input.Record) (string, error) {
						return strconv.Itoa(rec.Index), nil
					},
					Write: func(string) error { return syscall.EPIPE },
					Jobs:  4,
				})
			}()

			// The ordered engine deadlocks here if the writer returns without cancelling: the
			// dispatcher stays blocked on a queue nobody drains and workers.Wait never returns.
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return, the engine deadlocked on a closed pipe")
			}

			if runErr != nil {
				t.Errorf("a closed pipe is not an error, got %v", runErr)
			}

			if !result.Broken {
				t.Error("a closed pipe must set Broken")
			}
		})

		t.Run("should report a write failure that is not a closed pipe", func(t *testing.T) {
			t.Parallel()

			boom := errors.New("could not merge into the input line")

			result, err := engine.Run(t.Context(), engine.Config[input.Record, string]{
				Source: &counting{total: 10},
				Evaluate: func(_ context.Context, rec input.Record) (string, error) {
					return strconv.Itoa(rec.Index), nil
				},
				Write: func(string) error { return boom },
				Jobs:  2,
			})

			if !errors.Is(err, boom) {
				t.Errorf("error = %v, want the write failure surfaced", err)
			}

			if result.Broken {
				t.Error("a write failure of jev's own is not a closed pipe")
			}
		})
	})

	t.Run("should stop the run on an abort worthy failure", func(t *testing.T) {
		t.Parallel()

		t.Run("should stop at an abort worthy failure and write no cancellation records",
			func(t *testing.T) {
				t.Parallel()

				fatal := errors.New("unauthorized")

				var (
					mu      sync.Mutex
					written []string
				)

				result, err := engine.Run(t.Context(), engine.Config[input.Record, string]{
					Source: &counting{total: 200},
					Evaluate: func(ctx context.Context, rec input.Record) (string, error) {
						if rec.Index == 10 {
							return strconv.Itoa(rec.Index), fatal
						}

						// Every other record is slow enough that the abort lands while several are in
						// flight, which is where a worker ignoring the cancel would leak a spurious
						// record into the output.
						time.Sleep(5 * time.Millisecond)

						if ctx.Err() != nil {
							return "", ctx.Err()
						}

						return strconv.Itoa(rec.Index), nil
					},
					Write: func(line string) error {
						mu.Lock()
						defer mu.Unlock()

						written = append(written, line)

						return nil
					},
					Jobs:  8,
					Abort: func(err error) bool { return errors.Is(err, fatal) },
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				if !result.Aborted {
					t.Error("an abort worthy failure must set Aborted")
				}

				mu.Lock()
				defer mu.Unlock()

				// Output is a prefix of the input, never a prefix with a hole. How many records follow
				// the aborting one is not fixed, since it depends on which workers finished before the
				// cancel, but every line written must be in position.
				for i, line := range written {
					if line != strconv.Itoa(i) {
						t.Fatalf("line %d = %q, want %q. The output has a hole in it: %v",
							i, line, strconv.Itoa(i), written)
					}
				}

				if len(written) < 11 {
					t.Errorf("wrote %d lines, want at least the eleven up to the failure", len(written))
				}

				if len(written) == 200 {
					t.Error("the abort wrote every record, so it did not abort")
				}
			})
	})
}

type sliceSource[R any] struct {
	items []R
	at    int
}

func (s *sliceSource[R]) Next() (R, bool, error) {
	if s.at >= len(s.items) {
		var zero R

		return zero, false, nil
	}

	item := s.items[s.at]
	s.at++

	return item, true, nil
}

func countUp(n int) []string {
	lines := make([]string, 0, n)
	for i := range n {
		lines = append(lines, strconv.Itoa(i))
	}

	return lines
}

type counting struct {
	total int
	next  int
}

func (c *counting) Next() (input.Record, bool, error) {
	if c.next >= c.total {
		return input.Record{}, false, nil
	}

	record := input.Record{Index: c.next, Line: c.next + 1, State: fmt.Sprintf("state %d", c.next)}
	c.next++

	return record, true, nil
}
