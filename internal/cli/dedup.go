package cli

import (
	"context"
	"crypto/sha256"
	"sync"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func askOnce(
	ctx context.Context,
	shared *dedup,
	stats *collector,
	sent any,
	model string,
	questions jev.Questions,
	salt string,
	ask func() (output.Record, error),
) (output.Record, *sharedCall, error) {
	key, err := requestKey(sent, model, questions, salt)
	if err != nil {
		// A state that cannot be encoded fails its own request the same way, so it is asked alone.
		record, askErr := ask()

		return record, nil, askErr
	}

	record, call, duplicate, err := shared.share(ctx, key, ask)
	if duplicate {
		stats.deduplicated(err)
	}

	return record, call, err
}

func newDedup(limit int, notice func()) *dedup {
	return &dedup{limit: limit, notice: notice, calls: make(map[[sha256.Size]byte]*sharedCall)}
}

func (d *dedup) share(
	ctx context.Context,
	key [sha256.Size]byte,
	ask func() (output.Record, error),
) (output.Record, *sharedCall, bool, error) {
	if d == nil {
		record, err := ask()

		return record, nil, false, err
	}

	d.mu.Lock()

	if call, held := d.calls[key]; held {
		d.mu.Unlock()

		select {
		case <-call.done:
			return call.record, call, true, call.err
		case <-ctx.Done():
			return output.Record{}, nil, true, ctx.Err()
		}
	}

	if len(d.calls) >= d.limit {
		if !d.full {
			d.full = true
			d.notice()
		}

		d.mu.Unlock()

		record, err := ask()

		return record, nil, false, err
	}

	call := &sharedCall{done: make(chan struct{})}
	d.calls[key] = call
	d.mu.Unlock()

	defer close(call.done)

	call.record, call.err = ask()

	return call.record, call, false, call.err
}

type dedup struct {
	mu     sync.Mutex
	calls  map[[sha256.Size]byte]*sharedCall
	limit  int
	full   bool
	notice func()
}

type sharedCall struct {
	done   chan struct{}
	record output.Record // Its answers are every duplicate's answers too, so nothing may change them.
	err    error
	billed bool // Only the stream's single writer touches it, so it needs no lock.
}

func requestKey(sent any, model string, questions jev.Questions, salt string) ([sha256.Size]byte, error) {
	body, err := jev.MarshalBody(jev.Request{State: sent, Model: model, Questions: questions})
	if err != nil {
		return [sha256.Size]byte{}, err
	}

	hash := sha256.New()
	hash.Write(body)
	hash.Write([]byte(salt))

	var key [sha256.Size]byte

	hash.Sum(key[:0])

	return key, nil
}
