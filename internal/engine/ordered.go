package engine

import (
	"context"
	"errors"
	"io"
	"sync"
	"syscall"
)

func runOrdered[T any](ctx context.Context, cfg Config[T]) (Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan job[T])
	queue := make(chan chan outcome[T], fifoCap(cfg.Jobs))
	failed := make(chan error, 1)

	var workers sync.WaitGroup

	for range cfg.Jobs {
		workers.Add(1)

		go func() {
			defer workers.Done()

			for item := range jobs {
				line, err := cfg.Evaluate(ctx, item.record)

				// The send below is on a channel of capacity one and is therefore always ready.
				// A select against ctx.Done would pick between them at random, so a cancelled
				// worker would still deliver its outcome about half the time and the flush would
				// write a spurious cancellation record. Checking first is what makes the abort
				// deterministic.
				if ctx.Err() != nil {
					return
				}

				item.out <- outcome[T]{line: line, err: err}
			}
		}()
	}

	go dispatch(ctx, cfg, jobs, queue, failed)

	// The writer runs here rather than in a goroutine, since it owns the output and this call owns
	// the result.
	result := consume(ctx, cancel, cfg, queue)

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

func fifoCap(jobs int) int {
	if jobs < 2 {
		// A job count of one gives an unbuffered channel and a fully serial run.
		return 0
	}

	// One less than the job count, because the writer is always holding one popped channel. That
	// caps outstanding records at the job count, which is what makes the spec's bound of one fewer
	// completed records buffered true.
	return jobs - 1
}

func dispatch[T any](
	ctx context.Context,
	cfg Config[T],
	jobs chan<- job[T],
	queue chan<- chan outcome[T],
	failed chan<- error,
) {
	defer close(jobs)
	defer close(queue)

	for {
		rec, ok, err := cfg.Source.Next()
		if err != nil {
			failed <- err

			return
		}

		if !ok {
			return
		}

		out := make(chan outcome[T], 1)

		// The channel joins the queue before the job is handed out, so the writer always sees
		// positions in input order regardless of which worker finishes first.
		if ctx.Err() != nil {
			return
		}

		select {
		case queue <- out:
		case <-ctx.Done():
			return
		}

		// Checked again rather than relying on the select alone. Both are needed: the check keeps
		// a cancelled run from handing out one more job, and the select keeps an unbuffered send
		// from deadlocking when no worker is left to receive.
		if ctx.Err() != nil {
			return
		}

		select {
		case jobs <- job[T]{record: rec, out: out}:
		case <-ctx.Done():
			return
		}
	}
}

func consume[T any](
	ctx context.Context,
	cancel context.CancelFunc,
	cfg Config[T],
	queue <-chan chan outcome[T],
) Result {
	var result Result

	for out := range queue {
		select {
		case got := <-out:
			if !emit(cfg, got, &result) {
				// Cancelling here is not optional. Without it the dispatcher stays blocked on a
				// queue nobody drains, the workers stay blocked on jobs, and runOrdered deadlocks
				// on workers.Wait before its own deferred cancel can ever run.
				cancel()

				return result
			}

			if aborts(cfg, got.err) {
				result.Aborted = true
				result.Cause = got.err

				// Cancelling before the flush is what keeps an authentication failure from being
				// reported once per line. A worker that finished before the cancel has already
				// sent and the flush writes it. A worker still running returns without sending,
				// and the flush stops at the first of those, so how many records follow the
				// aborting one is not fixed.
				cancel()
				flush(cfg, queue, &result)

				return result
			}
		case <-ctx.Done():
			result.Aborted = true
			result.Cause = ctx.Err()

			// The channel being held is the head of the prefix. Both select cases are ready when
			// a signal lands just as this record completes, and Go picks between them at random,
			// so dropping the head here would let flush write the record behind it and produce
			// exactly the hole the flush exists to prevent. If the head is not ready, nothing
			// behind it may be written either.
			select {
			case got := <-out:
				if emit(cfg, got, &result) {
					flush(cfg, queue, &result)
				}
			default:
			}

			return result
		}
	}

	return result
}

func flush[T any](cfg Config[T], queue <-chan chan outcome[T], result *Result) {
	for {
		select {
		case out, ok := <-queue:
			if !ok {
				return
			}

			select {
			case got := <-out:
				if !emit(cfg, got, result) {
					return
				}
			default:
				// Stopping at the first record still in flight is what writes the longest
				// completed prefix and keeps the output a prefix of the input rather than a
				// prefix with a hole in it.
				return
			}
		default:
			return
		}
	}
}

func emit[T any](cfg Config[T], got outcome[T], result *Result) bool {
	result.Records++

	if got.err != nil {
		result.Failed++
	}

	err := cfg.Write(got.line)
	if err == nil {
		return true
	}

	if broken(err) {
		// The consumer stopped reading, which is its right. Nothing is reported and the run
		// succeeded, so false here means stop rather than fail.
		result.Broken = true

		return false
	}

	// Anything else is jev's own failure, such as a record that could not be merged into its input
	// line. Ending the run silently with exit 0 would hide it.
	result.Fatal = err

	return false
}

func broken(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe)
}

func aborts[T any](cfg Config[T], err error) bool {
	if err == nil {
		return false
	}

	if cfg.Abort != nil && cfg.Abort(err) {
		return true
	}

	return cfg.StopOnError
}
