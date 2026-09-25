package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
)

const dedupWorkers = 32

func TestDedup(t *testing.T) {
	t.Parallel()

	answered := output.Record{Model: "m"}
	refused := &jev.APIError{Status: 503}
	keyOf := func(name string) [sha256.Size]byte { return sha256.Sum256([]byte(name)) }

	t.Run("should run ask once for one key and hand every caller the record", func(t *testing.T) {
		t.Parallel()

		shared := newDedup(10, func() { t.Error("the notice ran below the limit") })

		var asked atomic.Int32

		results := together(t, dedupWorkers, func(context.Context) (output.Record, bool, error) {
			record, _, duplicate, err := shared.share(t.Context(), keyOf("a"), func() (output.Record, error) {
				asked.Add(1)

				return answered, nil
			})

			return record, duplicate, err
		})

		if got := asked.Load(); got != 1 {
			t.Errorf("ask ran %d times, want 1", got)
		}

		checkShared(t, results, answered, nil, dedupWorkers-1)
	})

	t.Run("should make a caller wait for the request in flight and give it that result", func(t *testing.T) {
		t.Parallel()

		shared := newDedup(10, func() {})
		release := make(chan struct{})
		started := make(chan struct{})

		var asked atomic.Int32

		ownerDone := make(chan error, 1)

		go func() {
			_, _, _, err := shared.share(t.Context(), keyOf("a"), func() (output.Record, error) {
				asked.Add(1)
				close(started)
				<-release

				return answered, nil
			})
			ownerDone <- err
		}()

		<-started

		var waiting sync.WaitGroup

		results := make(chan sharedResult, dedupWorkers)

		for range dedupWorkers {
			waiting.Add(1)

			go func() {
				defer waiting.Done()

				record, _, duplicate, err := shared.share(t.Context(), keyOf("a"), func() (output.Record, error) {
					asked.Add(1)

					return output.Record{}, errors.New("a waiter asked")
				})
				results <- sharedResult{record: record, duplicate: duplicate, err: err}
			}()
		}

		close(release)
		waiting.Wait()
		close(results)

		if err := <-ownerDone; err != nil {
			t.Errorf("the owner got %v", err)
		}

		if got := asked.Load(); got != 1 {
			t.Errorf("ask ran %d times, want 1", got)
		}

		var collected []sharedResult
		for result := range results {
			collected = append(collected, result)
		}

		checkShared(t, collected, answered, nil, dedupWorkers)
	})

	t.Run("should share a failed result with every caller, later ones included", func(t *testing.T) {
		t.Parallel()

		shared := newDedup(10, func() {})

		var asked atomic.Int32

		ask := func() (output.Record, error) {
			asked.Add(1)

			return output.Record{}, refused
		}

		results := together(t, dedupWorkers, func(context.Context) (output.Record, bool, error) {
			record, _, duplicate, err := shared.share(t.Context(), keyOf("a"), ask)

			return record, duplicate, err
		})

		_, _, duplicate, err := shared.share(t.Context(), keyOf("a"), ask)
		results = append(results, sharedResult{duplicate: duplicate, err: err})

		if got := asked.Load(); got != 1 {
			t.Errorf("ask ran %d times, want 1", got)
		}

		checkShared(t, results, output.Record{}, refused, dedupWorkers)
	})

	t.Run("should return the context's error to a waiter whose context ends", func(t *testing.T) {
		t.Parallel()

		shared := newDedup(10, func() {})
		release := make(chan struct{})
		started := make(chan struct{})

		defer close(release)

		go func() {
			_, _, _, _ = shared.share(t.Context(), keyOf("a"), func() (output.Record, error) {
				close(started)
				<-release

				return answered, nil
			})
		}()

		<-started

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		results := together(t, dedupWorkers, func(context.Context) (output.Record, bool, error) {
			record, _, duplicate, err := shared.share(ctx, keyOf("a"), func() (output.Record, error) {
				t.Error("a waiter asked")

				return output.Record{}, nil
			})

			return record, duplicate, err
		})

		for _, result := range results {
			if !errors.Is(result.err, context.Canceled) {
				t.Errorf("err = %v, want context.Canceled", result.err)
			}
		}
	})

	t.Run("should ask a new key every time past the limit while a held key still shares", func(t *testing.T) {
		t.Parallel()

		var notices atomic.Int32

		shared := newDedup(1, func() { notices.Add(1) })

		var askedOld, askedNew atomic.Int32

		if _, _, _, err := shared.share(t.Context(), keyOf("old"), func() (output.Record, error) {
			askedOld.Add(1)

			return answered, nil
		}); err != nil {
			t.Fatalf("share: %v", err)
		}

		results := together(t, dedupWorkers, func(context.Context) (output.Record, bool, error) {
			if _, call, duplicate, err := shared.share(t.Context(), keyOf("new"), func() (output.Record, error) {
				askedNew.Add(1)

				return answered, nil
			}); duplicate || call != nil || err != nil {
				t.Errorf("a new key past the limit was held: duplicate %t, call %v, err %v", duplicate, call, err)
			}

			record, _, duplicate, err := shared.share(t.Context(), keyOf("old"), func() (output.Record, error) {
				askedOld.Add(1)

				return output.Record{}, nil
			})

			return record, duplicate, err
		})

		if got := askedNew.Load(); got != dedupWorkers {
			t.Errorf("the new key was asked %d times, want %d", got, dedupWorkers)
		}

		if got := askedOld.Load(); got != 1 {
			t.Errorf("the held key was asked %d times, want 1", got)
		}

		if got := notices.Load(); got != 1 {
			t.Errorf("the notice ran %d times, want 1", got)
		}

		checkShared(t, results, answered, nil, dedupWorkers)
	})

	t.Run("should always ask through a nil group", func(t *testing.T) {
		t.Parallel()

		var shared *dedup

		var asked atomic.Int32

		results := together(t, dedupWorkers, func(context.Context) (output.Record, bool, error) {
			record, call, duplicate, err := shared.share(t.Context(), keyOf("a"), func() (output.Record, error) {
				asked.Add(1)

				return answered, nil
			})
			if call != nil {
				t.Error("a nil group returned a shared call")
			}

			return record, duplicate, err
		})

		if got := asked.Load(); got != dedupWorkers {
			t.Errorf("ask ran %d times, want %d", got, dedupWorkers)
		}

		checkShared(t, results, answered, nil, 0)
	})
}

