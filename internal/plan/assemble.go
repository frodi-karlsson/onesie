package plan

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/argv"
)

// PositionalID is the reserved id a bare QUESTION argument is keyed under.
const PositionalID = "answer"

const defaultSeparator = ","

const unnamedFile = "the question file"

// Assemble folds a recorded command line and any file questions into a Plan.
func Assemble(src Source) (*Plan, error) {
	groups, orphans := split(src.Events, src.Positional, len(src.File) > 0)

	built := &Plan{Questions: append([]Question(nil), src.File...)}
	fromFile := positions(src.File)

	for _, g := range groups {
		question, err := build(g, src.ReadFile)
		if err != nil {
			return nil, err
		}

		if err := merge(built, fromFile, question, src.Replace, src.FileName); err != nil {
			return nil, err
		}
	}

	// Top level flags bind when exactly one question was asked from any source. split already
	// handles a lone --ask group, so this covers the case where the single question came from a
	// file. With any other count they have nothing to bind to and validation reports them.
	if len(orphans) > 0 && len(built.Questions) == 1 {
		if built.Questions[0].Origin == OriginBody {
			for _, event := range orphans {
				if !policyFlag(event.Name) {
					return nil, fmt.Errorf(
						"onesie: --%s cannot reshape a request body's question. "+
							"A body carries its own type and criteria", event.Name)
				}
			}
		}

		if err := applyEvents(&built.Questions[0], orphans, src.ReadFile); err != nil {
			return nil, err
		}

		orphans = nil
	}

	built.Orphans = orphans

	return built, nil
}

// Source is where a plan's questions come from.
type Source struct {
	Events []argv.Event
	// Positional is the bare QUESTION argument, or the empty string.
	Positional string
	File       []Question
	// FileName names the file in a collision message, since a user with several files needs to
	// know which one defined the id.
	FileName string
	Replace  bool
	// ReadFile resolves @FILE references, injected so tests need nothing on disk.
	ReadFile func(string) ([]byte, error)
}

func split(events []argv.Event, positional string, hasFile bool) ([]group, []argv.Event) {
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

	// A lone --ask absorbs the top level flags only when it is the only question. With a file
	// also supplying questions, Assemble decides, since it is the only place that knows the total.
	if len(groups) == 1 && !hasFile {
		groups[0].events = append(leading, groups[0].events...)

		return groups, nil
	}

	return groups, leading
}

func build(g group, readFile func(string) ([]byte, error)) (Question, error) {
	question := Question{ID: g.id, Instructions: g.text, Origin: OriginPositional}

	if g.asked {
		id, text, ok := strings.Cut(g.ask, "=")
		if !ok || id == "" {
			return question, fmt.Errorf("onesie: --ask takes NAME=QUESTION, got '%s'", g.ask)
		}

		resolved, err := resolve(text, readFile)
		if err != nil {
			return question, err
		}

		question.ID = id
		question.Instructions = resolved
		question.Origin = OriginFlag
	}

	if err := applyEvents(&question, g.events, readFile); err != nil {
		return question, err
	}

	return question, nil
}

func merge(built *Plan, fromFile map[string]int, question Question, replace bool, name string) error {
	at, defined := fromFile[question.ID]
	if !defined {
		// Two --ask flags sharing an id are not a cross source collision. They append, and
		// validation reports the duplicate with the message it owns.
		built.Questions = append(built.Questions, question)

		return nil
	}

	if !replace {
		if name == "" {
			name = unnamedFile
		}

		return fmt.Errorf(
			"onesie: '%s' is defined in %s and by --ask. Pass --replace to override",
			question.ID, name)
	}

	// The replacement keeps the file's position, so output order is stable whether or not a
	// question was overridden.
	built.Questions[at] = question
	delete(fromFile, question.ID)

	return nil
}

func positions(questions []Question) map[string]int {
	byID := make(map[string]int, len(questions))
	for i, question := range questions {
		byID[question.ID] = i
	}

	return byID
}

func applyEvents(question *Question, events []argv.Event, readFile func(string) ([]byte, error)) error {
	separator := defaultSeparator
	descriptions := map[string]any{}

	var descOrder []string

	for _, event := range events {
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
			// Labelled is set here rather than from the final shape. Assemble applies top level
			// flags in a second pass, and a request body's score question carries criteria by
			// index with no labels, so keying off the shape would relabel it with empty strings.
			question.Labelled = true
			for _, label := range strings.Split(event.Value, separator) {
				question.Levels = append(question.Levels, Level{Label: label})
			}
		case "desc":
			key, text, ok := strings.Cut(event.Value, "=")
			if !ok {
				return fmt.Errorf("onesie: --desc takes KEY=TEXT, got '%s'", event.Value)
			}

			resolved, err := resolve(text, readFile)
			if err != nil {
				return err
			}

			if _, seen := descriptions[key]; !seen {
				descOrder = append(descOrder, key)
			}

			descriptions[key] = resolved
		default:
			if err := applyPolicy(question, event); err != nil {
				return err
			}
		}
	}

	question.DescOrder = append(question.DescOrder, descOrder...)
	attach(question, descriptions)

	// The shape is only final once the loop ends, and a yes/no fallback has to be a boolean by
	// the time anything reads it, whether or not validation ran. Unparseable text is reported by
	// checkPolicy, which owns the user facing message.
	if question.Shape == Noul && question.Policy.Fallback != nil {
		if value, ok := ParseFallback(question.Policy.Fallback.Text); ok {
			question.Policy.Fallback.Boolean = value
		}
	}

	return nil
}

func applyPolicy(question *Question, event argv.Event) error {
	// Only the text is parsed here. Ranges are checked during validation, so that every user
	// facing message lives in one file.
	switch event.Name {
	case "threshold":
		value, err := strconv.ParseFloat(event.Value, 64)
		if err != nil {
			return fmt.Errorf("onesie: --threshold takes a number, got '%s'", event.Value)
		}

		question.Policy.Threshold = &value
	case "min-confidence":
		value, err := strconv.ParseFloat(event.Value, 64)
		if err != nil {
			return fmt.Errorf("onesie: --min-confidence takes a number, got '%s'", event.Value)
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
			// Merge rather than replace. A file may already have set one half through yes_means
			// or no_means, and a --desc naming only the other half must not erase it.
			if question.Criteria == nil {
				question.Criteria = &YesNoCriteria{}
			}

			if hasYes {
				question.Criteria.Yes = yes
			}

			if hasNo {
				question.Criteria.No = no
			}

			delete(descriptions, "yes")
			delete(descriptions, "no")
		}
	}

	// Whatever matched nothing stays behind, so validation can report it against the question's
	// own vocabulary. A file question can already carry keys, so the leftovers merge in.
	if question.UnknownDesc == nil {
		question.UnknownDesc = descriptions

		return
	}

	for key, desc := range descriptions {
		question.UnknownDesc[key] = desc
	}
}

func resolve(text string, readFile func(string) ([]byte, error)) (string, error) {
	if strings.HasPrefix(text, "@@") {
		return text[1:], nil
	}

	if !strings.HasPrefix(text, "@") {
		return text, nil
	}

	body, err := readFile(text[1:])
	if err != nil {
		return "", fmt.Errorf("onesie: reading %s: %w", text[1:], err)
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
