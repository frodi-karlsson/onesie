// Command settingsgen writes the question file schema and the REFERENCE.md settings blocks from the
// settings rows. With -check it writes nothing and fails when a file is out of date.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/frodi-karlsson/onesie/internal/settingsgen"
)

func main() {
	check := flag.Bool("check", false, "name every generated file that is out of date, write nothing, and exit 1 if any is")
	flag.Parse()

	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "onesie:", err)
		os.Exit(1)
	}

	if !*check {
		if genErr := settingsgen.Generate(root, settingsgen.OS()); genErr != nil {
			fmt.Fprintln(os.Stderr, genErr)
			os.Exit(1)
		}

		return
	}

	stale, err := settingsgen.Check(root, settingsgen.OS())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	for _, name := range stale {
		fmt.Fprintf(os.Stderr, "onesie: %s is out of date, run make settings\n", name)
	}

	if len(stale) > 0 {
		os.Exit(1)
	}
}
