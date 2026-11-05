package settings

import (
	"fmt"

	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

// ParseRun parses the flag of every run scope row command registers through the row's kind and
// check, in table order, and returns the typed values beside each untyped row's default.
func ParseRun(rows []Row, given *Given, command Command) (*Merged, error) {
	merged := &Merged{}

	for _, row := range rows {
		bound, registered := row.FlagOn(command)
		if !registered || !parsedPerRun(row, command) {
			continue
		}

		flag := given.byName(bound.Long)
		if flag == nil {
			continue
		}

		if !flag.Changed {
			merged.Set(row.Name, untyped(row, bound), SourceDefault)

			continue
		}

		value, err := parseFlag(row, bound, flag)
		if err != nil {
			return nil, err
		}

		merged.Set(row.Name, value, SourceFlag)
	}

	return merged, nil
}

func untyped(row Row, bound BoundFlag) any {
	if row.Flag.Repeat {
		return []string(nil)
	}

	return defaultOf[any](bound, bound.Kind.Zero())
}

func parsedPerRun(row Row, command Command) bool {
	// A group row binds to a question and parses in plan.Assemble. A command that gives a flag a kind
	// of its own, as calibrate does for its report and its input, words that flag's refusals in its
	// own rules.
	return row.Scope == ScopeRun && !row.Flag.Group && row.Flag.PerCommand[command].Kind == nil
}

func parseFlag(row Row, bound BoundFlag, flag *pflag.Flag) (any, error) {
	if row.Flag.Repeat {
		list := sliceOf(flag)
		for _, raw := range list {
			if _, err := bound.Kind.Parse(raw); err != nil {
				return nil, fmt.Errorf("%s %w", spelling(bound), err)
			}
		}

		return list, nil
	}

	value, err := bound.Kind.Parse(flag.Value.String())

	// A number is refused rather than clamped, in every mode, and the row's check words its range.
	if row.Check != nil && value != nil {
		if checkErr := row.Check(value); checkErr != nil {
			return nil, checkErr
		}
	}

	if err != nil {
		return nil, fmt.Errorf("%s %w", spelling(bound), err)
	}

	return value, nil
}

func sliceOf(flag *pflag.Flag) []string {
	if list, isList := flag.Value.(interface{ GetSlice() []string }); isList {
		return list.GetSlice()
	}

	return []string{flag.Value.String()}
}

func spelling(bound BoundFlag) string {
	if bound.Short != "" {
		return "-" + bound.Short
	}

	return "--" + bound.Long
}

// PolicyNumbers returns the kinds of the threshold and min_confidence rows, which parse and bound a
// policy number for plan.Assemble and plan.Validate.
func PolicyNumbers() plan.Numbers {
	numbers := plan.Numbers{}

	if row, found := Lookup(Threshold.Name()); found {
		numbers.Threshold = row.Kind
	}

	if row, found := Lookup(MinConfidence.Name()); found {
		numbers.MinConfidence = row.Kind
	}

	return numbers
}
