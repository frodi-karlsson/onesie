package settings

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/argv"
)

// AnnotationAsksNothing marks a flag that asks no question, so a subcommand that lacks it does not
// suggest it belongs to a run.
const AnnotationAsksNothing = "onesie-asks-nothing"

// Register adds command's rows to set, or to persistent where marked. A group row records through
// recorder, and every other row fills its *string, *bool, *int or *[]string entry in targets.
func Register(
	set, persistent *pflag.FlagSet, rows []Row, recorder *argv.Recorder, command Command, targets map[Name]any,
) *Given {
	given := &Given{
		counts: map[Name]int{}, values: map[Name][]string{}, spellings: map[Name]BoundFlag{}, sets: []*pflag.FlagSet{set},
	}
	if persistent != nil {
		given.sets = append(given.sets, persistent)
	}

	for _, row := range rows {
		bound, ok := row.FlagOn(command)
		if !ok {
			continue
		}

		into := set
		if bound.Persistent {
			into = persistent
		}

		if into == nil {
			panic(fmt.Sprintf("settings: %s on %s is persistent, and no persistent flag set was given", row.Name, command))
		}

		flag := addFlag(into, row, bound, recorder, targets[row.Name], command)

		if row.Flag.NoOptDefVal != "" {
			flag.NoOptDefVal = row.Flag.NoOptDefVal
		}

		if !row.Flag.Repeat {
			flag.Value = counted{Value: flag.Value, given: given, name: row.Name}
		}

		if row.Flag.AsksNothing {
			flag.Annotations = map[string][]string{AnnotationAsksNothing: nil}
		}

		given.spellings[row.Name] = bound
	}

	return given
}

func addFlag(
	set *pflag.FlagSet, row Row, bound BoundFlag, recorder *argv.Recorder, target any, command Command,
) *pflag.Flag {
	rule := emptyRuleOf(bound.Kind)

	if row.Flag.Group {
		if recorder == nil {
			panic(fmt.Sprintf("settings: %s on %s is a group flag, and no recorder was given", row.Name, command))
		}

		value := recorder.Flag(bound.Long)
		if _, isSwitch := bound.Kind.Zero().(bool); isSwitch {
			value = recorder.Switch(bound.Long)
		}

		set.VarP(refusingEmpty(value, rule), bound.Long, bound.Short, bound.Help)

		return set.Lookup(bound.Long)
	}

	switch into := target.(type) {
	case *string:
		set.StringVarP(into, bound.Long, bound.Short, defaultOf(bound, ""), bound.Help)
	case *bool:
		set.BoolVarP(into, bound.Long, bound.Short, defaultOf(bound, false), bound.Help)
	case *int:
		set.IntVarP(into, bound.Long, bound.Short, defaultOf(bound, 0), bound.Help)
	case *[]string:
		if rule == allowEmpty {
			set.StringArrayVarP(into, bound.Long, bound.Short, nil, bound.Help)
		} else {
			set.VarP(nonEmptyList{values: into, rule: rule}, bound.Long, bound.Short, bound.Help)
		}

		return set.Lookup(bound.Long)
	default:
		panic(fmt.Sprintf("settings: %s on %s has no *string, *bool, *int or *[]string target", row.Name, command))
	}

	flag := set.Lookup(bound.Long)
	flag.Value = refusingEmpty(flag.Value, rule)

	return flag
}

func emptyRuleOf(kind Kind) emptyRule {
	switch {
	case !refusesAsEmpty(kind, ""):
		return allowEmpty
	case refusesAsEmpty(kind, " "):
		return refuseBlank
	default:
		return refuseEmpty
	}
}

func refusesAsEmpty(kind Kind, raw string) bool {
	_, err := kind.Parse(raw)

	return errors.Is(err, ErrEmptyValue)
}

func refusingEmpty(value pflag.Value, rule emptyRule) pflag.Value {
	if rule == allowEmpty {
		return value
	}

	return nonEmpty{Value: value, rule: rule}
}

func defaultOf[T any](bound BoundFlag, zero T) T {
	if value, ok := bound.Default.(T); ok {
		return value
	}

	return zero
}

// RefuseEmpty makes the flag name in set refuse an empty or blank value with ErrEmptyValue, for a
// flag no row registers.
func RefuseEmpty(set *pflag.FlagSet, name string) {
	flag := set.Lookup(name)
	flag.Value = nonEmpty{Value: flag.Value, rule: refuseBlank}
}

