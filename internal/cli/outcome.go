package cli

import (
	"context"
	"sync"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/output"
)

// Root is a command tree with the log of the failures that follow a run's failure, which cobra's one
// returned error has no room for. Command is the tree itself, for a caller to configure.
type Root struct {
	Command *cobra.Command

	laterFailures *failureLog
}

// Execute runs the tree and returns the process exit code, so main never has to classify an error
// itself. The failures that followed the run's failure print after it.
func (r *Root) Execute(ctx context.Context) int {
	ended := r.Run(ctx)

	for _, line := range ended.Report() {
		//nolint:errcheck // A stderr that cannot take the message leaves the exit code to name the failure.
		_ = output.Notice(r.Command.ErrOrStderr(), line)
	}

	return Classify(ended.Failure)
}

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

// Report returns the messages Execute prints, leaving out a quiet failure such as an interrupt or a
// broken pipe, so a caller that inspects the outcome reports by the same rule.
func (o Outcome) Report() []string {
	var lines []string

	for _, err := range append([]error{o.Failure}, o.Later...) {
		if err != nil && worthReporting(err) {
			// Stripped, since a message can quote the server or the input to a terminal.
			lines = append(lines, output.Printable(text(err)))
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
