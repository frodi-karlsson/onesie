package release_test

import (
	"os"
	"testing"

	"github.com/itchyny/gojq/cli"

	"github.com/frodi-karlsson/onesie/internal/release"
)

func TestMain(m *testing.M) {
	switch os.Getenv("ONESIE_RELEASE_CHILD") {
	case "releaseprep":
		os.Exit(release.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	case "gojq":
		os.Exit(cli.Run())
	}

	os.Exit(m.Run())
}
