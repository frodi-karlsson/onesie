// Command formulagen writes the Homebrew formula for a release from the release's checksums.txt.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/frodi-karlsson/onesie/internal/formula"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "formulagen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("formulagen", flag.ContinueOnError)
	version := flags.String("version", "", "release version, with or without the leading v")
	checksums := flags.String("checksums", filepath.Join("dist", "checksums.txt"), "the release's checksums.txt")
	out := flags.String("out", filepath.Join("Formula", "onesie.rb"), "where to write the formula")

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *version == "" {
		return fmt.Errorf("-version is required")
	}

	sums, err := os.ReadFile(*checksums)
	if err != nil {
		return err
	}

	rendered, err := formula.Render(*version, bytes.NewReader(sums))
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		return err
	}

	return os.WriteFile(*out, rendered, 0o644)
}