// Given counts how often each row's flag was set in one parse, keeps each value it was set to, and
// spells where it was typed.
type Given struct {
	counts    map[Name]int
	values    map[Name][]string
	spellings map[Name]BoundFlag
	sets      []*pflag.FlagSet
	parent    *Given
}

// Inherit adds set, the persistent flags of a parent command, to the sets the flags are looked up in.
func (g *Given) Inherit(set *pflag.FlagSet) {
	if g == nil {
		return
	}

	g.sets = append(g.sets, set)
}

// Within makes the flags parent counts, the persistent flags of a parent command among them, count
// and spell here too, for each row this Given does not register itself.
func (g *Given) Within(parent *Given) {
	if g == nil {
		return
	}

	g.parent = parent
}

// Count is how many times the flag of name was set. A repeatable flag counts nothing.
func (g *Given) Count(name Name) int {
	owner := g.owning(name)
	if owner == nil {
		return 0
	}

	return owner.counts[name]
}

// Values lists each value the flag of name was set to, in the order typed. A repeatable flag lists
// nothing.
func (g *Given) Values(name Name) []string {
	owner := g.owning(name)
	if owner == nil {
		return nil
	}

	return slices.Clone(owner.values[name])
}

// Spelled returns each spelling of name's flag in args in order, reading args as pflag does, so
// a cluster such as -qm holds several shorthands, a flag's value is no flag, and -- ends the flags.
func (g *Given) Spelled(args []string, name Name) []string {
	owner := g.owning(name)
	if owner == nil {
		return nil
	}

	bound, known := owner.spellings[name]
	if !known {
		return nil
	}

	var spelled []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch {
		case arg == "--":
			return spelled
		case strings.HasPrefix(arg, "--"):
			long, _, inline := strings.Cut(arg[2:], "=")
			if long == bound.Long {
				spelled = append(spelled, "--"+long)
			}

			if !inline && g.takesValue(g.byName(long)) {
				i++
			}
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			var skipNext bool

			spelled, skipNext = g.spelledInCluster(arg[1:], bound.Short, spelled)
			if skipNext {
				i++
			}
		}
	}

	return spelled
}

func (g *Given) owning(name Name) *Given {
	if g == nil {
		return nil
	}

	if _, own := g.spellings[name]; own || g.parent == nil {
		return g
	}

	return g.parent.owning(name)
}

func (g *Given) spelledInCluster(shorthands, short string, spelled []string) ([]string, bool) {
	for j := range len(shorthands) {
		letter := shorthands[j : j+1]
		if short != "" && letter == short {
			spelled = append(spelled, "-"+letter)
		}

		rest := shorthands[j+1:]

		switch {
		case strings.HasPrefix(rest, "="):
			return spelled, false
		case g.takesValue(g.byShorthand(letter)):
			return spelled, rest == ""
		}
	}

	return spelled, false
}

func (g *Given) byName(name string) *pflag.Flag {
	for _, set := range g.sets {
		if flag := set.Lookup(name); flag != nil {
			return flag
		}
	}

	return nil
}

func (g *Given) byShorthand(letter string) *pflag.Flag {
	for _, set := range g.sets {
		if flag := set.ShorthandLookup(letter); flag != nil {
			return flag
		}
	}

	return nil
}

func (*Given) takesValue(flag *pflag.Flag) bool {
	return flag != nil && flag.NoOptDefVal == ""
}

type counted struct {
	pflag.Value
	given *Given
	name  Name
}

func (c counted) Set(value string) error {
	if err := c.Value.Set(value); err != nil {
		return err
	}

	c.given.counts[c.name]++
	c.given.values[c.name] = append(c.given.values[c.name], value)

	return nil
}

type nonEmpty struct {
	pflag.Value
	rule emptyRule
}

func (v nonEmpty) Set(value string) error {
	if v.rule.refuses(value) {
		return ErrEmptyValue
	}

	return v.Value.Set(value)
}

type nonEmptyList struct {
	values *[]string
	rule   emptyRule
}

func (l nonEmptyList) Set(value string) error {
	if l.rule.refuses(value) {
		return ErrEmptyValue
	}

	*l.values = append(*l.values, value)

	return nil
}

func (l nonEmptyList) GetSlice() []string {
	return slices.Clone(*l.values)
}

func (l nonEmptyList) String() string {
	return strings.Join(*l.values, ",")
}

func (nonEmptyList) Type() string {
	return "stringArray"
}
