package qfile

import (
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/goccy/go-yaml"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func loadBody(top yaml.MapSlice) (*File, error) {
	file := &File{IsBody: true}

	// A body is the wire format, and the API has no assertion, so a key here would promise a gate
	// the request cannot carry.
	for _, gate := range []struct{ key, flag string }{
		{key: "assert", flag: "--assert"},
		{key: "abstain_if", flag: "--abstain-if"},
	} {
		if _, found := lookup(top, gate.key); found {
			return nil, fmt.Errorf(
				"a request body carries no '%s'. Pass %s on the command line",
				gate.key, gate.flag)
		}
	}

	if value, found := lookup(top, "model"); found {
		name, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf(
				"'model' in a request body must be a string, got %s", describe(value))
		}

		file.Model = name
	}

	if value, found := lookup(top, "long_context"); found {
		on, isBool := value.(bool)
		if !isBool {
			return nil, errors.New("'long_context' in a request body must be true or false")
		}

		file.LongContext = on
	}

	if value, found := lookup(top, "state"); found {
		// Present and null is not absent. Validation rejects a null state, and folding the two
		// together would hide the mistake.
		file.State = plain(value)
		file.HasState = true

		// The ordered bytes are what reaches the wire. A request body is the wire format and is
		// meant to be replayed as it was written, so its key order is part of the content.
		wire, err := marshalOrdered(value)
		if err != nil {
			return nil, err
		}

		file.StateWire = wire
	}

	if value, found := lookup(top, "assets"); found {
		assets, err := readAssets(value)
		if err != nil {
			return nil, err
		}

		file.Assets = assets
	}

	raw, _ := lookup(top, "questions")

	questions, ok := mapping(raw)
	if !ok {
		return nil, fmt.Errorf("'questions' in a request body must be a mapping")
	}

	read, err := readQuestions(questions)
	if err != nil {
		return nil, err
	}

	for _, named := range read {
		// readQuestions proved every question is a mapping.
		value, _ := lookup(questions, named.ID)
		fields, _ := mapping(value)

		question, err := buildBodyQuestion(named, fields)
		if err != nil {
			return nil, err
		}

		file.Questions = append(file.Questions, question)
	}

	return file, nil
}

func readQuestions(questions yaml.MapSlice) (onesie.Questions, error) {
	seen := make(map[string]bool, len(questions))

	for _, item := range questions {
		// Two keys YAML tells apart, such as 0x1 and "1", send as one id, and the request would keep
		// only the last of them.
		id := fmt.Sprintf("%v", item.Key)
		if seen[id] {
			return nil, fmt.Errorf("question '%s' is given twice in a request body", id)
		}

		seen[id] = true

		if _, err := marshalOrdered(item.Value); err != nil {
			return nil, fmt.Errorf("question '%v' in a request body cannot be sent: %w", item.Key, err)
		}
	}

	encoded, err := marshalOrdered(questions)
	if err != nil {
		return nil, err
	}

	// UnmarshalJSON returns nil or a *ValidationError, so no other error can reach past this.
	var (
		read    onesie.Questions
		refused *onesie.ValidationError
	)

	if errors.As(read.UnmarshalJSON(encoded), &refused) {
		return nil, errors.New(refused.Reason())
	}

	return read, nil
}

func readAssets(value any) (onesie.Assets, error) {
	entries, ok := mapping(value)
	if !ok {
		return nil, errors.New("'assets' in a request body must be a mapping")
	}

	assets := make(onesie.Assets, len(entries))

	for _, entry := range entries {
		ref := fmt.Sprintf("%v", entry.Key)

		fields, ok := mapping(entry.Value)
		if !ok {
			return nil, fmt.Errorf("asset '%s' in a request body must be a mapping", ref)
		}

		format, _ := lookup(fields, "format")

		formatText, isFormat := format.(string)
		if !isFormat {
			return nil, fmt.Errorf("asset '%s' in a request body needs a format, png or jpg", ref)
		}

		encoded, _ := lookup(fields, "data_base64")

		text, isText := encoded.(string)

		data, err := base64.StdEncoding.DecodeString(text)
		if !isText || err != nil {
			return nil, fmt.Errorf(
				"asset '%s' in a request body needs data_base64 as standard base64", ref)
		}

		assets[ref] = onesie.Image{Format: formatText, Data: data}
	}

	return assets, nil
}

func buildBodyQuestion(named onesie.NamedQuestion, fields yaml.MapSlice) (plan.Question, error) {
	question := plan.Question{ID: named.ID, Origin: plan.OriginBody}

	switch typed := named.Question.(type) {
	case onesie.Noul:
		criteria, err := bodyRubric(named.ID, typed.Criteria, fields)
		if err != nil {
			return question, err
		}

		question.Instructions, question.Criteria = typed.Instructions, criteria
	case onesie.Choice:
		question.Instructions, question.Shape = typed.Instructions, plan.Pick

		for _, option := range typed.Criteria {
			question.Options = append(question.Options, plan.Option{Name: option.Name, Desc: option.Desc})
		}
	case onesie.Score:
		question.Instructions, question.Shape = typed.Instructions, plan.Rate

		// Labelled stays false and Label stays empty. A body's criteria is a bare array with no
		// names, which is the case the index keyed normalization exists for.
		for _, level := range typed.Criteria {
			question.Levels = append(question.Levels, plan.Level{Desc: level})
		}
	case onesie.Box:
		if _, found := lookup(fields, "criteria"); found {
			return question, fmt.Errorf("box question '%s' in a request body takes no criteria", named.ID)
		}

		question.Instructions, question.Shape, question.Boxed = typed.Instructions, plan.Box, true
	}

	return question, checkBodyInstructions(named.ID, fields)
}

func checkBodyInstructions(id string, fields yaml.MapSlice) error {
	if _, found := lookup(fields, "instructions"); found {
		return nil
	}

	// A question file names its question ask, so a body carrying one was written as the wrong
	// format, and the ask would otherwise be dropped in silence.
	if _, found := lookup(fields, "ask"); found {
		return fmt.Errorf("question '%s' in a request body has 'ask', but a request body "+
			"names its question 'instructions'", id)
	}

	return fmt.Errorf("question '%s' in a request body has no 'instructions'", id)
}

func bodyRubric(id string, rubric *onesie.NoulCriteria, fields yaml.MapSlice) (*plan.YesNoCriteria, error) {
	criteria, _ := lookup(fields, "criteria")
	items, _ := mapping(criteria)

	if err := checkNoulCriteriaKeys(id, items); err != nil {
		return nil, err
	}

	// A rubric that names neither key replays as a bare question, as an absent or null one does.
	_, hasYes := lookup(items, "true")
	_, hasNo := lookup(items, "false")

	if rubric == nil || (!hasYes && !hasNo) {
		return nil, nil
	}

	return &plan.YesNoCriteria{Yes: rubric.True, No: rubric.False}, nil
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
			"question '%s' in a request body has an unknown criteria key '%s'", id, name)
	}

	return nil
}
