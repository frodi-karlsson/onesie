package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/output"
)

const dedupWorkers = 32

func TestDedup_Share(t *testing.T) {
	t.Parallel()

	answered := output.Record{Model: "m"}
	refused := &onesie.APIError{Status: 503}
	keyOf := func(name string) [sha256.Size]byte { return sha256.Sum256([]byte(name)) }

	t.Run("should run ask once for one key and hand every caller the record", func(t *testing.T) {
		t.Parallel()

		shared := newDedup(10, func() error {
			t.Error("the notice ran below the limit")

			return nil
		})

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

		synctest.Test(t, func(t *testing.T) {
			shared := newDedup(10, func() error { return nil })
			release := make(chan struct{})

			var asked atomic.Int32

			ownerDone := make(chan error, 1)

			go func() {
				_, _, _, err := shared.share(t.Context(), keyOf("a"), func() (output.Record, error) {
					asked.Add(1)
					<-release

					return answered, nil
				})
				ownerDone <- err
			}()

			synctest.Wait()

			results := make(chan sharedResult, dedupWorkers)

			for range dedupWorkers {
				go func() {
					record, _, duplicate, err := shared.share(t.Context(), keyOf("a"), func() (output.Record, error) {
						asked.Add(1)

						return output.Record{}, errors.New("a waiter asked")
					})
					results <- sharedResult{record: record, duplicate: duplicate, err: err}
				}()
			}

			// Every waiter is blocked on the call in flight before the owner is released.
			synctest.Wait()

			if got := len(results); got != 0 {
				t.Errorf("%d callers returned before the owner did, want 0", got)
			}

			close(release)

			if err := <-ownerDone; err != nil {
				t.Errorf("the owner got %v", err)
			}

			collected := make([]sharedResult, 0, dedupWorkers)
			for range dedupWorkers {
				collected = append(collected, <-results)
			}

			if got := asked.Load(); got != 1 {
				t.Errorf("ask ran %d times, want 1", got)
			}

			checkShared(t, collected, answered, nil, dedupWorkers)
		})
	})

	t.Run("should share a failed result with every caller, later ones included", func(t *testing.T) {
		t.Parallel()

		shared := newDedup(10, func() error { return nil })

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

		shared := newDedup(10, func() error { return nil })
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

		shared := newDedup(1, func() error {
			notices.Add(1)

			return nil
		})

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

	t.Run("should run the notice with the group unlocked", func(t *testing.T) {
		t.Parallel()

		var shared *dedup

		var noticed atomic.Bool

		shared = newDedup(1, func() error {
			noticed.Store(true)

			if !shared.mu.TryLock() {
				t.Error("the notice ran while the group was locked")

				return nil
			}

			shared.mu.Unlock()

			return nil
		})

		for _, name := range []string{"held", "new"} {
			if _, _, _, err := shared.share(t.Context(), keyOf(name), func() (output.Record, error) {
				return answered, nil
			}); err != nil {
				t.Fatalf("share: %v", err)
			}
		}

		if !noticed.Load() {
			t.Error("the notice never ran")
		}
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

func TestDedup_NoticeError(t *testing.T) {
	t.Parallel()

	errPrint := errors.New("stderr is closed")
	keyOf := func(name string) [sha256.Size]byte { return sha256.Sum256([]byte(name)) }

	tests := []struct {
		name   string
		shared *dedup
		keys   []string
		want   error
	}{
		{name: "should hold nothing for a run without dedup"},
		{
			name:   "should hold nothing below the limit",
			shared: newDedup(2, func() error { return errPrint }), keys: []string{"a", "b"},
		},
		{
			name:   "should hold the notice's print error past the limit",
			shared: newDedup(1, func() error { return errPrint }), keys: []string{"a", "b"}, want: errPrint,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, name := range tc.keys {
				if _, _, _, err := tc.shared.share(t.Context(), keyOf(name), func() (output.Record, error) {
					return output.Record{}, nil
				}); err != nil {
					t.Fatalf("share: %v", err)
				}
			}

			if got := tc.shared.noticeError(); !errors.Is(got, tc.want) {
				t.Errorf("noticeError = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAskOnce(t *testing.T) {
	t.Parallel()

	questions := onesie.Questions{{ID: "urgent", Question: onesie.Noul{Instructions: "is this urgent"}}}
	answered := output.Record{Model: "m"}

	tests := []struct {
		name        string
		group       bool
		sents       []any
		salts       []string
		wantAsked   int32
		wantDedups  int
		wantRecords int
		wantCalls   bool
	}{
		{
			name: "should ask a repeated request once and count the duplicate", group: true,
			sents: []any{"x", "x"}, salts: []string{"", ""}, wantAsked: 1, wantDedups: 1, wantRecords: 1, wantCalls: true,
		},
		{
			name: "should ask each salt apart", group: true,
			sents: []any{"x", "x"}, salts: []string{"a", "b"}, wantAsked: 2, wantCalls: true,
		},
		{
			name:  "should ask every record through no group",
			sents: []any{"x", "x"}, salts: []string{"", ""}, wantAsked: 2,
		},
		{
			name: "should ask a state that cannot be encoded alone", group: true,
			sents: []any{math.Inf(1), math.Inf(1)}, salts: []string{"", ""}, wantAsked: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var shared *dedup
			if tc.group {
				shared = newDedup(10, func() error { return nil })
			}

			var (
				stats collector
				asked atomic.Int32
			)

			for i, sent := range tc.sents {
				record, call, err := askOnce(t.Context(), shared, &stats, onesie.Request{State: sent, Model: "m", Questions: questions}, tc.salts[i],
					func() (output.Record, error) {
						asked.Add(1)

						return answered, nil
					})
				if err != nil || record.Model != "m" {
					t.Errorf("record %d = %+v, %v, want the answer", i, record, err)
				}

				if (call != nil) != tc.wantCalls {
					t.Errorf("record %d carried call %v, want one %t", i, call, tc.wantCalls)
				}
			}

			got := stats.snapshot(time.Second)

			if asked.Load() != tc.wantAsked || got.Dedups != tc.wantDedups || got.Records != tc.wantRecords {
				t.Errorf("asked, dedups, records = %d, %d, %d, want %d, %d, %d",
					asked.Load(), got.Dedups, got.Records, tc.wantAsked, tc.wantDedups, tc.wantRecords)
			}
		})
	}
}

func TestBillOnce(t *testing.T) {
	t.Parallel()

	spent := func() *onesie.Usage { return &onesie.Usage{InputTokens: 5, OutputTokens: 2} }

	tests := []struct {
		name      string
		call      *sharedCall
		usage     *onesie.Usage
		want      *onesie.Usage
		wantBills bool
	}{
		{name: "should leave a line of no group as it is", usage: spent(), want: spent()},
		{
			name: "should keep the tokens on the first line of a group and mark it billed",
			call: &sharedCall{}, usage: spent(), want: spent(), wantBills: true,
		},
		{
			name: "should give a later line of a group zero tokens",
			call: &sharedCall{billed: true}, usage: spent(), want: &onesie.Usage{}, wantBills: true,
		},
		{
			name: "should leave a later error line that carried no usage with none",
			call: &sharedCall{billed: true}, wantBills: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := line{record: output.Record{Usage: tc.usage}, call: tc.call}

			billOnce(&l)

			if (l.record.Usage == nil) != (tc.want == nil) || (tc.want != nil && *l.record.Usage != *tc.want) {
				t.Errorf("usage = %v, want %v", l.record.Usage, tc.want)
			}

			if tc.call != nil && tc.call.billed != tc.wantBills {
				t.Errorf("billed = %t, want %t", tc.call.billed, tc.wantBills)
			}
		})
	}
}

func TestRequestKey(t *testing.T) {
	t.Parallel()

	questions := onesie.Questions{{ID: "urgent", Question: onesie.Noul{Instructions: "is this urgent"}}}
	other := onesie.Questions{{ID: "urgent", Question: onesie.Noul{Instructions: "is this rude"}}}
	state := json.RawMessage(`{"a":1,"b":2}`)

	base, err := requestKey(onesie.Request{State: state, Model: "m", Questions: questions}, "")
	if err != nil {
		t.Fatalf("requestKey: %v", err)
	}

	tests := []struct {
		name      string
		sent      any
		model     string
		questions onesie.Questions
		salt      string
		long      bool
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
		{name: "should give another key to long context", sent: state, model: "m", questions: questions, long: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, keyErr := requestKey(onesie.Request{
				State: tc.sent, Model: tc.model, Questions: tc.questions, LongContext: tc.long,
			}, tc.salt)
			if keyErr != nil {
				t.Fatalf("requestKey: %v", keyErr)
			}

			if (got == base) != tc.wantSame {
				t.Errorf("same key = %t, want %t", got == base, tc.wantSame)
			}
		})
	}

	t.Run("should change when an image changes behind the same reference", func(t *testing.T) {
		t.Parallel()

		key := func(data byte) [32]byte {
			got, keyErr := requestKey(onesie.Request{
				State: map[string]any{"photo": "ref"}, Model: "m", Questions: questions,
				Assets: onesie.Assets{"ref": {Format: "png", Data: []byte{data}}},
			}, "")
			if keyErr != nil {
				t.Fatalf("requestKey: %v", keyErr)
			}

			return got
		}

		first := key(1)

		if first == key(2) {
			t.Error("keys are equal, want an image's bytes in the key")
		}

		if first != key(1) {
			t.Error("keys differ for one request")
		}
	})
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
