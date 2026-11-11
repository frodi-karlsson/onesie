package qfile

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"

	"github.com/goccy/go-yaml"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

var bodyKeys = []string{"state", "model", "questions", "long_context", "assets"}

func loadBody(top yaml.MapSlice) (*File, error) {
	file := &File{IsBody: true}

	if err := checkBodyKeys(top); err != nil {
		return nil, err
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
		// Present and null is not absent. Validation refuses a null state, and folding the two
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
		assets, sums, err := readAssets(value)
		if err != nil {
			return nil, err
		}

		file.Assets, file.AssetSums = assets, sums
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

func checkBodyKeys(top yaml.MapSlice) error {
	// A body is the wire format, and the API has no assertion, so a key here would promise a gate
	// the request cannot carry.
	for _, gate := range []struct{ key, flag string }{
		{key: "assert", flag: "--assert"},
		{key: "abstain_if", flag: "--abstain-if"},
	} {
		if _, found := lookup(top, gate.key); found {
			return fmt.Errorf(
				"a request body carries no '%s'. Pass %s on the command line",
				gate.key, gate.flag)
		}
	}

	for _, item := range top {
		name := fmt.Sprintf("%v", item.Key)

		if name == "$schema" {
			if _, err := readGate(name, item.Value); err != nil {
				return err
			}

			continue
		}

		// A key this reader skips never reaches the request, so a misspelling would vanish.
		if !slices.Contains(bodyKeys, name) {
			return fmt.Errorf("a request body has an unknown key '%s'. A request body takes "+
				"state, model, questions, long_context and assets", name)
		}
	}

	return nil
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

	var read onesie.Questions

	if err := read.UnmarshalJSON(encoded); err != nil {
		return nil, reasonOf(err)
	}

	return read, nil
}

func readAssets(value any) (onesie.Assets, map[string][sha256.Size]byte, error) {
	entries, ok := mapping(value)
	if !ok {
		return nil, nil, errors.New("'assets' in a request body must be a mapping")
	}

	assets := make(onesie.Assets, len(entries))
	sums := make(map[string][sha256.Size]byte, len(entries))

	for _, entry := range entries {
		ref := fmt.Sprintf("%v", entry.Key)

		fields, ok := mapping(entry.Value)
		if !ok {
			return nil, nil, fmt.Errorf("asset '%s' in a request body must be a mapping", ref)
		}

		encoded, _ := lookup(fields, "data_base64")

		text, isText := encoded.(string)

		data, err := base64.StdEncoding.DecodeString(text)
		if !isText || err != nil {
			return nil, nil, fmt.Errorf(
				"asset '%s' in a request body needs data_base64 as standard base64", ref)
		}

		// The bytes say what the image is, so a format the body declares is not read.
		img, err := onesie.NewImage(data)
		if err != nil {
			return nil, nil, fmt.Errorf("asset '%s' in a request body: %w", ref, reasonOf(err))
		}

		assets[ref], sums[ref] = img, sha256.Sum256(data)
	}

	return assets, sums, nil
}

func reasonOf(err error) error {
	var refused *onesie.ValidationError
	if errors.As(err, &refused) {
		return errors.New(refused.Reason())
	}

	return err
}

func buildBodyQuestion(named onesie.NamedQuestion, fields yaml.MapSlice) (plan.Question, error) {
	question := plan.Question{ID: named.ID, Origin: plan.OriginBody}

	switch typed := named.Question.(type) {
	case onesie.Noul:
		question.Instructions, question.Criteria = typed.Instructions, bodyRubric(typed.Criteria, fields)
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
		question.Instructions, question.Shape, question.Boxed = typed.Instructions, plan.Box, true
	}

	return question, checkBodyInstructions(named.ID, fields)
}

func checkBodyInstructions(id string, fields yaml.MapSlice) error {
	if _, found := lookup(fields, "instructions"); found {
		return nil
	}

	return fmt.Errorf("question '%s' in a request body has no 'instructions'", id)
}

func bodyRubric(rubric *onesie.NoulCriteria, fields yaml.MapSlice) *plan.YesNoCriteria {
	criteria, _ := lookup(fields, "criteria")
	items, _ := mapping(criteria)

	// A rubric that names neither key replays as a bare question, as an absent or null one does.
	_, hasYes := lookup(items, "true")
	_, hasNo := lookup(items, "false")

	if rubric == nil || (!hasYes && !hasNo) {
		return nil
	}

	return &plan.YesNoCriteria{Yes: rubric.True, No: rubric.False}
}
