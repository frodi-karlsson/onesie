package runner

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/cache"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestCachedAnswerer(t *testing.T) {
	t.Parallel()

	request := onesie.Request{
		State: json.RawMessage(`"site down"`), Questions: plan.WireAll([]plan.Question{{ID: "u", Shape: plan.Noul}}),
	}
	key := RecordKey{Position: 1, Line: 1}

	t.Run("should ask again past a stored answer that no longer fits its question", func(t *testing.T) {
		t.Parallel()

		client, err := onesie.New(onesie.WithProvider(onesie.TypeSafe()), onesie.WithAPIKey("SECRET-K"),
			onesie.WithBaseURL("https://answers.invalid"))
		if err != nil {
			t.Fatal(err)
		}

		dir := filepath.Join(t.TempDir(), "cache")
		open := func() (*cache.Store, error) {
			return cache.Open(dir, cache.Options{Now: func() time.Time { return time.Unix(1_800_000_000, 0) }})
		}
		next := &countingAnswerer{result: &onesie.Result{
			Model: "m", Answers: map[string]onesie.Answer{"u": &onesie.NoulAnswer{Noul: 0.4}},
		}}
		model := client.DefaultModel()
		warn := func(err error) { t.Errorf("unexpected cache warning: %v", err) }

		cached := NewCachedAnswerer(next, open, client, model, warn)

		store, err := cached.open()
		if err != nil {
			t.Fatal(err)
		}

		id, err := requestKey(onesie.Request{
			State: request.State, Model: cached.model, Questions: request.Questions,
		}, cached.origin)
		if err != nil {
			t.Fatal(err)
		}

		outOfRange := &onesie.Result{Model: "m", Answers: map[string]onesie.Answer{"u": &onesie.NoulAnswer{Noul: 1.5}}}
		if putErr := cached.put(store, cache.Key(id), outOfRange); putErr != nil {
			t.Fatal(putErr)
		}

		got, err := cached.Answer(t.Context(), key, request)
		if err != nil || got.Cached {
			t.Fatalf("answer = %+v, %v, want a fresh answer past the refused entry", got, err)
		}

		if requests := next.asked.Load(); requests != 1 {
			t.Errorf("%d requests, want 1", requests)
		}
	})
}

func (a *countingAnswerer) Answer(context.Context, RecordKey, onesie.Request) (Reply, error) {
	a.asked.Add(1)

	return Reply{Result: a.result}, nil
}

func (*countingAnswerer) Salt(RecordKey) (string, bool) {
	return "", true
}

type countingAnswerer struct {
	result *onesie.Result
	asked  atomic.Int32
}
