// Package engine runs one evaluation per input record, bounded by concurrency and ordered by input
// position. It knows nothing about HTTP, answers or output formats.
package engine

import (
	"context"

	"github.com/frodi-karlsson/jev-cli/internal/input"
)

// Run evaluates every record the source yields and writes one line per record. It returns when the
// input is exhausted or the run aborts.
func Run[T any](ctx context.Context, cfg Config[T]) (Result, error) {
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
type Config[T any] struct {
	Source   Source
	Evaluate func(context.Context, input.Record) (T, error)
	Write    func(T) error

	Jobs        int
	Unordered   bool
	StopOnError bool

	// Abort reports whether a failure ends the whole run rather than the one record. It is how an
	// authentication failure stops a stream that would otherwise fail the same way on every line.
	Abort func(error) bool
}

// Source yields records in input order. The second result is false at end of input.
type Source interface {
	Next() (input.Record, bool, error)
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
	// Fatal is a write failure of jev's own, as distinct from a consumer that stopped reading.
	Fatal error
}

type job[T any] struct {
	record input.Record
	out    chan outcome[T]
}

type outcome[T any] struct {
	line T
	err  error
}
