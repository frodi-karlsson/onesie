package engine

import "context"

func interruptible[R any](ctx context.Context, source Source[R]) func() (R, bool, error) {
	want := make(chan struct{})
	got := make(chan pulled[R])

	// Read on its own goroutine so the dispatcher can give up on a read with no deadline, such as a
	// terminal or a FIFO. A read under way cannot be interrupted, so this goroutine outlives Run
	// until that read returns. The leak is the price of an interrupt ending the run at all.
	go func() {
		for {
			// Reading only when asked keeps the read ahead to the one record the dispatcher held
			// when it called the source itself, so the bound on buffered records is unchanged.
			select {
			case <-want:
			case <-ctx.Done():
				return
			}

			rec, ok, err := source.Next()

			select {
			case got <- pulled[R]{rec: rec, ok: ok, err: err}:
			case <-ctx.Done():
				return
			}

			if !ok || err != nil {
				return
			}
		}
	}()

	return func() (R, bool, error) {
		var zero R

		select {
		case want <- struct{}{}:
		case <-ctx.Done():
			return zero, false, nil
		}

		select {
		case p := <-got:
			return p.rec, p.ok, p.err
		case <-ctx.Done():
			return zero, false, nil
		}
	}
}

type pulled[R any] struct {
	rec R
	ok  bool
	err error
}
