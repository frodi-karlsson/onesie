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
)

func main() {
	// os.Exit skips deferred calls, so run owns the cleanup and the exit code.
	os.Exit(run())
}

func run() int {
	// A write to a closed pipe on fd 1 raises SIGPIPE, whose default disposition kills the process
	// with status 141 before any Go code runs. Section 8 asks for exit 0 and a graceful shutdown,
	// which the engine already implements through the EPIPE path, so the signal is ignored to make
	// that path reachable on every platform rather than only where the runtime returns EPIPE.
	signal.Ignore(syscall.SIGPIPE)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCmd(cli.BuildInfo{Version: version, Commit: commit, Date: date})

	return cli.Execute(ctx, root)
}
