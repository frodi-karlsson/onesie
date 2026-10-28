package cli

import (
	"context"
	"sync"
)

// Run runs the tree once and returns its failure apart from the failures that followed it,
// for a caller that inspects the errors instead of exiting with them.
func (r *Root) Run(ctx context.Context) Outcome {
	failure := r.Command.ExecuteContext(ctx)

	return Outcome{Failure: failure, Later: r.laterFailures.drain()}
}

// Outcome is one run's failure and the failures that followed it. Later never decides the
// exit code while Failure is set, so errors.Is on Failure alone tells what the run did.
type Outcome struct {
	Failure error
	Later   []error
}

func (o Outcome) report() []string {
	var lines []string

	for _, err := range append([]error{o.Failure}, o.Later...) {
		if err != nil && worthReporting(err) {
			lines = append(lines, text(err))
		}
	}

	return lines
}

type failureLog struct {
	mu       sync.Mutex
	failures []error
}

func (l *failureLog) keepFirst(failure, later error) error {
	if later == nil {
		return failure
	}

	if failure == nil {
		return later
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.failures = append(l.failures, later)

	return failure
}

func (l *failureLog) drain() []error {
	l.mu.Lock()
	defer l.mu.Unlock()

	drained := l.failures
	l.failures = nil

	return drained
}
