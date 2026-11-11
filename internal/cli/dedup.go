package cli

import (
	"context"
	"crypto/sha256"
	"sync"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func askOnce(
	ctx context.Context,
	shared *dedup,
	stats *collector,
	key [sha256.Size]byte,
	ask func() (output.Record, error),
) (output.Record, *sharedCall, error) {
	record, call, duplicate, err := shared.share(ctx, key, ask)
	if duplicate {
		stats.deduplicated(err)
	}

	return record, call, err
}

func newDedup(limit int, notice func() error) *dedup {
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
		announce := !d.full
		d.full = true
		d.mu.Unlock()

		// Outside the lock, so a slow stderr holds up no other record.
		if announce {
			d.noticed(d.notice())
		}

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

func (d *dedup) noticed(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.noticeErr = err
}

func (d *dedup) noticeError() error {
	if d == nil {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	return d.noticeErr
}

type dedup struct {
	mu        sync.Mutex
	calls     map[[sha256.Size]byte]*sharedCall
	limit     int
	full      bool
	notice    func() error
	noticeErr error
}

type sharedCall struct {
	done   chan struct{}
	record output.Record // Its answers are every duplicate's answers too, so nothing may change them.
	err    error
	billed bool // Only the stream's single writer touches it, so it needs no lock.
}

func newPrepared(body *onesie.PreparedBody) prepared {
	hash := sha256.New()
	body.WriteTo(hash) //nolint:errcheck // A hash.Hash never returns an error from Write.

	var sum [sha256.Size]byte

	hash.Sum(sum[:0])

	return prepared{body: body, sum: sum}
}

func (p prepared) key(salt string) [sha256.Size]byte {
	hash := sha256.New()
	hash.Write(p.sum[:])
	hash.Write([]byte(salt))

	var key [sha256.Size]byte

	hash.Sum(key[:0])

	return key
}

type prepared struct {
	body *onesie.PreparedBody
	sum  [sha256.Size]byte
}
