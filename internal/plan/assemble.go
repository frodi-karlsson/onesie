package plan

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/patherr"
)

// PositionalID is the reserved id a bare QUESTION argument is keyed under.
const PositionalID = "answer"

const defaultSeparator = ","

// UnnamedFile names a question file in a message when no -f value names it.
const UnnamedFile = "the question file"

// ErrNoNumbers is the refusal of a Source or a Validate call whose Numbers the caller left unset.
var ErrNoNumbers = errors.New("plan: Numbers is unset. Pass what settings.PolicyNumbers returns")

// Assemble folds a recorded command line and any file questions into a Plan. src.Numbers is
// required.
func Assemble(src Source) (*Plan, error) {
	if !src.Numbers.complete() {
		return nil, ErrNoNumbers
	}

	groups, selections, orphans := split(src.Events, src.Positional, len(src.File) > 0)

	built := &Plan{Questions: append([]Question(nil), src.File...), Selections: selections}
	fromFile := positions(src.File)

	for _, g := range groups {
		question, err := build(g, src)
		if err != nil {
			return nil, err
		}

		if err := merge(built, fromFile, question, src.Replace, src.FileName); err != nil {
			return nil, err
		}
	}

	built.Orphans = orphans

	switch {
	case len(orphans) == 0 || len(built.Questions) != 1:
	case !built.Questions[0].Origin.FromFile():
		// A question --ask defines takes the top level flags as its own, as a lone --ask with no file
		// does in split. A file question takes them in settings.MergeQuestions, with a notice.
		if err := applyEvents(&built.Questions[0], orphans, src); err != nil {
			return nil, err
		}

		built.Orphans = nil
	default:
		prepared, err := prepare(orphans, src)
		if err != nil {
			return nil, err
		}

		built.Orphans = prepared
	}

	for i, selection := range built.Selections {
		prepared, err := prepare(selection.Events, src)
		if err != nil {
			return nil, err
		}

		built.Selections[i].Events = prepared
	}

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
	// Numbers parse the policy numbers, and are required. settings.PolicyNumbers fills them.
	Numbers Numbers
}

// Numbers parse and bound a question's policy numbers. The settings rows' kinds fill it, and the
// zero value is refused, since plan cannot import settings to supply them.
type Numbers struct {
	Threshold, MinConfidence Number
}

func (n Numbers) complete() bool {
	return n.Threshold != nil && n.MinConfidence != nil
}

// Number reads a policy number from flag text, and bounds one a question file gave. Parse returns
// the number it read beside a range refusal.
type Number interface {
	Parse(raw string) (any, error)
	FromFile(raw any) (any, error)
}

func split(events []argv.Event, positional string, hasFile bool) ([]group, []Selection, []argv.Event) {
	var (
		groups     []group
		selections []Selection
		leading    []argv.Event
	)

	joining := &leading

	for _, event := range events {
		switch event.Name {
		case "ask":
			groups = append(groups, group{ask: event.Value, asked: true})
			joining = nil
		case "for":
			at := slices.IndexFunc(selections, func(s Selection) bool { return s.ID == event.Value })
			if at < 0 {
				selections = append(selections, Selection{ID: event.Value})
				at = len(selections) - 1
			}

			joining = &selections[at].Events
		default:
			if joining == nil {
				joining = &groups[len(groups)-1].events
			}

			*joining = append(*joining, event)
		}
	}

	if positional != "" {
		return append([]group{{id: PositionalID, text: positional, events: leading}}, groups...), selections, nil
	}

	// A lone --ask absorbs the top level flags only when it is the only question. With a file
	// also supplying questions, Assemble decides, since it is the only place that knows the total.
	if len(groups) == 1 && !hasFile {
		groups[0].events = append(leading, groups[0].events...)

		return groups, selections, nil
	}

	return groups, selections, leading
}

