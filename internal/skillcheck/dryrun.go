package skillcheck

import "strings"

type flagSpec struct {
	long  string
	short string
	value bool
}

// alwaysStripped are the flags checkPrintFlags in internal/plan/validate.go rejects on every dry
// run, print-request and print-questions alike.
var alwaysStripped = []flagSpec{
	{long: "output", short: "o", value: true},
	{long: "raw", short: "r"},
	{long: "quiet", short: "q"},
	{long: "usage"},
	{long: "merge"},
	// Not in checkPrintFlags by name, but --merge-key implies --merge the same rejection covers.
	{long: "merge-key", value: true},
	{long: "stats"},
}

// questionsOnlyStripped are the flags checkPrintFlags rejects only for --print-questions, since
// --print-request still needs them to build a request body.
var questionsOnlyStripped = []flagSpec{
	{long: "input", short: "i", value: true},
	{long: "jobs", short: "j", value: true},
	{long: "model", short: "m", value: true},
	{long: "state", value: true},
	{long: "state-file", value: true},
}

var printFlags = []flagSpec{
	{long: "print-request"},
	{long: "print-questions"},
}

func dryRunArgs(tokens []string) (args []string, ok bool) {
	if len(tokens) == 0 || tokens[0] != "jev" {
		return nil, false
	}

	args = append([]string{}, tokens[1:]...)

	mode := "--print-request"
	if hasFlag(args, "assert") {
		mode = "--print-questions"
	}

	args = stripFlags(args, printFlags)
	args = stripFlags(args, alwaysStripped)

	if mode == "--print-questions" {
		args = stripFlags(args, questionsOnlyStripped)
	}

	return insertFlag(args, mode), true
}

func hasFlag(args []string, long string) bool {
	_, _, found := findFlag(args, []flagSpec{{long: long}})

	return found
}

func stripFlags(args []string, specs []flagSpec) []string {
	out := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		spec, joined, matched := matchFlag(args[i], specs)
		if !matched {
			out = append(out, args[i])

			continue
		}

		if spec.value && !joined && i+1 < len(args) {
			i++
		}
	}

	return out
}

func findFlag(args []string, specs []flagSpec) (flagSpec, bool, bool) {
	for _, arg := range args {
		if spec, joined, matched := matchFlag(arg, specs); matched {
			return spec, joined, true
		}
	}

	return flagSpec{}, false, false
}

func matchFlag(token string, specs []flagSpec) (flagSpec, bool, bool) {
	for _, candidate := range specs {
		if long := "--" + candidate.long; token == long {
			return candidate, false, true
		} else if strings.HasPrefix(token, long+"=") {
			return candidate, true, true
		}

		if candidate.short == "" {
			continue
		}

		if short := "-" + candidate.short; token == short {
			return candidate, false, true
		} else if strings.HasPrefix(token, short+"=") {
			return candidate, true, true
		}
	}

	return flagSpec{}, false, false
}

func insertFlag(args []string, flag string) []string {
	for i, arg := range args {
		// Everything after a literal -- is a positional argument, so the mode flag has to land
		// ahead of it or it would be read as the question text instead of a flag.
		if arg == "--" {
			out := make([]string, 0, len(args)+1)
			out = append(out, args[:i]...)
			out = append(out, flag)

			return append(out, args[i:]...)
		}
	}

	return append(args, flag)
}
