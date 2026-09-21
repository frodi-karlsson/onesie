package qfile

import (
	"fmt"

	"github.com/goccy/go-yaml"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

func loadQuestions(top yaml.MapSlice) (*File, error) {
	file := &File{}

	for _, item := range top {
		id := fmt.Sprintf("%v", item.Key)

		question, err := buildQuestion(id, item.Value)
		if err != nil {
			return nil, err
		}

		file.Questions = append(file.Questions, question)
	}

	return file, nil
}

func buildQuestion(id string, value any) (plan.Question, error) {
	// Named gates the reserved id check, and a file id is chosen by the user just as an --ask id is.
	question := plan.Question{ID: id, Named: true}

	if text, ok := value.(string); ok {
		question.Instructions = text

		return question, nil
	}

	fields, ok := mapping(value)
	if !ok {
		return question, fmt.Errorf(
			"jev: question '%s' must be a string or a mapping", id)
	}

	ask, found := lookup(fields, "ask")
	if !found {
		return question, fmt.Errorf("jev: question '%s' has no 'ask'", id)
	}

	question.Instructions = plain(ask)

	yes, hasYes := firstOf(fields, "yes_means", "true")
	no, hasNo := firstOf(fields, "no_means", "false")

	if hasYes || hasNo {
		question.Criteria = &plan.YesNoCriteria{Yes: plain(yes), No: plain(no)}
	}

	return question, nil
}

// firstOf returns the first name present, so the documented _means spelling wins over the true and
// false aliases when a file carries both.
func firstOf(fields yaml.MapSlice, names ...string) (any, bool) {
	for _, name := range names {
		if value, ok := lookup(fields, name); ok {
			return value, true
		}
	}

	return nil, false
}
