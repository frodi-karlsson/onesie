package skillcheck

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	printFlags      = []string{"print-request", "print-questions"}
	answerFileFlags = []string{"out", "resume", "offline", "prune"}
)

// NewCatalog reads each flag in the command tree under root, its spellings and whether it takes a
// value. always and questionsOnly name the flags a dry run refuses, which only the caller's flag rules know.
func NewCatalog(root *cobra.Command, always, questionsOnly []string) (*Catalog, error) {
	catalog := &Catalog{}
	seen := map[string]bool{}

	add := func(flag *pflag.Flag) {
		if seen[flag.Name] {
			return
		}

		seen[flag.Name] = true
		catalog.specs = append(catalog.specs, flagSpec{
			long: flag.Name, short: flag.Shorthand, value: flag.NoOptDefVal == "",
		})
	}

	commands := []*cobra.Command{root}
	for len(commands) > 0 {
		cmd := commands[0]
		commands = append(commands[1:], cmd.Commands()...)
		cmd.LocalFlags().VisitAll(add)
	}

	stripped := append(append(append([]string{}, printFlags...), always...), answerFileFlags...)

	var err error

	if catalog.always, err = catalog.named(stripped); err != nil {
		return nil, err
	}

	if catalog.questionsOnly, err = catalog.named(questionsOnly); err != nil {
		return nil, err
	}

	return catalog, nil
}

// Catalog knows how each flag of a onesie command tree is spelled, so it can turn a command into
// its dry run.
type Catalog struct {
	specs         []flagSpec
	always        []flagSpec
	questionsOnly []flagSpec
}

func (c *Catalog) named(names []string) ([]flagSpec, error) {
	specs := make([]flagSpec, 0, len(names))

	for _, name := range names {
		at := slices.IndexFunc(c.specs, func(spec flagSpec) bool { return spec.long == name })
		if at < 0 {
			return nil, fmt.Errorf("onesie: the command tree has no flag --%s for a dry run to strip", name)
		}

		specs = append(specs, c.specs[at])
	}

	return specs, nil
}

// DryRunArgs turns a tokenized onesie command into the argv of its dry run. It strips the flags a
// dry run rejects and adds --print-request, or --print-questions for a command with a typed gate.
func (c *Catalog) DryRunArgs(tokens []string) (args []string, reason string, err error) {
	if len(tokens) == 0 || tokens[0] != "onesie" {
		return nil, "is not a onesie invocation", nil
	}

	raw := tokens[1:]

	mode := "--print-request"
	if c.hasFlag(raw, "assert") || c.hasFlag(raw, "abstain-if") {
		mode = "--print-questions"
	}

	strip := c.always
	if mode == "--print-questions" {
		strip = append(append([]flagSpec{}, c.always...), c.questionsOnly...)
	}

	final := insertFlag(c.stripFlags(raw, strip), mode)

	if !c.provenDryRun(final, strip) {
		return nil, "", fmt.Errorf(
			"onesie: built %s from %s, which is not provably a dry run. "+
				"this is a skillcheck bug, not a skill problem",
			strings.Join(final, " "), strings.Join(tokens, " "))
	}

	return final, "", nil
}

// pairingProblem names the first pairing rule of --out, --resume, --offline and --prune that the
// tokenized command breaks, which the dry run would hide by stripping them. It is empty when none is.
func (c *Catalog) pairingProblem(tokens []string) string {
	has := func(long string) bool { return c.hasFlag(tokens[1:], long) }

	switch {
	case has("resume") && !has("out"):
		return "--resume needs --out"
	case has("offline") && (!has("out") || !has("resume")):
		return "--offline needs --out and --resume"
	case has("prune") && (!has("resume") || !has("id")):
		return "--prune needs --resume and --id"
	default:
		return ""
	}
}

func (c *Catalog) hasAnswerFileFlag(tokens []string) bool {
	for _, long := range answerFileFlags {
		if c.hasFlag(tokens[1:], long) {
			return true
		}
	}

	return false
}

func (c *Catalog) hasFlag(args []string, long string) bool {
	for _, occ := range flagOccurrences(args, c.specs) {
		if occ.spec.long == long {
			return true
		}
	}

	return false
}

func (c *Catalog) stripFlags(args []string, strip []flagSpec) []string {
	drop := make(map[int]bool)

	for _, occ := range flagOccurrences(args, c.specs) {
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
	// end, or ahead of a literal --, it would stay in reach of whatever comes before it.
	return append([]string{flag}, args...)
}

func (c *Catalog) provenDryRun(args []string, strip []flagSpec) bool {
	// A second line of defence that never fires today. It goes false only if stripFlags stops
	// dropping a value with its flag, or insertFlag stops leading with the mode flag.
	modeCount := 0

	for _, occ := range flagOccurrences(args, c.specs) {
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
