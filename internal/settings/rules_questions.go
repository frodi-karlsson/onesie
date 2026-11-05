package settings

import (
	"errors"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

// ErrNoQuestion is the refusal of a run that asks nothing.
var ErrNoQuestion = errors.New("no question given. Pass a question, --ask, or -f")

func noQuestionRule() Rule {
	return Rule{
		ID:    "needs-a-question",
		Reads: []Name{"ask", File.Name()},
		Check: func(v View) error {
			if v.Plan != nil && len(v.Plan.Questions) == 0 {
				return ErrNoQuestion
			}

			return nil
		},
	}
}

func replaceNeedsFileRule() Rule {
	return Rule{
		ID:    "replace-needs-file",
		Reads: []Name{Replace.Name(), File.Name()},
		Check: func(v View) error {
			if Get(v.Merged, Replace) && Get(v.Merged, File) == "" {
				return errors.New("--replace applies to -f, which was not given")
			}

			return nil
		},
	}
}

func questionsValidRule() Rule {
	return Rule{
		ID:    "questions-valid",
		Reads: []Name{"ask", File.Name()},
		Check: func(v View) error {
			if v.Plan == nil {
				return nil
			}

			_, err := plan.Validate(v.Plan, Get(v.Merged, File))

			return err
		},
		Warn: func(v View) []string {
			if v.Plan == nil {
				return nil
			}

			warnings, err := plan.Validate(v.Plan, Get(v.Merged, File))
			if err != nil {
				// Check reports the refusal. The warnings of the questions ahead of it still print.
				return warnings
			}

			return warnings
		},
	}
}
