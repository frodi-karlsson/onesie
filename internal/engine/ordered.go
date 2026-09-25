package engine

import (
	"context"
	"sync"
)

func runOrdered[R, T any](ctx context.Context, cfg Config[R, T]) (Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan job[R, T])
	queue := make(chan chan outcome[T], fifoCap(cfg.Jobs))
	failed := make(chan error, 1)

	var workers sync.WaitGroup

	for range cfg.Jobs {
		workers.Add(1)

		go func() {
			defer workers.Done()

			for item := range jobs {
				line, err := cfg.Evaluate(ctx, item.record)

				// The send below is on a channel of capacity one and is always ready. A select
				// against ctx.Done would pick at random and deliver a cancelled outcome about half
				// the time, so checking first makes the abort deterministic.
				if ctx.Err() != nil {
					return
				}

				item.out <- outcome[T]{line: line, err: err}
			}
		}()
	}

	go dispatch(ctx, interruptible(ctx, cfg.Source), jobs, queue, failed)

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
	// caps outstanding records at the job count, so at most one fewer completed records wait in the
	// buffer.
	return jobs - 1
}

func dispatch[R, T any](
	ctx context.Context,
	next func() (R, bool, error),
	jobs chan<- job[R, T],
	queue chan<- chan outcome[T],
	failed chan<- error,
) {
	defer close(jobs)
	defer close(queue)

	for {
		rec, ok, err := next()
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
		case jobs <- job[R, T]{record: rec, out: out}:
		case <-ctx.Done():
			return
		}
	}
}

func consume[R, T any](
	ctx context.Context,
	cancel context.CancelFunc,
	cfg Config[R, T],
	queue <-chan chan outcome[T],
) Result {
	var result Result

	for {
		var out chan outcome[T]

		select {
		case head, ok := <-queue:
			if !ok {
				// A closed queue is ambiguous. The dispatcher closes it at the end of the input and
				// also when the run is interrupted, and only the context tells the two apart.
				if ctx.Err() != nil {
					interrupted(ctx, &result)
				}

				return result
			}

			out = head
		case <-ctx.Done():
			interrupted(ctx, &result)
			flush(cfg, queue, &result)

			return result
		}

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

				// Cancelling before the flush keeps an authentication failure from being reported
				// once per line. The flush writes the workers that finished before the cancel and
				// stops at the first still running, so how many records follow the aborting one is
				// not fixed.
				cancel()
				flush(cfg, queue, &result)

				return result
			}

			if stops(cfg, got.line) {
				// No Cause and no Aborted, because the record that ended the run succeeded. The
				// caller reads the outcome off the lines it was handed, and the flush leaves the
				// same completed prefix an abort does.
				cancel()
				flush(cfg, queue, &result)

				return result
			}
		case <-ctx.Done():
			interrupted(ctx, &result)

			// The held channel is the head of the prefix. Both cases are ready when a signal lands
			// as this record completes, and Go picks at random, so dropping the head here would let
			// flush write the record behind it and leave a hole.
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
}

func interrupted(ctx context.Context, result *Result) {
	result.Aborted = true
	result.Cause = ctx.Err()
}

func flush[R, T any](cfg Config[R, T], queue <-chan chan outcome[T], result *Result) {
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
				// Stopping at the first record still in flight writes the longest completed prefix
				// without a hole.
				return
			}
		default:
			return
		}
	}
}

func emit[R, T any](cfg Config[R, T], got outcome[T], result *Result) bool {
	result.Records++

	if got.err != nil {
		result.Failed++
	}

	err := cfg.Write(got.line)
	if err == nil {
		return true
	}

	if BrokenPipe(err) {
		// The consumer stopped reading, which is its right. Nothing is reported and the run
		// succeeded, so false here means stop rather than fail.
		result.Broken = true

		return false
	}

	// Anything else is onesie's own failure, such as a record that could not be merged into its input
	// line. Ending the run silently with exit 0 would hide it.
	result.Fatal = err

	return false
}

func aborts[R, T any](cfg Config[R, T], err error) bool {
	if err == nil {
		return false
	}

	if cfg.Abort != nil && cfg.Abort(err) {
		return true
	}

	return cfg.StopOnError
}

func stops[R, T any](cfg Config[R, T], line T) bool {
	return cfg.Stop != nil && cfg.Stop(line)
}
