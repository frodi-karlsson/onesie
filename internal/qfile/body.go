package qfile

import (
	"errors"
	"fmt"

	"github.com/goccy/go-yaml"

	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

func loadBody(top yaml.MapSlice) (*File, error) {
	file := &File{IsBody: true}

	if _, found := lookup(top, "assert"); found {
		// Section 17.5. A body is the wire format, and the API has no assertion, so a key here
		// would promise a gate the request cannot carry.
		return nil, errors.New(
			"jev: a request body carries no 'assert'. Pass --assert on the command line")
	}

	if value, found := lookup(top, "model"); found {
		name, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf(
				"jev: 'model' in a request body must be a string, got %s", describe(value))
		}

		file.Model = name
	}

	if value, found := lookup(top, "state"); found {
		// Present and null is not absent. Validation rejects a null state, and folding the two
		// together would hide the mistake.
		file.State = plain(value)
		file.HasState = true

		// The ordered bytes are what reaches the wire. A request body is the wire format and is
		// meant to be replayed as it was written, so its key order is part of the content.
		wire, err := MarshalOrdered(value)
		if err != nil {
			return nil, err
		}

		file.StateWire = wire
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
	question := plan.Question{ID: id, Origin: plan.OriginBody}

	fields, ok := mapping(value)
	if !ok {
		return question, fmt.Errorf("jev: question '%s' in a request body must be a mapping", id)
	}

	kind, found := lookup(fields, "type")
	if !found {
		return question, fmt.Errorf("jev: question '%s' in a request body has no 'type'", id)
	}

	if instructions, found := lookup(fields, "instructions"); found {
		wired, err := wireValue(instructions)
		if err != nil {
			return question, fmt.Errorf(
				"jev: 'instructions' in question '%s' cannot be sent: %w", id, err)
		}

		question.Instructions = wired
	}

	criteria, hasCriteria := lookup(fields, "criteria")

	switch fmt.Sprintf("%v", kind) {
	case "noul":
		return buildBodyNoul(question, criteria, hasCriteria)
	case "choice":
		return buildBodyChoice(question, criteria)
	case "score":
		return buildBodyScore(question, criteria)
	default:
		return question, fmt.Errorf(
			"jev: question '%s' in a request body has an unknown type '%v'", id, kind)
	}
}

func buildBodyNoul(question plan.Question, criteria any, present bool) (plan.Question, error) {
	// A noul rubric is optional, so an absent key is not a mistake. An explicit null is treated the
	// same way, since the wire field is omitted when empty and a body carrying one replays as a
	// bare question.
	if !present || criteria == nil {
		return question, nil
	}

	items, ok := mapping(criteria)
	if !ok {
		return question, fmt.Errorf(
			"jev: question '%s' is a noul and needs a criteria mapping", question.ID)
	}

	if err := checkNoulCriteriaKeys(question.ID, items); err != nil {
		return question, err
	}

	yes, hasYes := lookup(items, "true")
	no, hasNo := lookup(items, "false")

	if !hasYes && !hasNo {
		return question, nil
	}

	rubric, err := readYesNo(question.ID, "true", "false", yes, no)
	if err != nil {
		return question, err
	}

	question.Criteria = rubric

	return question, nil
}

func checkNoulCriteriaKeys(id string, items yaml.MapSlice) error {
	for _, item := range items {
		// An unquoted true or false arrives as a Go bool, and %v spells both the way the wire
		// does.
		name := fmt.Sprintf("%v", item.Key)
		if name == "true" || name == "false" {
			continue
		}

		// A noul rubric is the two keys and nothing else. Dropping the rest in silence would
		// leave the user believing they reached the API.
		return fmt.Errorf(
			"jev: question '%s' in a request body has an unknown criteria key '%s'", id, name)
	}

	return nil
}

func buildBodyChoice(question plan.Question, criteria any) (plan.Question, error) {
	items, ok := mapping(criteria)
	if !ok {
		return question, fmt.Errorf(
			"jev: question '%s' is a choice and needs a criteria mapping", question.ID)
	}

	question.Shape = plan.Pick

	for _, item := range items {
		name := fmt.Sprintf("%v", item.Key)

		desc, err := wireValue(item.Value)
		if err != nil {
			return question, fmt.Errorf(
				"jev: criteria '%s' in question '%s' cannot be sent: %w", name, question.ID, err)
		}

		question.Options = append(question.Options, plan.Option{
			Name: name,
			Desc: desc,
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

	for index, entry := range entries {
		desc, err := wireValue(entry)
		if err != nil {
			return question, fmt.Errorf(
				"jev: criteria %d in question '%s' cannot be sent: %w", index, question.ID, err)
		}

		// Labelled stays false and Label stays empty. A body's criteria is a bare array with no
		// names, which is the case the index keyed normalization exists for.
		question.Levels = append(question.Levels, plan.Level{Desc: desc})
	}

	return question, nil
}
