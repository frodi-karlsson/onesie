package settings

import (
	"slices"
	"strings"
)

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

		overridden := unique(slices.DeleteFunc(typed[:len(typed)-1], func(value string) bool { return value == last }))
		if len(overridden) == 0 {
			continue
		}

		notices = append(notices, Notice{
			Setting: row.Name,
			Winner:  Origin{Source: SourceFlag, Spelled: lastSpelling(v, row.Name, bound, len(typed))},
			Verb:    VerbGivenTwice,
			Values:  [2]string{last, joinedAnd(overridden)},
			Times:   len(typed),
		})
	}

	return notices
}

func lastSpelling(v View, name Name, bound BoundFlag, times int) string {
	// Without the command line, as when a caller parsed a flag set of its own, the long name stands in.
	spelled := v.Facts.Given.Spelled(v.Facts.Args, name)
	if len(spelled) != times {
		return "--" + bound.Long
	}

	return spelled[len(spelled)-1]
}

func unique(values []string) []string {
	var kept []string

	for _, value := range values {
		if !slices.Contains(kept, value) {
			kept = append(kept, value)
		}
	}

	return kept
}

func joinedAnd(values []string) string {
	if len(values) < 2 {
		return strings.Join(values, "")
	}

	return strings.Join(values[:len(values)-1], ", ") + " and " + values[len(values)-1]
}
