package skillcheck

import (
	"fmt"
	"strings"
)

var alwaysStripped = []flagSpec{
	// Mostly checkPrintFlags's unconditional rejections in internal/plan/validate.go.
	{long: "output", short: "o", value: true},
	{long: "raw", short: "r"},
	{long: "quiet", short: "q"},
	{long: "usage"},
	{long: "merge"},
	// Not in checkPrintFlags by name, but --merge-key implies --merge the same rejection covers.
	{long: "merge-key", value: true},
	{long: "stats"},
	// checkPrintFlags rejects it under --print-request, and checkSingleRecord under
	// --print-questions once -i is stripped and the run becomes single record.
	{long: "stop-on-assert"},
}

var questionsOnlyStripped = []flagSpec{
	// checkPrintFlags rejects these under --print-questions only, since --print-request still
	// needs them to build a request body.
	{long: "input", short: "i", value: true},
	{long: "jobs", short: "j", value: true},
	{long: "model", short: "m", value: true},
	{long: "state", value: true},
	{long: "state-file", value: true},
	// checkSingleRecord rejects these, since stripping -i turns the run into a single record one.
	{long: "unordered"},
	{long: "stop-on-error"},
	{long: "skip-blank"},
}

var printFlags = []flagSpec{
	{long: "print-request"},
	{long: "print-questions"},
}

var gateFlags = []flagSpec{
	// Never stripped, only catalogued for their values.
	{long: "assert", value: true},
	{long: "abstain-if", value: true},
}

var catalog = append(append(append(append([]flagSpec{}, printFlags...), alwaysStripped...),
	questionsOnlyStripped...), gateFlags...) // A kept flag's value must never be read as a flag.

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

func insertFlag(args []string, flag string) []string {
	// Nothing precedes index 0, so no flag, catalogued or not, can consume it as a value. At the
	// end, or ahead of a literal --, it stayed in reach of whatever came before.
	return append([]string{flag}, args...)
}

func provenDryRun(args []string, strip []flagSpec) bool {
	// A second line of defence that never fires today. It goes false only if stripFlags stops
	// dropping a value with its flag, insertFlag stops leading with the mode flag, or a strip set
	// names a spec the catalog lost.
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
		// A value the flag before consumed is never an occurrence, however it is spelled.
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

type occurrence struct {
	index  int
	spec   flagSpec
	joined bool
}

func specIn(spec flagSpec, specs []flagSpec) bool {
	for _, s := range specs {
		if s.long == spec.long {
			return true
		}
	}

	return false
}

func matchFlag(token string, specs []flagSpec) ([]flagMatch, bool) {
	// Every form pflag accepts: long or short, bare or joined with =, a short value attached as in
	// -ojson, and a cluster of short booleans as in -rq that may end in a value flag.
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

func matchShortCluster(token string, specs []flagSpec) ([]flagMatch, bool) {
	if len(token) < 3 || token[0] != '-' || token[1] == '-' {
		return nil, false
	}

	body := token[1:]

	var matches []flagMatch

	for i := 0; i < len(body); i++ {
		spec, found := shortSpec(body[i], specs)
		// One unrecognised character rejects the whole token, since a partial read cannot be
		// trusted to mean what the rest of it says.
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

type flagMatch struct {
	spec   flagSpec
	joined bool
}

func shortSpec(b byte, specs []flagSpec) (flagSpec, bool) {
	for _, candidate := range specs {
		if candidate.short != "" && candidate.short[0] == b {
			return candidate, true
		}
	}

	return flagSpec{}, false
}

type flagSpec struct {
	long  string
	short string
	value bool
}
