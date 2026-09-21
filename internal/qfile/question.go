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

	_, hasPick := lookup(fields, "pick")
	rateValue, hasRate := lookup(fields, "rate")

	if hasPick && hasRate {
		return question, fmt.Errorf(
			"jev: question '%s' has both 'pick' and 'rate'. A question is one or the other", id)
	}

	if value, found := lookup(fields, "pick"); found {
		options, err := readPick(id, value)
		if err != nil {
			return question, err
		}

		question.Shape = plan.Pick
		question.Options = options
		// A file giving both a yes or no rubric and pick is contradictory, and carrying the rubric
		// onto a choice question would send a criteria shape the API does not expect for the type.
		question.Criteria = nil
	}

	if hasRate {
		levels, err := readRate(id, rateValue)
		if err != nil {
			return question, err
		}

		question.Shape = plan.Rate
		question.Levels = levels
		question.Labelled = true
		question.Criteria = nil
	}

	return question, nil
}

func readPick(id string, value any) ([]plan.Option, error) {
	if items, ok := mapping(value); ok {
		options := make([]plan.Option, 0, len(items))
		for _, item := range items {
			options = append(options, plan.Option{
				Name: fmt.Sprintf("%v", item.Key),
				Desc: plain(item.Value),
			})
		}

		return options, nil
	}

	names, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf(
			"jev: 'pick' in question '%s' must be a mapping of option to description, "+
				"or a sequence of option names", id)
	}

	options := make([]plan.Option, 0, len(names))

	for _, entry := range names {
		name, ok := entry.(string)
		if !ok {
			return nil, fmt.Errorf(
				"jev: 'pick' in question '%s' has a non string option name", id)
		}

		options = append(options, plan.Option{Name: name})
	}

	return options, nil
}

func readRate(id string, value any) ([]plan.Level, error) {
	if items, ok := mapping(value); ok {
		levels := make([]plan.Level, 0, len(items))
		for _, item := range items {
			levels = append(levels, plan.Level{
				Label: fmt.Sprintf("%v", item.Key),
				Desc:  plain(item.Value),
			})
		}

		return levels, nil
	}

	entries, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf(
			"jev: 'rate' in question '%s' must be a sequence of levels or a mapping of level to "+
				"description", id)
	}

	levels := make([]plan.Level, 0, len(entries))

	for _, entry := range entries {
		level, err := readLevel(id, entry)
		if err != nil {
			return nil, err
		}

		levels = append(levels, level)
	}

	return levels, nil
}

func readLevel(id string, entry any) (plan.Level, error) {
	if label, ok := entry.(string); ok {
		// Desc stays nil so the wire conversion sends the label, which is what a bare --rate does.
		// Copying the label in would also break the all described or all bare check in validation.
		return plan.Level{Label: label}, nil
	}

	items, ok := mapping(entry)
	if !ok {
		return plan.Level{}, fmt.Errorf(
			"jev: 'rate' in question '%s' has a level that is neither a name nor a "+
				"single key mapping", id)
	}

	if len(items) != 1 {
		return plan.Level{}, fmt.Errorf(
			"jev: 'rate' in question '%s' has a sequence entry with %d keys, "+
				"each entry names one level", id, len(items))
	}

	return plan.Level{
		Label: fmt.Sprintf("%v", items[0].Key),
		Desc:  plain(items[0].Value),
	}, nil
}

func firstOf(fields yaml.MapSlice, names ...string) (any, bool) {
	for _, name := range names {
		// Names are tried in order, so the documented _means spelling wins over the true and false
		// aliases when a file carries both.
		if value, ok := lookup(fields, name); ok {
			return value, true
		}
	}

	return nil, false
}
