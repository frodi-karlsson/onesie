package settings

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode"
)

const shownValueRunes = 40

func givenTwiceRule() Rule {
	return Rule{ID: "setting-given-twice", Notice: givenTwice}
}

func givenTwice(v View) []Notice {
	var notices []Notice

	for _, row := range Table() {
		bound, registered := boundOn(row, v.Facts.Command)
		if !registered || row.Flag.Repeat || row.Flag.Group {
			continue
		}

		typed := v.Facts.Given.Values(row.Name)
		if len(typed) < 2 {
			continue
		}

		last := typed[len(typed)-1]

		var overridden []string

		for _, earlier := range typed[:len(typed)-1] {
			same := func(value string) bool { return sameValue(bound.Kind, value, earlier) }
			if !same(last) && !slices.ContainsFunc(overridden, same) {
				overridden = append(overridden, earlier)
			}
		}

		if len(overridden) == 0 {
			continue
		}

		shown := make([]string, 0, len(overridden))
		for _, value := range overridden {
			shown = append(shown, quotedValue(value))
		}

		notices = append(notices, Notice{
			Setting: row.Name,
			Winner:  Origin{Source: SourceFlag, Spelled: lastSpelling(v, row.Name, bound, len(typed))},
			Verb:    VerbGivenTwice,
			Values:  [2]string{quotedValue(last), joinedAnd(shown)},
			Times:   len(typed),
		})
	}

	return notices
}

func sameValue(kind Kind, a, b string) bool {
	parsedA, errA := kind.Parse(a)
	parsedB, errB := kind.Parse(b)

	if errA != nil || errB != nil {
		return a == b
	}

	return reflect.DeepEqual(parsedA, parsedB)
}

func lastSpelling(v View, name Name, bound BoundFlag, times int) string {
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
