package settings

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const shownValueRunes = 40

func givenTwiceRule() Rule {
	return Rule{ID: "setting-given-twice", Notice: givenTwice}
}

func givenTwice(v View) []Notice {
	var notices []Notice

	for _, row := range settingrow.Table() {
		bound, registered := boundOn(row, v.Facts.Command)
		if !registered || row.Flag.Repeat || row.Flag.Group {
			continue
		}

		typed := v.Facts.Given.Values(row.Name)

		if notice, twice := typedTwice(row, bound.Kind, typed); twice {
			notice.Winner.Spelled = lastSpelling(v, row.Name, bound, len(typed))
			notices = append(notices, notice)
		}
	}

	return append(notices, givenTwiceFor(v)...)
}

func givenTwiceFor(v View) []Notice {
	var notices []Notice

	for _, group := range typedGroups(v.Facts.Events, v.Facts.Positional, v.Plan) {
		for _, row := range settingrow.Table() {
			if row.Scope != settingrow.ScopeQuestion || !row.Flag.Group || !overridesWhenRepeated(row.Name) {
				continue
			}

			for _, typed := range typedValues(row, group.events) {
				notice, twice := typedTwice(row, row.Kind, typed)
				if !twice {
					continue
				}

				notice.Winner.Spelled, notice.Question = "--"+row.Flag.Long, group.question
				notices = append(notices, notice)
			}
		}
	}

	return notices
}

func overridesWhenRepeated(name settingrow.Name) bool {
	// A repeated --pick or --rate adds options, --sep applies to the flags after it, and --box takes no
	// value to override.
	return name == Threshold.Name() || name == MinConfidence.Name() || name == FallbackAnswer.Name() ||
		name == descName
}

func typedValues(row settingrow.Row, events []argv.Event) [][]string {
	if row.Name != descName {
		var typed []string

		for _, event := range events {
			if event.Name == row.Flag.Long {
				typed = append(typed, event.Value)
			}
		}

		return [][]string{typed}
	}

	var (
		keys  []string
		byKey = map[string][]string{}
	)

	for _, event := range events {
		if event.Name != row.Flag.Long {
			continue
		}

		key, _, _ := strings.Cut(event.Value, "=")
		if key = strings.TrimSpace(key); byKey[key] == nil {
			keys = append(keys, key)
		}

		byKey[key] = append(byKey[key], event.Value)
	}

	typed := make([][]string, 0, len(keys))
	for _, key := range keys {
		typed = append(typed, byKey[key])
	}

	return typed
}

func typedGroups(events []argv.Event, positional string, built *plan.Plan) []typedGroup {
	if built == nil {
		return nil
	}

	// The flags before the first --ask or --for belong to the one question asked: the bare question,
	// a lone --ask or a lone question from -f.
	var leading string

	switch {
	case positional != "":
		leading = plan.PositionalID
	case len(built.Questions) == 1:
		leading = built.Questions[0].ID
	}

	var groups []typedGroup

	joining := func(id string) int {
		at := slices.IndexFunc(groups, func(g typedGroup) bool { return g.id == id })
		if at < 0 {
			question := id
			if id == plan.PositionalID && positional != "" {
				question = ""
			}

			groups = append(groups, typedGroup{id: id, question: question})
			at = len(groups) - 1
		}

		return at
	}

	current := -1

	for typedAt, event := range events {
		switch event.Name {
		case "ask":
			id, _, _ := strings.Cut(event.Value, "=")
			current = joining(id)
		case "for":
			current = joining(event.Value)
		default:
			at := current
			if at < 0 && leading != "" {
				at = joining(leading)
			}

			if at >= 0 {
				groups[at].events = append(groups[at].events, event)
				groups[at].positions = append(groups[at].positions, typedAt)
			}
		}
	}

	return groups
}

type typedGroup struct {
	id        string
	question  string
	events    []argv.Event
	positions []int
}

func typedTwice(row settingrow.Row, kind settingrow.Kind, typed []string) (Notice, bool) {
	if len(typed) < 2 {
		return Notice{}, false
	}

	last := typed[len(typed)-1]

	var overridden []string

	for _, earlier := range typed[:len(typed)-1] {
		same := func(value string) bool { return sameValue(kind, value, earlier) }
		if !same(last) && !slices.ContainsFunc(overridden, same) {
			overridden = append(overridden, earlier)
		}
	}

	if len(overridden) == 0 {
		return Notice{}, false
	}

	shown := make([]string, 0, len(overridden))
	for _, value := range overridden {
		shown = append(shown, quotedValue(value))
	}

	return Notice{
		Setting: row.Name,
		Winner:  Origin{Source: SourceFlag},
		Verb:    VerbGivenTwice,
		Values:  [2]string{quotedValue(last), joinedAnd(shown)},
		Times:   len(typed),
	}, true
}

func sameValue(kind settingrow.Kind, a, b string) bool {
	parsedA, errA := kind.Parse(a)
	parsedB, errB := kind.Parse(b)

	if errA != nil || errB != nil {
		return a == b
	}

	return reflect.DeepEqual(parsedA, parsedB)
}

func lastSpelling(v View, name settingrow.Name, bound settingrow.BoundFlag, times int) string {
	// Without the command line, as when a caller parsed a flag set of its own, the long name stands in.
	spelled := v.Facts.Given.Spelled(v.Facts.Args, name)
	if len(spelled) != times {
		return "--" + bound.Long
	}

	return spelled[len(spelled)-1]
}

func quotedValue(value string) string {
	runes := []rune(value)

	var cut string
	if len(runes) > shownValueRunes {
		runes, cut = runes[:shownValueRunes], "…"
	}

	var quoted strings.Builder

	quoted.WriteString("'")

	for _, r := range runes {
		switch {
		case r == '\n':
			quoted.WriteString(`\n`)
		case r == '\t':
			quoted.WriteString(`\t`)
		case r == '\'' || r == '\\':
			quoted.WriteString(`\` + string(r))
		case unicode.IsControl(r):
			fmt.Fprintf(&quoted, `\x%02x`, r)
		default:
			quoted.WriteRune(r)
		}
	}

	quoted.WriteString(cut + "'")

	return quoted.String()
}

func joinedAnd(values []string) string {
	if len(values) < 2 {
		return strings.Join(values, "")
	}

	return strings.Join(values[:len(values)-1], ", ") + " and " + values[len(values)-1]
}
