// Package interrupt lets a caller stop waiting on a read that has no deadline.
package interrupt

import "context"

// Wait runs read on its own goroutine and returns ctx.Err() once ctx ends first. A read under way
// cannot be stopped, so its goroutine outlives Wait until the read returns.
func Wait[T any](ctx context.Context, read func() (T, error)) (T, error) {
	if err := ctx.Err(); err != nil {
		var zero T

		return zero, err
	}

	done := make(chan result[T], 1)

	go func() {
		value, err := read()
		done <- result[T]{value: value, err: err}
	}()

	select {
	case got := <-done:
		return got.value, got.err
	case <-ctx.Done():
		var zero T

		return zero, ctx.Err()
	}
}

type result[T any] struct {
	value T
	err   error
}
