package cli

import (
	"context"
	"sync"
)

func (r *Root) run(ctx context.Context) outcome {
	failure := r.Command.ExecuteContext(ctx)

	return outcome{failure: failure, cleanups: r.cleanups.drain()}
}

type outcome struct {
	failure  error
	cleanups []error
}

func (o outcome) report() []string {
	var lines []string

	for _, err := range append([]error{o.failure}, o.cleanups...) {
		if err != nil && worthReporting(err) {
			lines = append(lines, text(err))
		}
	}

	return lines
}

type cleanupLog struct {
	mu       sync.Mutex
	failures []error
}

func (l *cleanupLog) after(failure, cleanup error) error {
	if cleanup == nil {
		return failure
	}

	if failure == nil {
		return cleanup
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.failures = append(l.failures, cleanup)

	return failure
}

func (l *cleanupLog) drain() []error {
	l.mu.Lock()
	defer l.mu.Unlock()

	drained := l.failures
	l.failures = nil

	return drained
}
