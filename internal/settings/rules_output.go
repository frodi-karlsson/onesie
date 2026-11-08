package settings

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// WrapKey is the key a text or lines record wraps under, so --merge-key cannot take it there.
const WrapKey = "state"

func delimiterRule() Rule {
	return Rule{
		ID: "delimiter-needs-csv",
		Check: func(v View) error {
			if !typed(v, Delimiter.Name()) {
				return nil
			}

			mode := inputModeOf(v)
			delimiter, err := input.ParseDelimiter(Get(v.Merged, Delimiter))

			switch {
			case err == nil && delimiter.Detect && mode != input.CSV:
				return errors.New("--delimiter auto needs -i csv, since it reads the separator from the input's header row")
			case mode == input.CSV || Get(v.Merged, Output) == "csv":
				return nil
			default:
				return fmt.Errorf("--delimiter applies to -i csv and -o csv. -i %s %s",
					inputName(v), delimiterlessReason(mode))
			}
		},
	}
}

func delimiterlessReason(mode input.Mode) string {
	if mode == input.TSV {
		return "is tab separated by definition"
	}

	return "has no delimiter"
}

func mergeOutputRule() Rule {
	return Rule{
		ID: "merge-needs-mergeable-output",
		Check: func(v View) error {
			if merging(v) && !mergeable(v) {
				return fmt.Errorf("%s needs -o json, values, csv or tsv", mergeName(v))
			}

			return nil
		},
	}
}

func mergeable(v View) bool {
	// auto resolves to json under a merge, so an explicit auto is as mergeable as an absent flag.
	// The other -o refusals deliberately fire on auto, since a mode they ignore does nothing there.
	switch Get(v.Merged, Output) {
	case "", "auto", "json", "values", "csv", "tsv":
		return true
	default:
		return false
	}
}

func mergeKeyWrapsRule() Rule {
	return Rule{
		ID: "merge-key-not-wrapped",
		Check: func(v View) error {
			name := Get(v.Merged, Input)
			if Get(v.Merged, MergeKey) != WrapKey || (name != "text" && name != "lines") {
				return nil
			}

			return fmt.Errorf("--merge-key %s does not apply to -i %s, which wraps every record under a %s key",
				WrapKey, name, WrapKey)
		},
	}
}

func delimitedOutputRule() Rule {
	return Rule{
		ID: "delimited-output",
		Check: func(v View) error {
			output, name := Get(v.Merged, Output), Get(v.Merged, Input)
			if output != "csv" && output != "tsv" {
				return nil
			}

			switch {
			case Get(v.Merged, MergeKey) != "":
				return fmt.Errorf("--merge-key does not apply to -o %s, which puts the answers in columns", output)
			case merging(v) && name != "csv" && name != "tsv":
				return fmt.Errorf(
					"-o %s with --merge needs -i csv or -i tsv, since other input has no fixed columns", output)
			case Get(v.Merged, Usage):
				return fmt.Errorf("--usage does not apply to -o %s, which has no column for it", output)
			default:
				return nil
			}
		},
	}
}

func usageWritesAnswersRule() Rule {
	return Rule{
		ID: "usage-needs-a-usage-column",
		Check: func(v View) error {
			var mode string

			switch output := Get(v.Merged, Output); {
			case !Get(v.Merged, Usage):
				return nil
			case output == "values", output == "raw":
				mode = "-o " + output
			default:
				return nil
			}

			return fmt.Errorf("--usage does not apply to %s, which writes only the answers. Use -o json", mode)
		},
	}
}

func fallbackColumnsRule() Rule {
	return Rule{
		ID: "columns-unique",
		Check: func(v View) error {
			output := Get(v.Merged, Output)
			if v.Plan == nil || (output != "csv" && output != "tsv") {
				return nil
			}

			ids := questionIDs(v.Plan.Questions)

			for _, question := range v.Plan.Questions {
				if question.Shape != plan.Box {
					continue
				}

				for _, column := range plan.BoxColumns(question.ID) {
					if slices.Contains(ids, column) {
						return fmt.Errorf("question '%s' takes a column name box question '%s' writes. "+
							"Rename one", column, question.ID)
					}
				}
			}

			for _, question := range v.Plan.Questions {
				if question.Policy.Fallback == nil {
					continue
				}

				if taken := plan.FallbackColumn(question.ID); slices.Contains(ids, taken) {
					return fmt.Errorf("question '%s' has the name of the fallback column -o %s writes for '%s'",
						taken, output, question.ID)
				}
			}

			return nil
		},
	}
}

func questionIDs(questions []plan.Question) []string {
	ids := make([]string, 0, len(questions))
	for _, question := range questions {
		ids = append(ids, question.ID)
	}

	return ids
}

func rawNeedsOneQuestionRule() Rule {
	return Rule{
		ID: "raw-needs-one-question",
		Check: func(v View) error {
			if v.Plan == nil || Get(v.Merged, Output) != "raw" || len(v.Plan.Questions) == 1 {
				return nil
			}

			return fmt.Errorf("-o raw needs a single question. %s were asked", quotedIDs(v.Plan.Questions))
		},
	}
}

func quotedIDs(questions []plan.Question) string {
	names := make([]string, 0, len(questions))
	for _, question := range questions {
		names = append(names, "'"+question.ID+"'")
	}

	return strings.Join(names, ", ")
}