func build(g group, src Source) (Question, error) {
	question := Question{ID: g.id, Instructions: g.text, Origin: OriginPositional}

	if g.asked {
		id, text, ok := strings.Cut(g.ask, "=")
		if !ok || id == "" {
			return question, fmt.Errorf("--ask takes NAME=QUESTION, got '%s'", g.ask)
		}

		resolved, err := resolve(text, src.ReadFile)
		if err != nil {
			return question, err
		}

		question.ID = id
		question.Instructions = resolved
		question.Origin = OriginFlag
	}

	if err := applyEvents(&question, g.events, src); err != nil {
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
			name = UnnamedFile
		}

		source := "--ask"
		if question.Origin == OriginPositional {
			source = "the question argument"
		}

		return fmt.Errorf(
			"'%s' is defined in %s and by %s. Pass --replace to override",
			question.ID, name, source)
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

func applyEvents(question *Question, events []argv.Event, src Source) error {
	prepared, err := prepare(events, src)
	if err != nil {
		return err
	}

	apply(question, prepared, src.Numbers)

	return nil
}

// Refine applies the flags Assemble prepared for a question from a file or a body. It replaces options
// or levels within the question's shape, and leaves a flag it cannot apply to validation.
func Refine(question *Question, events []argv.Event, numbers Numbers) {
	kept := slices.DeleteFunc(slices.Clone(events), func(event argv.Event) bool {
		return !refines(*question, event.Name)
	})

	if slices.ContainsFunc(kept, named("pick")) {
		question.Options = nil
	}

	if slices.ContainsFunc(kept, named("rate")) {
		question.Levels = nil
	}

	for _, event := range kept {
		if flag := "--" + event.Name; event.Name != "sep" && !slices.Contains(question.Typed, flag) {
			question.Typed = append(question.Typed, flag)
		}
	}

	apply(question, kept, numbers)
}

func refines(question Question, flag string) bool {
	if PolicyFlag(flag) {
		return true
	}

	if question.Origin != OriginFile {
		return false
	}

	switch flag {
	case "pick":
		return question.Shape == Pick
	case "rate":
		return question.Shape == Rate
	case "desc", "sep":
		return true
	default:
		return false
	}
}

func named(flag string) func(argv.Event) bool {
	return func(event argv.Event) bool { return event.Name == flag }
}

func prepare(events []argv.Event, src Source) ([]argv.Event, error) {
	prepared := make([]argv.Event, 0, len(events))

	for _, event := range events {
		switch event.Name {
		case "box":
			if event.Value != "true" {
				return nil, fmt.Errorf("--box takes no value, got '%s'", event.Value)
			}
		case "desc":
			key, text, ok := strings.Cut(event.Value, "=")
			if !ok {
				return nil, fmt.Errorf("--desc takes KEY=TEXT, got '%s'", event.Value)
			}

			resolved, err := resolve(text, src.ReadFile)
			if err != nil {
				return nil, err
			}

			event.Value = key + "=" + resolved
		case "threshold", "min-confidence":
			number := src.Numbers.Threshold
			if event.Name == "min-confidence" {
				number = src.Numbers.MinConfidence
			}

			if _, isNumber := policyNumber(number, event.Value); !isNumber {
				return nil, fmt.Errorf("--%s takes a number, got '%s'", event.Name, event.Value)
			}
		}

		prepared = append(prepared, event)
	}

	return prepared, nil
}

func apply(question *Question, events []argv.Event, numbers Numbers) {
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
				question.Options = append(question.Options, Option{Name: strings.TrimSpace(name)})
			}
		case "rate":
			question.Shape = Rate
			// Labelled is set here rather than from the final shape, since a request body's score
			// question carries criteria by index with no labels, so keying off the shape would relabel
			// it with empty strings.
			question.Labelled = true
			for _, label := range strings.Split(event.Value, separator) {
				question.Levels = append(question.Levels, Level{Label: strings.TrimSpace(label)})
			}
		case "box":
			question.Boxed = true
		case "desc":
			key, text, _ := strings.Cut(event.Value, "=")
			key = strings.TrimSpace(key)

			if _, seen := descriptions[key]; !seen {
				descOrder = append(descOrder, key)
			}

			descriptions[key] = text
		default:
			applyPolicy(question, event, numbers)
		}
	}

	if question.Boxed {
		question.Shape = Box
	}

	question.DescOrder = append(question.DescOrder, descOrder...)
	attach(question, descriptions)

	// The shape is only final once the loop ends, and a yes or no fallback has to be a boolean by
	// the time anything reads it, whether or not validation ran. Unparseable text is reported by
	// checkPolicy, which owns the user facing message.
	if question.Shape == Noul && question.Policy.Fallback != nil {
		if value, ok := ParseFallback(question.Policy.Fallback.Text); ok {
			question.Policy.Fallback.Boolean = value
		}
	}
}

func applyPolicy(question *Question, event argv.Event, numbers Numbers) {
	switch event.Name {
	case "threshold":
		if value, isNumber := policyNumber(numbers.Threshold, event.Value); isNumber {
			question.Policy.Threshold = &value
		}
	case "min-confidence":
		if value, isNumber := policyNumber(numbers.MinConfidence, event.Value); isNumber {
			question.Policy.MinConfidence = &value
		}
	case "fallback":
		question.Policy.Fallback = &Fallback{Text: event.Value}
	}
}

func policyNumber(number Number, raw string) (float64, bool) {
	parsed, err := number.Parse(raw)

	// A number out of range is kept, since validation words the range for flag and file values
	// alike, after the shape checks that come first.
	value, isNumber := parsed.(float64)
	if err != nil && !isNumber {
		return 0, false
	}

	return value, true
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
		return "", fmt.Errorf("reading %s: %w", text[1:], patherr.Inner(err))
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
