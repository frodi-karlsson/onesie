package cli

import (
	"context"
	"sync"
)

// Run runs the tree once and returns its failure apart from the cleanup failures that followed it,
// for a caller that inspects the errors instead of exiting with them.
func (r *Root) Run(ctx context.Context) Outcome {
	failure := r.Command.ExecuteContext(ctx)

	return Outcome{Failure: failure, Cleanups: r.cleanups.drain()}
}

// Outcome is one run's failure and the cleanup failures that followed it. Cleanups never decide the
// exit code while Failure is set, so errors.Is on Failure alone tells what the run did.
type Outcome struct {
	Failure  error
	Cleanups []error
}

func (o Outcome) report() []string {
	var lines []string

	for _, err := range append([]error{o.Failure}, o.Cleanups...) {
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
