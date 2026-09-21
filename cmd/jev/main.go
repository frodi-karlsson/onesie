// Command jev is the command line interface for TypeSafe Jev.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCmd(cli.BuildInfo{Version: version, Commit: commit, Date: date})

	return cli.Execute(ctx, root)
}
