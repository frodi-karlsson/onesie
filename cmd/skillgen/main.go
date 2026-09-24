// Command skillgen generates the per client skill files from skills/, per
// docs/onesie-skills-spec.md section 3.
package main

import (
	"fmt"
	"os"

	"github.com/frodi-karlsson/onesie/internal/skillgen"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "onesie:", err)
		os.Exit(1)
	}

	if err := skillgen.Generate(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
