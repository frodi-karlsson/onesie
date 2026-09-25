package release

import (
	"bufio"
	"flag"
	"fmt"
	"io"
)

const usage = `usage: releaseprep check-tag TAG        reads the existing tag names on stdin, exits 1 unless TAG is greater
       releaseprep bump [-dry-run] TAG  sets both plugin versions and both marketplace refs to TAG
       releaseprep prerelease TAG       exits 0 for a prerelease and 1 for a release
`

// Run is the releaseprep command, with bump editing the manifests under root through fsys.
// It returns 0 on success, 1 on a refusal and 2 on a usage error.
func Run(args []string, root string, fsys Files, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageError(stderr, "no subcommand")
	}

	switch args[0] {
	case "check-tag", "bump", "prerelease":
	default:
		return usageError(stderr, "unknown subcommand "+args[0])
	}

	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dryRun := flags.Bool("dry-run", false, "print what bump would set and write nothing")

	if err := flags.Parse(args[1:]); err != nil {
		return usageError(stderr, err.Error())
	}

	if *dryRun && args[0] != "bump" {
		return usageError(stderr, "-dry-run is only for bump")
	}

	if flags.NArg() != 1 {
		return usageError(stderr, args[0]+" takes one TAG")
	}

	tag, err := ParseTag(flags.Arg(0))
	if err != nil {
		return usageError(stderr, err.Error())
	}

	switch args[0] {
	case "check-tag":
		return checkTag(tag, stdin, stderr)
	case "bump":
		return bump(root, fsys, tag, *dryRun, stdout, stderr)
	}

	if tag.Prerelease() {
		return 0
	}

	return 1
}

func usageError(stderr io.Writer, reason string) int {
	return say(stderr, 2, "releaseprep: %s\n%s", reason, usage)
}

func checkTag(tag Tag, stdin io.Reader, stderr io.Writer) int {
	var existing []string

	scanner := bufio.NewScanner(stdin)
	for scanner.Scan() {
		existing = append(existing, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return refuse(stderr, err)
	}

	if err := CheckTag(tag.String(), existing); err != nil {
		return refuse(stderr, err)
	}

	return 0
}

func bump(root string, fsys Files, tag Tag, dryRun bool, stdout, stderr io.Writer) int {
	changes, err := Bump(root, tag, dryRun, fsys)
	for _, change := range changes {
		if say(stdout, 0, "%s\n", change.Describe(dryRun)) != 0 {
			return 1
		}
	}

	if err != nil {
		return refuse(stderr, err)
	}

	return 0
}

func refuse(stderr io.Writer, err error) int {
	return say(stderr, 1, "releaseprep: %v\n", err)
}

func say(w io.Writer, code int, format string, args ...any) int {
	if _, err := fmt.Fprintf(w, format, args...); err != nil && code == 0 {
		return 1
	}

	return code
}
