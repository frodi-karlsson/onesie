package plan

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/jev-cli/internal/argv"
)

// PositionalID is the reserved id a bare QUESTION argument is keyed under.
const PositionalID = "answer"

const defaultSeparator = ","

// Assemble folds a recorded command line into a Plan. positional is the bare QUESTION argument or
// the empty string. readFile resolves @FILE references, injected so tests need nothing on disk.
func Assemble(
	events []argv.Event,
	positional string,
	readFile func(string) ([]byte, error),
) (*Plan, error) {
	groups, orphans := split(events, positional)

	built := &Plan{Orphans: orphans}

	for _, g := range groups {
		question, err := build(g, readFile)
		if err != nil {
			return nil, err
		}

		built.Questions = append(built.Questions, question)
	}

	return built, nil
}

func split(events []argv.Event, positional string) ([]group, []argv.Event) {
	var (
		groups  []group
		leading []argv.Event
	)

	for _, event := range events {
		if event.Name != "ask" {
			if len(groups) == 0 {
				leading = append(leading, event)
			} else {
				groups[len(groups)-1].events = append(groups[len(groups)-1].events, event)
			}

			continue
		}

		groups = append(groups, group{ask: event.Value, asked: true})
	}

	if positional != "" {
		return append([]group{{id: PositionalID, text: positional, events: leading}}, groups...), nil
	}

	// A lone --ask absorbs the top level flags. With any other count they have nothing to bind to,
	// so they are returned for validation to report.
	if len(groups) == 1 {
		groups[0].events = append(leading, groups[0].events...)

		return groups, nil
	}

	return groups, leading
}

func build(g group, readFile func(string) ([]byte, error)) (Question, error) {
	question := Question{ID: g.id, Instructions: g.text}

	if g.asked {
		id, text, ok := strings.Cut(g.ask, "=")
		if !ok || id == "" {
			return question, fmt.Errorf("jev: --ask takes NAME=QUESTION, got '%s'", g.ask)
		}

		resolved, err := resolve(text, readFile)
		if err != nil {
			return question, err
		}

		question.ID = id
		question.Instructions = resolved
		question.Named = true
	}

	separator := defaultSeparator
	descriptions := map[string]any{}

	var descOrder []string

	for _, event := range g.events {
		switch event.Name {
		case "sep":
			separator = event.Value
		case "pick":
			question.Shape = Pick
			for _, name := range strings.Split(event.Value, separator) {
				question.Options = append(question.Options, Option{Name: name})
			}
		case "rate":
			question.Shape = Rate
			for _, label := range strings.Split(event.Value, separator) {
				question.Levels = append(question.Levels, Level{Label: label})
			}
		case "desc":
			key, text, ok := strings.Cut(event.Value, "=")
			if !ok {
				return question, fmt.Errorf("jev: --desc takes KEY=TEXT, got '%s'", event.Value)
			}

			resolved, err := resolve(text, readFile)
			if err != nil {
				return question, err
			}

			if _, seen := descriptions[key]; !seen {
				descOrder = append(descOrder, key)
			}

			descriptions[key] = resolved
		default:
			if err := applyPolicy(&question, event); err != nil {
				return question, err
			}
		}
	}

	question.DescOrder = descOrder
	attach(&question, descriptions)

	if question.Shape == Rate {
		question.Labelled = true
	}

	// The shape is only final once the loop ends, and a yes/no fallback has to be a boolean by
	// the time anything reads it, whether or not validation ran. Unparseable text is reported by
	// checkPolicy, which owns the user facing message.
	if question.Shape == Noul && question.Policy.Fallback != nil {
		if value, ok := ParseFallback(question.Policy.Fallback.Text); ok {
			question.Policy.Fallback.Boolean = value
		}
	}

	return question, nil
}

func applyPolicy(question *Question, event argv.Event) error {
	// Only the text is parsed here. Ranges are checked during validation, so that every user
	// facing message lives in one file.
	switch event.Name {
	case "threshold":
		value, err := strconv.ParseFloat(event.Value, 64)
		if err != nil {
			return fmt.Errorf("jev: --threshold takes a number, got '%s'", event.Value)
		}

		question.Policy.Threshold = &value
	case "min-confidence":
		value, err := strconv.ParseFloat(event.Value, 64)
		if err != nil {
			return fmt.Errorf("jev: --min-confidence takes a number, got '%s'", event.Value)
		}

		question.Policy.MinConfidence = &value
	case "fallback":
		question.Policy.Fallback = &Fallback{Text: event.Value}
	}

	return nil
}

func attach(question *Question, descriptions map[string]any) {
	switch question.Shape {
	case Pick:
		for i, option := range question.Options {
			if desc, ok := descriptions[option.Name]; ok {
				question.Options[i].Desc = desc
				delete(descriptions, option.Name)
			}
		}
	case Rate:
		for i, level := range question.Levels {
			if desc, ok := descriptions[level.Label]; ok {
				question.Levels[i].Desc = desc
				delete(descriptions, level.Label)
			}
		}
	case Noul:
		yes, hasYes := descriptions["yes"]
		no, hasNo := descriptions["no"]

		if hasYes || hasNo {
			question.Criteria = &YesNoCriteria{Yes: yes, No: no}
			delete(descriptions, "yes")
			delete(descriptions, "no")
		}
	}

	// Whatever matched nothing stays behind, so validation can report it against the question's
	// own vocabulary.
	question.UnknownDesc = descriptions
}

func resolve(text string, readFile func(string) ([]byte, error)) (string, error) {
	// @@ escapes a literal leading at sign.
	if strings.HasPrefix(text, "@@") {
		return text[1:], nil
	}

	if !strings.HasPrefix(text, "@") {
		return text, nil
	}

	body, err := readFile(text[1:])
	if err != nil {
		return "", fmt.Errorf("jev: reading %s: %w", text[1:], err)
	}

	// One trailing newline goes, so an editor's trailing newline does not become part of the text.
	return strings.TrimSuffix(string(body), "\n"), nil
}

type group struct {
	id     string
	text   string
	ask    string
	asked  bool
	events []argv.Event
}
