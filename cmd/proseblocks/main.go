// Command proseblocks reads a unified diff on stdin and writes the prose blocks of the Go and Markdown
// files that overlap an added line as JSON lines of {"id":"path:line","text":"..."}.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/frodi-karlsson/onesie/internal/proseblocks"
)

var errArgs = errors.New("proseblocks takes no arguments, pipe a diff to it such as git diff -U0 main -- '*.go' '*.md'")

func main() {
	if err := run(os.Args[1:], os.Stdin, osFiles{}, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "proseblocks:", err)
		os.Exit(1)
	}
}

func run(args []string, diff io.Reader, files proseblocks.FileReader, stdout io.Writer) error {
	if len(args) > 0 {
		return errArgs
	}

	changes, err := proseblocks.ParseDiff(diff)
	if err != nil {
		return err
	}

	blocks, err := proseblocks.Extract(files, changes)
	if err != nil {
		return err
	}

	out := bufio.NewWriter(stdout)
	if err := proseblocks.Write(out, blocks); err != nil {
		return err
	}

	return out.Flush()
}

type osFiles struct{}

func (osFiles) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}
