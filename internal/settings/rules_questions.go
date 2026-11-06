package settings

import (
	"errors"
	"fmt"
	"slices"

	"github.com/frodi-karlsson/onesie/internal/argv"
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
		if question.ID == id && question.Origin.FromFile() {
			return i, true
		}
	}

	return 0, false
}

func fileNamed(v View) string {
	if v.Facts.FileName != "" {
		return v.Facts.FileName
	}

	if name := Get(v.Merged, File); name != "" {
		return name
	}

	return plan.UnnamedFile
}

func fromFile(built *plan.Plan) []plan.Question {
	var questions []plan.Question

	for _, question := range built.Questions {
		if question.Origin.FromFile() {
			questions = append(questions, question)
		}
	}

	return questions
}

func fileQuestionShapeRule() Rule {
	return Rule{
		ID:    "file-question-shape",
		Reads: []Name{"for", "pick", "rate", "box", "desc", "sep", File.Name()},
		Check: func(v View) error {
			if v.Plan == nil {
				return nil
			}

			for _, question := range v.Plan.Questions {
				if !question.Origin.FromFile() {
					continue
				}

				for _, event := range typedFor(v.Plan, question.ID) {
					if err := keepsShape(v, question, event.Name); err != nil {
						return err
					}
				}
			}

			return nil
		},
	}
}

func keepsShape(v View, question plan.Question, flag string) error {
	if plan.PolicyFlag(flag) {
		return nil
	}

	if question.Origin == plan.OriginBody {
		return fmt.Errorf("--%s cannot reshape a request body's question. A body carries its own type and criteria", flag)
	}

	shapes := map[string]plan.Shape{"pick": plan.Pick, "rate": plan.Rate, "box": plan.Box}

	shape, shaping := shapes[flag]

	switch {
	case !shaping || (shape == question.Shape && shape != plan.Box):
		return nil
	case shape == question.Shape:
		return fmt.Errorf("--box does not apply to '%s', a box question from %s already. Drop --box",
			question.ID, fileNamed(v))
	}

	return fmt.Errorf("--%s cannot make '%s' a %s question, since a flag never changes the shape of a question "+
		"from %s. Redefine it whole with --ask %s=QUESTION --replace",
		flag, question.ID, shapeNoun(shape), fileNamed(v), question.ID)
}

func shapeNoun(shape plan.Shape) string {
	switch shape {
	case plan.Pick:
		return "choice"
	case plan.Rate:
		return "score"
	default:
		return shape.String()
	}
}

func policyBindingRule() Rule {
	return Rule{
		ID:    "policy-binding",
		Reads: []Name{"ask", "pick", "rate", "box", "desc", "sep", "threshold", "min_confidence", "fallback"},
		Check: func(v View) error {
			if v.Plan == nil || len(v.Plan.Orphans) == 0 || len(v.Plan.Questions) == 1 {
				return nil
			}

			orphan, asked := v.Plan.Orphans[0], v.Plan.Questions

			filed := fromFile(v.Plan)
			if len(filed) == 0 {
				return fmt.Errorf("--%s given with no --ask to bind to and %d questions asked", orphan.Name, len(asked))
			}

			taker := slices.IndexFunc(filed, func(question plan.Question) bool { return takes(question, orphan.Name) })
			if taker < 0 {
				return fmt.Errorf("--%s applies to one question, and %d were asked: %s. No question from the file "+
					"takes it, so put it after the --ask it belongs to, as in --ask ID=QUESTION %s",
					orphan.Name, len(asked), quotedIDs(asked), typedFlag(orphan))
			}

			return fmt.Errorf("--%s applies to one question, and %d were asked: %s. "+
				"Put it after --for ID to set one, as in --for %s %s",
				orphan.Name, len(asked), quotedIDs(asked), filed[taker].ID, typedFlag(orphan))
		},
	}
}

func takes(question plan.Question, flag string) bool {
	choice := question.Shape == plan.Pick || question.Shape == plan.Rate
	filed := question.Origin == plan.OriginFile

	switch flag {
	case "pick":
		return filed && question.Shape == plan.Pick
	case "rate":
		return filed && question.Shape == plan.Rate
	case "desc":
		return filed && question.Shape != plan.Box
	case "sep":
		return filed && choice
	case "threshold":
		return question.Shape == plan.Noul
	case "min-confidence":
		return choice
	case "fallback":
		return question.Shape != plan.Box
	default:
		return false
	}
}

func typedFlag(event argv.Event) string {
	if event.Name == "box" {
		return "--box"
	}

	return "--" + event.Name + " " + event.Value
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
