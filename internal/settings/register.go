package settings

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/argv"
)

// AnnotationAsksNothing marks a flag that asks no question, so a subcommand that lacks it does not
// suggest it belongs to a run.
const AnnotationAsksNothing = "onesie-asks-nothing"

// Register adds command's rows to set. A group row records through recorder, and every other row
// fills its *string, *bool, *int or *[]string entry in targets.
func Register(
	set *pflag.FlagSet, rows []settingrow.Row, recorder *argv.Recorder, command settingrow.Command, targets map[settingrow.Name]any,
) *Given {
	given := &Given{
		counts: map[settingrow.Name]int{}, values: map[settingrow.Name][]string{}, spellings: map[settingrow.Name]settingrow.BoundFlag{}, set: set,
	}

	for _, row := range rows {
		bound, ok := row.FlagOn(command)
		if !ok {
			continue
		}

		flag := addFlag(set, row, bound, recorder, targets[row.Name], command)

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
	set *pflag.FlagSet, row settingrow.Row, bound settingrow.BoundFlag, recorder *argv.Recorder, target any, command settingrow.Command,
) *pflag.Flag {
	rule := settingrow.EmptyPolicyOf(bound.Kind)

	if row.Flag.Group {
		if recorder == nil {
			panic(fmt.Sprintf("settings: %s on %s is a group flag, and no recorder was given", row.Name, command))
		}

		value := recorder.Flag(bound.Long)
		if _, isSwitch := bound.Kind.Zero().(bool); isSwitch {
			value = recorder.Switch(bound.Long)
		}

		set.VarP(checked{Value: value, rule: rule}, bound.Long, bound.Short, bound.Help)

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
		set.VarP(checkedList{values: into, rule: rule}, bound.Long, bound.Short, bound.Help)

		return set.Lookup(bound.Long)
	default:
		panic(fmt.Sprintf("settings: %s on %s has no *string, *bool, *int or *[]string target", row.Name, command))
	}

	flag := set.Lookup(bound.Long)
	flag.Value = checked{Value: flag.Value, rule: rule}

	return flag
}

func defaultOf[T any](bound settingrow.BoundFlag, zero T) T {
	if value, ok := bound.Default.(T); ok {
		return value
	}

	return zero
}

// CheckText makes the flag name in set refuse an empty or blank value with ErrEmptyValue, and a
// value that is not valid UTF-8 with ErrNotUTF8, for a flag no row registers.
func CheckText(set *pflag.FlagSet, name string) {
	flag := set.Lookup(name)
	flag.Value = checked{Value: flag.Value, rule: settingrow.RefuseBlank}
}

// Given counts how often each row's flag was set in one parse, keeps each value it was set to, and
// spells where it was typed.
type Given struct {
	counts    map[settingrow.Name]int
	values    map[settingrow.Name][]string
	spellings map[settingrow.Name]settingrow.BoundFlag
	set       *pflag.FlagSet
	parent    *Given
}

// Within makes the flags parent counts count and spell here too, for each row this Given does not
// register itself, such as a root flag a subcommand shares.
func (g *Given) Within(parent *Given) {
	if g == nil {
		return
	}

	g.parent = parent
}

// Count is how many times the flag of name was set. A repeatable flag counts nothing.
func (g *Given) Count(name settingrow.Name) int {
	owner := g.owning(name)
	if owner == nil {
		return 0
	}

	return owner.counts[name]
}

// Values lists each value the flag of name was set to, in the order typed. A repeatable flag lists
// nothing.
func (g *Given) Values(name settingrow.Name) []string {
	owner := g.owning(name)
	if owner == nil {
		return nil
	}

	return slices.Clone(owner.values[name])
}

// Spelled returns each spelling of name's flag in args in order, reading args as pflag does, so
// a cluster such as -qm holds several shorthands, a flag's value is no flag, and -- ends the flags.
func (g *Given) Spelled(args []string, name settingrow.Name) []string {
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

func (g *Given) owning(name settingrow.Name) *Given {
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
	return g.set.Lookup(name)
}

func (g *Given) byShorthand(letter string) *pflag.Flag {
	return g.set.ShorthandLookup(letter)
}

func (*Given) takesValue(flag *pflag.Flag) bool {
	return flag != nil && flag.NoOptDefVal == ""
}

type counted struct {
	pflag.Value
	given *Given
	name  settingrow.Name
}

func (c counted) Set(value string) error {
	if err := c.Value.Set(value); err != nil {
		return err
	}

	c.given.counts[c.name]++
	c.given.values[c.name] = append(c.given.values[c.name], value)

	return nil
}

type checked struct {
	pflag.Value
	rule settingrow.EmptyPolicy
}

func (v checked) Set(value string) error {
	if err := checkFlagValue(value, v.rule); err != nil {
		return err
	}

	return v.Value.Set(value)
}

func checkFlagValue(raw string, rule settingrow.EmptyPolicy) error {
	if !utf8.ValidString(raw) {
		return settingrow.ErrNotUTF8
	}

	if rule.Refuses(raw) {
		return settingrow.ErrEmptyValue
	}

	return nil
}

type checkedList struct {
	values *[]string
	rule   settingrow.EmptyPolicy
}

func (l checkedList) Set(value string) error {
	if err := checkFlagValue(value, l.rule); err != nil {
		return err
	}

	*l.values = append(*l.values, value)

	return nil
}

func (l checkedList) GetSlice() []string {
	return slices.Clone(*l.values)
}

func (l checkedList) String() string {
	return strings.Join(*l.values, ",")
}

func (checkedList) Type() string {
	return "stringArray"
}
