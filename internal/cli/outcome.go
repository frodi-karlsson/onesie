package cli

import (
	"context"
	"sync"

	"github.com/spf13/cobra"
)

func execute(ctx context.Context, root *cobra.Command) outcome {
	cleanups := &cleanupLog{}

	// Cobra hands back one error, so the cleanup failures that followed it travel in the context
	// every command reads.
	failure := root.ExecuteContext(withCleanups(ctx, cleanups))

	return outcome{failure: failure, cleanups: cleanups.recorded()}
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

func withCleanups(ctx context.Context, cleanups *cleanupLog) context.Context {
	return context.WithValue(ctx, cleanupsKey{}, cleanups)
}

func cleanupsIn(ctx context.Context) *cleanupLog {
	if cleanups, found := ctx.Value(cleanupsKey{}).(*cleanupLog); found {
		return cleanups
	}

	return nil
}

type cleanupsKey struct{}

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

	// Without a log the run was not started through Execute, and its caller reads the failure alone.
	if l == nil {
		return failure
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.failures = append(l.failures, cleanup)

	return failure
}

func (l *cleanupLog) recorded() []error {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]error(nil), l.failures...)
}
