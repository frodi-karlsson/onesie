// Command releaseprep checks a release tag against the existing ones and bumps the plugin manifests to it.
package main

import (
	"os"

	"github.com/frodi-karlsson/onesie/internal/release"
)

func main() {
	os.Exit(release.Run(os.Args[1:], ".", release.OSFiles{}, os.Stdin, os.Stdout, os.Stderr))
}
