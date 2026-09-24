// Package engine runs one evaluation per record the source yields, bounded by concurrency and
// ordered by input position. It knows nothing about HTTP, answers or output formats.
package engine

import "context"

// Run evaluates every record the source yields and writes one line per record. It returns when the
// input is exhausted or the run aborts.
func Run[R, T any](ctx context.Context, cfg Config[R, T]) (Result, error) {
	if cfg.Jobs < 1 {
		cfg.Jobs = 1
	}

	if cfg.Unordered {
		return runUnordered(ctx, cfg)
	}

	return runOrdered(ctx, cfg)
}

// Config is everything Run needs. Every dependency is injected, so the engine is tested with no
// network and no files.
type Config[R, T any] struct {
	Source   Source[R]
	Evaluate func(context.Context, R) (T, error)
	Write    func(T) error

	Jobs        int
	Unordered   bool
	StopOnError bool

	// Abort reports whether a failure ends the whole run rather than the one record. It is how an
	// authentication failure stops a stream that would otherwise fail the same way on every line.
	Abort func(error) bool

	// Stop reports whether a written line ends the run, for an outcome that is not a failure. It
	// is how --stop-on-assert ends a stream at the first false assertion, which is a judgment
	// about a complete record rather than a reason to count it failed.
	Stop func(T) bool
}

// Source yields records in input order. The second result is false at end of input.
type Source[R any] interface {
	Next() (R, bool, error)
}

// Result reports what happened, so the caller can choose an exit code.
type Result struct {
	Records int
	Failed  int
	Aborted bool
	// Cause is the failure that aborted the run, nil when it finished.
	Cause error
	// Broken is true when the output pipe closed, which is not a failure.
	Broken bool
	// Fatal is a write failure of onesie's own, as distinct from a consumer that stopped reading.
	Fatal error
}

type job[R, T any] struct {
	record R
	out    chan outcome[T]
}

type outcome[T any] struct {
	line T
	err  error
}
