// Command onesie is the command line interface for TypeSafe Jev.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/frodi-karlsson/onesie/internal/cli"
)

// Overwritten at link time by the ldflags in the Makefile and .goreleaser.yml.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
	tag     = ""
)

func main() {
	// os.Exit skips deferred calls, so run owns the cleanup and the exit code.
	os.Exit(run())
}

func run() int {
	// A write to a closed stdout raises SIGPIPE, which kills the process with status 141 before any
	// Go code runs. A closed stdout should exit 0, which the engine gives through the EPIPE path,
	// so the signal is ignored to reach that path on every platform.
	signal.Ignore(syscall.SIGPIPE)

	ctx, stop := interruptible(context.Background())
	defer stop()

	root := cli.NewRootCmd(cli.BuildInfo{Version: version, Commit: commit, Date: date, Tag: tag})

	return cli.Execute(ctx, root)
}

func interruptible(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)

	// Only the first signal is caught. Releasing the handler once the run is cancelled gives a
	// second one its default disposition, so a shutdown that hangs still dies on the next one.
	context.AfterFunc(ctx, stop)

	return ctx, stop
}
