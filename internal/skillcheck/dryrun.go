package skillcheck

import (
	"fmt"
	"strings"
)

// Rejected on every dry run this package builds, whichever mode runs. Most are checkPrintFlags's
// own rejections in internal/plan/validate.go, unconditional there. --stop-on-assert is here for
// a second reason too: checkPrintFlags rejects it under --print-request, and checkSingleRecord
// rejects it under --print-questions once -i is stripped and the run becomes single record.
var alwaysStripped = []flagSpec{
	{long: "output", short: "o", value: true},
	{long: "raw", short: "r"},
	{long: "quiet", short: "q"},
	{long: "usage"},
	{long: "merge"},
	// Not in checkPrintFlags by name, but --merge-key implies --merge the same rejection covers.
	{long: "merge-key", value: true},
	{long: "stats"},
	{long: "stop-on-assert"},
}

// Rejected only when the dry run is --print-questions. The first five, checkPrintFlags rejects
// outright, since --print-request still needs them to build a request body. The last three,
// checkSingleRecord rejects instead, and only because stripping -i turns the run into a single
// record one, which is streaming input's own rule rather than a print flag's.
var questionsOnlyStripped = []flagSpec{
	{long: "input", short: "i", value: true},
	{long: "jobs", short: "j", value: true},
	{long: "model", short: "m", value: true},
	{long: "state", value: true},
	{long: "state-file", value: true},
	{long: "unordered"},
	{long: "stop-on-error"},
	{long: "skip-blank"},
}

var printFlags = []flagSpec{
	{long: "print-request"},
	{long: "print-questions"},
}

// Never stripped. It is in the catalog purely so a value that happens to spell --assert is never
// misread as the flag itself.
var assertFlag = flagSpec{long: "assert", value: true}

// Every flag dryRunArgs recognises positionally, so a value belonging to a flag it keeps is never
// mistaken for a flag of its own.
var catalog = append(append(append(append([]flagSpec{}, printFlags...), alwaysStripped...),
	questionsOnlyStripped...), assertFlag)

type flagSpec struct {
	long  string
	short string
	value bool
}

// A value token consumed by the flag before it is never itself an occurrence, however it happens
// to be spelled.
type occurrence struct {
	index  int
	spec   flagSpec
	joined bool
}

type flagMatch struct {
	spec   flagSpec
	joined bool
}

func dryRunArgs(tokens []string) (args []string, reason string, err error) {
	if len(tokens) == 0 || tokens[0] != "onesie" {
		return nil, "is not a onesie invocation", nil
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
		return nil, "", fmt.Errorf(
			"onesie: built %s from %s, which is not provably a dry run. "+
				"this is a skillcheck bug, not a skill problem",
			strings.Join(final, " "), strings.Join(tokens, " "))
	}

	return final, "", nil
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

// Nothing precedes index 0, so no flag onesie knows about, catalogued here or not, can ever consume
// it as a value. Appending it at the end, or ahead of a literal --, still left it in reach of
// whatever came directly before.
func insertFlag(args []string, flag string) []string {
	return append([]string{flag}, args...)
}

// A second line of defence once stripFlags and insertFlag are trusted to have done their job, so
// it never fires today. It goes false again only if stripFlags stops dropping a value flag's
// value together with the flag, insertFlag stops placing the mode flag at index 0, or a spec is
// removed from catalog while it still appears in a strip set.
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

		// Everything from here on is positional, per pflag's own rule, so nothing after it is
		// eligible to match a flag.
		if arg == "--" {
			break
		}

		matches, matched := matchFlag(arg, specs)
		if !matched {
			continue
		}

		for _, m := range matches {
			occs = append(occs, occurrence{index: i, spec: m.spec, joined: m.joined})
		}

		if last := matches[len(matches)-1]; last.spec.value && !last.joined {
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

// Every form pflag itself accepts is recognised here: the long spelling bare or joined with =,
// the short spelling bare or joined with =, a short flag with its value attached directly as in
// -ojson, and a cluster of short boolean flags as in -rq, ending in a value flag that claims the
// remainder of the token if one is present.
func matchFlag(token string, specs []flagSpec) ([]flagMatch, bool) {
	for _, candidate := range specs {
		if long := "--" + candidate.long; token == long {
			return []flagMatch{{spec: candidate, joined: false}}, true
		} else if strings.HasPrefix(token, long+"=") {
			return []flagMatch{{spec: candidate, joined: true}}, true
		}

		if candidate.short == "" {
			continue
		}

		if short := "-" + candidate.short; token == short {
			return []flagMatch{{spec: candidate, joined: false}}, true
		} else if strings.HasPrefix(token, short+"=") {
			return []flagMatch{{spec: candidate, joined: true}}, true
		}
	}

	return matchShortCluster(token, specs)
}

// A single dash token is read one character at a time, each one a short flag from specs. A
// boolean flag consumes one character and the read continues. A value flag consumes every
// character left in the token as its value, joined when any remain and unjoined, needing the next
// token instead, when the value flag is the token's last character. An unrecognised character
// anywhere in the cluster means the whole token is not a match, since a partial read cannot be
// trusted to mean what the rest of it says.
func matchShortCluster(token string, specs []flagSpec) ([]flagMatch, bool) {
	if len(token) < 3 || token[0] != '-' || token[1] == '-' {
		return nil, false
	}

	body := token[1:]

	var matches []flagMatch

	for i := 0; i < len(body); i++ {
		spec, found := shortSpec(body[i], specs)
		if !found {
			return nil, false
		}

		if !spec.value {
			matches = append(matches, flagMatch{spec: spec, joined: false})

			continue
		}

		matches = append(matches, flagMatch{spec: spec, joined: i+1 < len(body)})

		break
	}

	if len(matches) == 0 {
		return nil, false
	}

	return matches, true
}

func shortSpec(b byte, specs []flagSpec) (flagSpec, bool) {
	for _, candidate := range specs {
		if candidate.short != "" && candidate.short[0] == b {
			return candidate, true
		}
	}

	return flagSpec{}, false
}
