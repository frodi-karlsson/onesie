// Command proseblocks writes the prose blocks of the Go and Markdown files it is given as JSON lines
// of {"id":"path:line","text":"..."}, for the history lessons check to ask about.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"

	"github.com/frodi-karlsson/onesie/internal/proseblocks"
)

func main() {
	if err := run(os.Args[1:], osFiles{}, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "proseblocks:", err)
		os.Exit(1)
	}
}

func run(paths []string, files proseblocks.FileReader, stdout io.Writer) error {
	blocks, err := proseblocks.Extract(files, paths)
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
