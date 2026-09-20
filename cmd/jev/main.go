// Command jev is the command line interface for TypeSafe Jev.
package main

import (
	"context"
	"errors"
	"fmt"
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

	if err := root.ExecuteContext(ctx); err != nil {
		if worthReporting(err) {
			fmt.Fprintln(os.Stderr, "jev:", err)
		}

		return 1
	}

	return 0
}

func worthReporting(err error) bool {
	return !errors.Is(err, context.Canceled)
}
