package qfile

import (
	"fmt"

	"github.com/goccy/go-yaml"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

func loadBody(top yaml.MapSlice) (*File, error) {
	file := &File{IsBody: true}

	if value, found := lookup(top, "model"); found {
		file.Model = fmt.Sprintf("%v", value)
	}

	if value, found := lookup(top, "state"); found {
		// Present and null is not absent. Validation rejects a null state, and folding the two
		// together would hide the mistake.
		file.State = plain(value)
		file.HasState = true
	}

	raw, _ := lookup(top, "questions")

	questions, ok := mapping(raw)
	if !ok {
		return nil, fmt.Errorf("jev: 'questions' in a request body must be a mapping")
	}

	for _, item := range questions {
		id := fmt.Sprintf("%v", item.Key)

		question, err := buildBodyQuestion(id, item.Value)
		if err != nil {
			return nil, err
		}

		file.Questions = append(file.Questions, question)
	}

	return file, nil
}

func buildBodyQuestion(id string, value any) (plan.Question, error) {
	question := plan.Question{ID: id, Named: true, FromBody: true}

	fields, ok := mapping(value)
	if !ok {
		return question, fmt.Errorf("jev: question '%s' in a request body must be a mapping", id)
	}

	kind, found := lookup(fields, "type")
	if !found {
		return question, fmt.Errorf("jev: question '%s' in a request body has no 'type'", id)
	}

	if instructions, found := lookup(fields, "instructions"); found {
		question.Instructions = plain(instructions)
	}

	criteria, _ := lookup(fields, "criteria")

	switch fmt.Sprintf("%v", kind) {
	case "noul":
		return buildBodyNoul(question, criteria), nil
	case "choice":
		return buildBodyChoice(question, criteria)
	case "score":
		return buildBodyScore(question, criteria)
	default:
		return question, fmt.Errorf(
			"jev: question '%s' in a request body has an unknown type '%v'", id, kind)
	}
}

func buildBodyNoul(question plan.Question, criteria any) plan.Question {
	items, ok := mapping(criteria)
	if !ok {
		return question
	}

	yes, _ := lookup(items, "true")
	no, _ := lookup(items, "false")
	question.Criteria = &plan.YesNoCriteria{Yes: plain(yes), No: plain(no)}

	return question
}

func buildBodyChoice(question plan.Question, criteria any) (plan.Question, error) {
	items, ok := mapping(criteria)
	if !ok {
		return question, fmt.Errorf(
			"jev: question '%s' is a choice and needs a criteria mapping", question.ID)
	}

	question.Shape = plan.Pick

	for _, item := range items {
		question.Options = append(question.Options, plan.Option{
			Name: fmt.Sprintf("%v", item.Key),
			Desc: plain(item.Value),
		})
	}

	return question, nil
}

func buildBodyScore(question plan.Question, criteria any) (plan.Question, error) {
	entries, ok := criteria.([]any)
	if !ok {
		return question, fmt.Errorf(
			"jev: question '%s' is a score and needs a criteria sequence", question.ID)
	}

	question.Shape = plan.Rate

	for _, entry := range entries {
		// Labelled stays false and Label stays empty. A body's criteria is a bare array with no
		// names, which is the case the index keyed normalization exists for.
		question.Levels = append(question.Levels, plan.Level{Desc: plain(entry)})
	}

	return question, nil
}
