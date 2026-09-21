package qfile

import (
	"fmt"
	"strconv"

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

	yes, yesKey, hasYes := firstOf(fields, "yes_means", "true")
	no, noKey, hasNo := firstOf(fields, "no_means", "false")

	if hasYes || hasNo {
		question.Criteria = &plan.YesNoCriteria{Yes: plain(yes), No: plain(no)}
	}

	pickValue, hasPick := lookup(fields, "pick")
	rateValue, hasRate := lookup(fields, "rate")

	if hasPick && hasRate {
		return question, fmt.Errorf(
			"jev: question '%s' has both 'pick' and 'rate'. A question is one or the other", id)
	}

	if err := checkShapeAndRubric(id, shapeKey(hasPick, hasRate), yesKey, noKey); err != nil {
		return question, err
	}

	if hasPick {
		options, err := readPick(id, pickValue)
		if err != nil {
			return question, err
		}

		question.Shape = plan.Pick
		question.Options = options
	}

	if hasRate {
		levels, err := readRate(id, rateValue)
		if err != nil {
			return question, err
		}

		question.Shape = plan.Rate
		question.Levels = levels
		question.Labelled = true
	}

	if err := readPolicy(&question, fields); err != nil {
		return question, err
	}

	if err := checkKeys(id, fields); err != nil {
		return question, err
	}

	return question, nil
}

func shapeKey(hasPick, hasRate bool) string {
	switch {
	case hasPick:
		return "pick"
	case hasRate:
		return "rate"
	default:
		return ""
	}
}

func checkShapeAndRubric(id, shape, yesKey, noKey string) error {
	if shape == "" {
		return nil
	}

	rubric := yesKey
	if rubric == "" {
		rubric = noKey
	}

	if rubric == "" {
		return nil
	}

	// Dropping the rubric silently would leave the user believing it reached the API, and a
	// question file is written once and trusted afterwards.
	return fmt.Errorf(
		"jev: question '%s' has both '%s' and '%s'. A question is one or the other",
		id, rubric, shape)
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

func readPolicy(question *plan.Question, fields yaml.MapSlice) error {
	if value, found := lookup(fields, "threshold"); found {
		number, err := readNumber(question.ID, "threshold", value)
		if err != nil {
			return err
		}

		question.Policy.Threshold = &number
	}

	if value, found := lookup(fields, "min_confidence"); found {
		number, err := readNumber(question.ID, "min_confidence", value)
		if err != nil {
			return err
		}

		question.Policy.MinConfidence = &number
	}

	if value, found := lookup(fields, "fallback"); found {
		text, err := readFallback(question.ID, value)
		if err != nil {
			return err
		}

		question.Policy.Fallback = &plan.Fallback{Text: text}

		// Resolved here rather than during validation, so nothing downstream depends on the order
		// the two ran in. Unparseable text is still reported by validation.
		if question.Shape == plan.Noul {
			if parsed, ok := plan.ParseFallback(text); ok {
				question.Policy.Fallback.Boolean = parsed
			}
		}
	}

	return nil
}

func readNumber(id, key string, value any) (float64, error) {
	// goccy yields a bare integer as uint64, a negative one as int64 and anything with a decimal
	// point or a sign on the exponent as float64.
	switch typed := value.(type) {
	case float64:
		return typed, nil
	case int64:
		return float64(typed), nil
	case uint64:
		return float64(typed), nil
	default:
		return 0, fmt.Errorf(
			"jev: '%s' in question '%s' must be a number, got '%v'", key, id, value)
	}
}

func readFallback(id string, value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case bool:
		// An unquoted 'fallback: true' reaches the loader as a Go bool, while 'fallback: yes'
		// stays a string under the YAML 1.2 core schema.
		return strconv.FormatBool(typed), nil
	default:
		return "", fmt.Errorf(
			"jev: 'fallback' in question '%s' must be a string, got '%v'", id, value)
	}
}

func checkKeys(id string, fields yaml.MapSlice) error {
	known := map[string]struct{}{
		"ask": {}, "yes_means": {}, "no_means": {}, "true": {}, "false": {},
		"pick": {}, "rate": {},
		"threshold": {}, "min_confidence": {}, "fallback": {},
	}

	for _, item := range fields {
		name := fmt.Sprintf("%v", item.Key)
		if _, ok := known[name]; !ok {
			// A silently ignored misspelling leaves the user believing a policy is in force when
			// it is not, and a question file is written once and trusted afterwards.
			return fmt.Errorf("jev: question '%s' has an unknown key '%s'", id, name)
		}
	}

	return nil
}

func firstOf(fields yaml.MapSlice, names ...string) (any, string, bool) {
	for _, name := range names {
		// Names are tried in order, so the documented _means spelling wins over the true and false
		// aliases when a file carries both.
		if value, ok := lookup(fields, name); ok {
			return value, name, true
		}
	}

	return nil, "", false
}
