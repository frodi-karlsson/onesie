package settings

import (
	"errors"
	"fmt"
	"slices"

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

func forSelectsRule() Rule {
	return Rule{
		ID:    "for-selects-a-file-question",
		Reads: []Name{"for", File.Name(), Replace.Name()},
		Check: func(v View) error {
			if v.Plan == nil || len(v.Plan.Selections) == 0 {
				return nil
			}

			if Get(v.Merged, File) == "" {
				return errors.New("--for selects a question from -f, which was not given")
			}

			if Get(v.Merged, Replace) {
				return errors.New("--for never replaces a question, so --replace has nothing to do beside it. " +
					"Redefine a question whole with --ask ID=QUESTION --replace, or drop --replace")
			}

			for _, selection := range v.Plan.Selections {
				if _, found := fileQuestion(v.Plan, selection.ID); !found {
					return fmt.Errorf("--for %s names no question in %s, which has: %s. "+
						"Define a new question with --ask %s=QUESTION",
						selection.ID, fileNamed(v), quotedIDs(fromFile(v.Plan)), selection.ID)
				}
			}

			return nil
		},
	}
}

func fileQuestion(built *plan.Plan, id string) (int, bool) {
	for i, question := range built.Questions {
		if question.ID == id && fromFileOrBody(question) {
			return i, true
		}
	}

	return 0, false
}

func fromFileOrBody(question plan.Question) bool {
	return question.Origin == plan.OriginFile || question.Origin == plan.OriginBody
}

func fileNamed(v View) string {
	if name := Get(v.Merged, File); name != "" {
		return name
	}

	return plan.UnnamedFile
}

func fromFile(built *plan.Plan) []plan.Question {
	var questions []plan.Question

	for _, question := range built.Questions {
		if fromFileOrBody(question) {
			questions = append(questions, question)
		}
	}

	return questions
}

func bodyPolicyRule() Rule {
	return Rule{
		ID:    "body-policy-needs-one-question",
		Reads: []Name{"threshold", "min_confidence", "fallback", File.Name()},
		Check: func(v View) error {
			if v.Plan == nil {
				return nil
			}

			body := 0

			for _, question := range v.Plan.Questions {
				if question.Origin == plan.OriginBody {
					body++
				}
			}

			if body <= 1 {
				return nil
			}

			// A policy flag that found no question to bind to is still a policy flag aimed at the
			// body, since Assemble binds top level flags only when exactly one question was asked.
			for _, event := range v.Plan.Orphans {
				if plan.PolicyFlag(event.Name) {
					return bodyPolicyError(Get(v.Merged, File), body)
				}
			}

			for _, question := range v.Plan.Questions {
				policy := question.Policy
				if question.Origin == plan.OriginBody &&
					(policy.Threshold != nil || policy.MinConfidence != nil || policy.Fallback != nil) {
					return bodyPolicyError(Get(v.Merged, File), body)
				}
			}

			return nil
		},
	}
}

func bodyPolicyError(name string, questions int) error {
	if name == "" {
		name = plan.UnnamedFile
	}

	return fmt.Errorf(
		"policy flags apply to a request body only when it has one question. "+
			"%s has %d, freeze with --print-questions first", name, questions)
}

func orphansRule() Rule {
	return Rule{
		ID:    "group-flags-bind",
		Reads: []Name{"ask", "pick", "rate", "box", "desc", "sep", "threshold", "min_confidence", "fallback"},
		Check: func(v View) error {
			if v.Plan == nil || len(v.Plan.Orphans) == 0 {
				return nil
			}

			orphan := v.Plan.Orphans[0].Name
			err := fmt.Errorf("--%s given with no --ask to bind to and %d questions asked",
				orphan, len(v.Plan.Questions))

			keys := fileKeysFor(orphan)
			if keys == "" || !slices.ContainsFunc(v.Plan.Questions, func(q plan.Question) bool {
				return q.Origin == plan.OriginFile
			}) {
				return err
			}

			return fmt.Errorf("%w. Set %s on a question in the -f file instead", err, keys)
		},
	}
}

func fileKeysFor(flag string) string {
	switch flag {
	case "threshold":
		return "threshold"
	case "min-confidence":
		return "min_confidence and fallback"
	case "fallback":
		return "fallback"
	default:
		return ""
	}
}

func questionsValidRule() Rule {
	return Rule{
		ID:    "questions-valid",
		Reads: []Name{"ask", File.Name()},
		Inspect: func(v View) ([]string, error) {
			if v.Plan == nil {
				return nil, nil
			}

			return plan.Validate(v.Plan, PolicyNumbers())
		},
	}
}