func TestRequestKey(t *testing.T) {
	t.Parallel()

	questions := jev.Questions{{ID: "urgent", Question: jev.Noul{Instructions: "is this urgent"}}}
	other := jev.Questions{{ID: "urgent", Question: jev.Noul{Instructions: "is this rude"}}}
	state := json.RawMessage(`{"a":1,"b":2}`)

	base, err := requestKey(state, "m", questions, "")
	if err != nil {
		t.Fatalf("requestKey: %v", err)
	}

	tests := []struct {
		name      string
		sent      any
		model     string
		questions jev.Questions
		salt      string
		wantSame  bool
	}{
		{
			name: "should give one key to a state that differs only in whitespace",
			sent: json.RawMessage("{ \"a\": 1,\n \"b\": 2 }"), model: "m", questions: questions, wantSame: true,
		},
		{name: "should give another key to a different key order", sent: json.RawMessage(`{"b":2,"a":1}`), model: "m", questions: questions},
		{name: "should give another key to a different model", sent: state, model: "n", questions: questions},
		{name: "should give another key to a different question", sent: state, model: "m", questions: other},
		{name: "should give another key to a different salt", sent: state, model: "m", questions: questions, salt: "answers {}"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, keyErr := requestKey(tc.sent, tc.model, tc.questions, tc.salt)
			if keyErr != nil {
				t.Fatalf("requestKey: %v", keyErr)
			}

			if (got == base) != tc.wantSame {
				t.Errorf("same key = %t, want %t", got == base, tc.wantSame)
			}
		})
	}
}

func together(
	t *testing.T, workers int, call func(context.Context) (output.Record, bool, error),
) []sharedResult {
	t.Helper()

	var wg sync.WaitGroup

	results := make([]sharedResult, workers)
	start := make(chan struct{})

	for i := range workers {
		wg.Add(1)

		go func() {
			defer wg.Done()

			<-start

			record, duplicate, err := call(t.Context())
			results[i] = sharedResult{record: record, duplicate: duplicate, err: err}
		}()
	}

	close(start)
	wg.Wait()

	return results
}

func checkShared(t *testing.T, results []sharedResult, want output.Record, wantErr error, wantDuplicates int) {
	t.Helper()

	duplicates := 0

	for _, result := range results {
		if result.duplicate {
			duplicates++
		}

		if result.record.Model != want.Model {
			t.Errorf("record model = %q, want %q", result.record.Model, want.Model)
		}

		if !errors.Is(result.err, wantErr) {
			t.Errorf("err = %v, want %v", result.err, wantErr)
		}
	}

	if duplicates != wantDuplicates {
		t.Errorf("%d duplicates, want %d", duplicates, wantDuplicates)
	}
}

type sharedResult struct {
	record    output.Record
	duplicate bool
	err       error
}
