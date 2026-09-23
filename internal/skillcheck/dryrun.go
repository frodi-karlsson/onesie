package skillcheck

import "strings"

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

// assertFlag is never stripped. It is in the catalog purely so a value that happens to spell
// --assert is never misread as the flag itself.
var assertFlag = flagSpec{long: "assert", value: true}

// catalog is every flag dryRunArgs recognises positionally. A value belonging to a flag it keeps
// must never be mistaken for a flag of its own, and this is what makes that possible.
var catalog = append(append(append(append([]flagSpec{}, printFlags...), alwaysStripped...),
	questionsOnlyStripped...), assertFlag)

type flagSpec struct {
	long  string
	short string
	value bool
}

// occurrence is one genuine, positionally verified match of a catalog flag. A value token
// consumed by the flag before it is never itself an occurrence, however it happens to be spelled.
type occurrence struct {
	index  int
	spec   flagSpec
	joined bool
}

func dryRunArgs(tokens []string) (args []string, ok bool) {
	if len(tokens) == 0 || tokens[0] != "jev" {
		return nil, false
	}

	raw := tokens[1:]

	mode := "--print-request"
	if hasFlag(raw, "assert") {
		mode = "--print-questions"
	}

	strip := append(append([]flagSpec{}, printFlags...), alwaysStripped...)
	if mode == "--print-questions" {
		strip = append(strip, questionsOnlyStripped...)
	}

	final := insertFlag(stripFlags(raw, strip), mode)

	if !provenDryRun(final, strip) {
		return nil, false
	}

	return final, true
}

func hasFlag(args []string, long string) bool {
	for _, occ := range flagOccurrences(args, catalog) {
		if occ.spec.long == long {
			return true
		}
	}

	return false
}

func stripFlags(args []string, strip []flagSpec) []string {
	drop := make(map[int]bool)

	for _, occ := range flagOccurrences(args, catalog) {
		if !specIn(occ.spec, strip) {
			continue
		}

		drop[occ.index] = true

		if occ.spec.value && !occ.joined {
			drop[occ.index+1] = true
		}
	}

	out := make([]string, 0, len(args))

	for i, arg := range args {
		if !drop[i] {
			out = append(out, arg)
		}
	}

	return out
}

// insertFlag places flag at the very front of args, immediately after the binary itself. Nothing
// ever precedes that position, so no flag jev knows about, catalogued here or not, can consume it
// as a value. Appending it at the end, or ahead of a literal --, still left it in reach of
// whatever came directly before.
func insertFlag(args []string, flag string) []string {
	return append([]string{flag}, args...)
}

// provenDryRun re-derives the flag occurrences in a built argument list from scratch rather than
// trusting stripFlags and insertFlag got it right. It fails closed. Anything other than exactly
// one dry run flag, or a flag strip was supposed to remove still occurring, is not proof, and the
// caller must not run what it cannot prove.
func provenDryRun(args []string, strip []flagSpec) bool {
	modeCount := 0

	for _, occ := range flagOccurrences(args, catalog) {
		switch {
		case occ.spec.long == "print-request" || occ.spec.long == "print-questions":
			modeCount++
		case specIn(occ.spec, strip):
			return false
		}
	}

	return modeCount == 1
}

func flagOccurrences(args []string, specs []flagSpec) []occurrence {
	var occs []occurrence

	protected := false

	for i, arg := range args {
		if protected {
			protected = false

			continue
		}

		spec, joined, matched := matchFlag(arg, specs)
		if !matched {
			continue
		}

		occs = append(occs, occurrence{index: i, spec: spec, joined: joined})

		if spec.value && !joined {
			protected = true
		}
	}

	return occs
}

func specIn(spec flagSpec, specs []flagSpec) bool {
	for _, s := range specs {
		if s.long == spec.long {
			return true
		}
	}

	return false
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
