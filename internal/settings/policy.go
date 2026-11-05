package settings

import "github.com/frodi-karlsson/onesie/internal/plan"

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
