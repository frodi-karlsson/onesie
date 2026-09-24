package engine

import (
	"context"
	"sync"
)

func runUnordered[R, T any](ctx context.Context, cfg Config[R, T]) (Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan job[R, T])
	done := make(chan outcome[T], cfg.Jobs)
	failed := make(chan error, 1)

	var workers sync.WaitGroup

	for range cfg.Jobs {
		workers.Add(1)

		go func() {
			defer workers.Done()

			for item := range jobs {
				line, err := cfg.Evaluate(ctx, item.record)

				// Both guards are needed and they solve different problems. The check makes the
				// abort deterministic, since the completion channel is buffered and a select
				// alone would deliver at random. The select prevents a goroutine leak: one fast
				// worker can fill the channel on its own while the drain is busy, and a worker
				// already blocked on a full channel when the cancel lands would never return.
				if ctx.Err() != nil {
					return
				}

				select {
				case done <- outcome[T]{line: line, err: err}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	next := interruptible(ctx, cfg.Source)

	go func() {
		defer close(jobs)

		for {
			rec, ok, err := next()
			if err != nil {
				failed <- err

				return
			}

			if !ok {
				return
			}

			if ctx.Err() != nil {
				return
			}

			select {
			case jobs <- job[R, T]{record: rec}:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		workers.Wait()
		close(done)
	}()

	result := drain(ctx, cancel, cfg, done)

	// Waited on inline rather than left to the closer goroutine above, so Run never returns while
	// a worker it started is still live. Waiting twice on one WaitGroup is safe, and after a clean
	// run this returns at once because the workers have already finished.
	workers.Wait()

	if result.Fatal != nil {
		return result, result.Fatal
	}

	select {
	case err := <-failed:
		return result, err
	default:
		return result, nil
	}
}

func drain[R, T any](
	ctx context.Context,
	cancel context.CancelFunc,
	cfg Config[R, T],
	done <-chan outcome[T],
) Result {
	var result Result

	for {
		select {
		case got, ok := <-done:
			if !ok {
				// The workers finish and close the channel on an interrupt as well as at the end
				// of the input, and only the context tells the two apart.
				if ctx.Err() != nil {
					interrupted(ctx, &result)
				}

				return result
			}

			if !emit(cfg, got, &result) {
				cancel()

				return result
			}

			if aborts(cfg, got.err) {
				result.Aborted = true
				result.Cause = got.err

				// Unordered mode has no prefix flush, and that is the design rather than an
				// omission. It has already given up the ordering guarantee the flush exists to
				// preserve, so on an abort there is no meaningful prefix left to complete.
				cancel()

				return result
			}

			if stops(cfg, got.line) {
				cancel()

				return result
			}
		case <-ctx.Done():
			interrupted(ctx, &result)

			return result
		}
	}
}
