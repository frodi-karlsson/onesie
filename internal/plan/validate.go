package plan

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/jev-cli/internal/limits"
)

// Validate checks a plan and returns the non fatal warnings plus the first fatal error, so a
// caller can print the warnings whether or not validation succeeded.
func Validate(p *Plan, cfg Config) ([]string, error) {
	if len(p.Questions) == 0 {
		return nil, errors.New("jev: no question given. Pass a question, --ask, or -f")
	}

	if cfg.Raw && cfg.Output != "" {
		return nil, errors.New("jev: -r and -o are mutually exclusive")
	}

	if cfg.HasState && cfg.HasStateFile {
		return nil, errors.New("jev: --state and --state-file are mutually exclusive")
	}

	if err := checkBodyPolicy(p, cfg); err != nil {
		return nil, err
	}

	if err := checkOrphans(p); err != nil {
		return nil, err
	}

	if err := checkSources(p); err != nil {
		return nil, err
	}

	if err := checkDuplicateIDs(p); err != nil {
		return nil, err
	}

	var warnings []string

	for i := range p.Questions {
		hint, err := checkQuestion(&p.Questions[i])
		if err != nil {
			return warnings, err
		}

		if hint != "" {
			warnings = append(warnings, hint)
		}
	}

	if err := checkSingle(p, cfg); err != nil {
		return warnings, err
	}

	return warnings, nil
}

// Config carries the invocation settings validation needs beyond the questions themselves.
type Config struct {
	Raw          bool
	Quiet        bool
	Output       string
	HasState     bool
	HasStateFile bool

	// FromBody is true when the questions came from a raw API request body, which carries no
	// labels and no policy of its own.
	FromBody bool
}

func checkBodyPolicy(p *Plan, cfg Config) error {
	if !cfg.FromBody || len(p.Questions) < 2 {
		return nil
	}

	// A top level policy flag stays an orphan with more than one question, so both the bound and
	// the unbound spelling have to be caught here, before checkOrphans reports the generic case.
	for _, event := range p.Orphans {
		if policyFlag(event.Name) {
			return bodyPolicyError(len(p.Questions))
		}
	}

	for _, question := range p.Questions {
		if question.Policy.Threshold != nil || question.Policy.MinConfidence != nil ||
			question.Policy.Fallback != nil {
			return bodyPolicyError(len(p.Questions))
		}
	}

	return nil
}

func policyFlag(name string) bool {
	switch name {
	case "threshold", "min-confidence", "fallback":
		return true
	default:
		return false
	}
}

func bodyPolicyError(questions int) error {
	return fmt.Errorf(
		"jev: policy flags apply to a request body only when it has one question. "+
			"The body has %d", questions)
}

func checkOrphans(p *Plan) error {
	if len(p.Orphans) == 0 {
		return nil
	}

	return fmt.Errorf(
		"jev: --%s given with no --ask to bind to and %d questions asked",
		p.Orphans[0].Name, len(p.Questions),
	)
}

func checkSources(p *Plan) error {
	named, positional := 0, 0

	for _, question := range p.Questions {
		if question.Named {
			named++

			continue
		}

		positional++
	}

	if named > 0 && positional > 0 {
		return errors.New(
			"jev: a positional question cannot be combined with --ask or -f")
	}

	return nil
}

func checkDuplicateIDs(p *Plan) error {
	ids := make([]string, 0, len(p.Questions))
	for _, question := range p.Questions {
		ids = append(ids, question.ID)
	}

	// The request body is keyed by id, so a repeated one would collapse into a single question
	// while the output still carries the key twice.
	if dupe := firstDuplicate(ids); dupe != "" {
		return fmt.Errorf("jev: question id '%s' is given twice", dupe)
	}

	return nil
}

func checkQuestion(q *Question) (string, error) {
	// The positional question is keyed answer by design, so the reserved list only applies to an
	// id the user chose.
	if q.Named && (reserved(q.ID) || q.ID == PositionalID) {
		return "", fmt.Errorf("jev: question id '%s' is reserved", q.ID)
	}

	if len(q.Options) > 0 && len(q.Levels) > 0 {
		return "", errors.New(
			"jev: --pick and --rate are mutually exclusive. A question is one or the other")
	}

	switch q.Shape {
	case Pick:
		return checkPick(q)
	case Rate:
		return "", checkRate(q)
	default:
		return "", checkNoul(q)
	}
}

func checkPick(q *Question) (string, error) {
	names := make([]string, 0, len(q.Options))
	for _, option := range q.Options {
		names = append(names, option.Name)
	}

	switch {
	case len(q.Options) < limits.MinChoiceOptions:
		return "", fmt.Errorf("jev: --pick needs at least two options, got %d: %s",
			len(q.Options), strings.Join(names, ", "))
	case len(q.Options) > limits.MaxChoiceOptions:
		return "", fmt.Errorf("jev: --pick takes at most %d options, got %d",
			limits.MaxChoiceOptions, len(q.Options))
	}

	if dupe := firstDuplicate(names); dupe != "" {
		return "", fmt.Errorf(
			"jev: --pick option '%s' is listed twice in question '%s'", dupe, q.ID)
	}

	if err := checkUnknownDesc(q, names, "--pick"); err != nil {
		return "", err
	}

	if err := checkPolicy(q, false); err != nil {
		return "", err
	}

	return describedHint(q, names, described(q)), nil
}

func checkRate(q *Question) error {
	labels := make([]string, 0, len(q.Levels))
	for _, level := range q.Levels {
		labels = append(labels, level.Label)
	}

	switch {
	case len(q.Levels) < limits.MinScoreLevels:
		return fmt.Errorf("jev: --rate needs at least two levels, got %d: %s",
			len(q.Levels), strings.Join(labels, ", "))
	case len(q.Levels) > limits.MaxScoreLevels:
		return fmt.Errorf("jev: --rate takes at most %d levels, got %d",
			limits.MaxScoreLevels, len(q.Levels))
	}

	if dupe := firstDuplicate(labels); dupe != "" {
		return fmt.Errorf("jev: --rate label '%s' is listed twice in question '%s'", dupe, q.ID)
	}

	if err := checkUnknownDesc(q, labels, "--rate"); err != nil {
		return err
	}

	// A mixed rubric calibrates poorly and a warning would be invisible in a pipeline, so this is
	// fatal where the equivalent for --pick is only a warning.
	have := described(q)
	if len(have) != 0 && len(have) != len(labels) {
		return fmt.Errorf(
			"jev: --rate levels must all be described or all bare. '%s' describes %s but not %s",
			q.ID, strings.Join(have, ", "), strings.Join(missing(labels, have), ", "),
		)
	}

	return checkPolicy(q, false)
}

func checkNoul(q *Question) error {
	for _, key := range q.DescOrder {
		if key != "yes" && key != "no" {
			return fmt.Errorf(
				"jev: --desc on a yes/no question takes 'yes' or 'no', got '%s'", key)
		}
	}

	return checkPolicy(q, true)
}

func checkPolicy(q *Question, yesNo bool) error {
	policy := q.Policy

	if policy.Threshold != nil {
		if !yesNo {
			noun := "options"
			if q.Shape == Rate {
				noun = "levels"
			}

			return fmt.Errorf(
				"jev: --threshold cuts a yes/no probability. '%s' has %s, "+
					"use --min-confidence with --fallback", q.ID, noun)
		}

		if *policy.Threshold < 0 || *policy.Threshold > 1 {
			return fmt.Errorf("jev: --threshold must be between 0 and 1, got %s",
				strconv.FormatFloat(*policy.Threshold, 'g', -1, 64))
		}
	}

	if policy.MinConfidence != nil {
		if yesNo {
			return fmt.Errorf(
				"jev: --min-confidence needs a confidence value. '%s' is a yes/no question, "+
					"use --threshold, or add --pick or --rate", q.ID)
		}

		if *policy.MinConfidence < 0 || *policy.MinConfidence > 1 {
			return fmt.Errorf("jev: --min-confidence must be between 0 and 1, got %s",
				strconv.FormatFloat(*policy.MinConfidence, 'g', -1, 64))
		}

		if policy.Fallback == nil {
			return fmt.Errorf(
				"jev: --min-confidence needs --fallback, nothing to substitute for '%s'", q.ID)
		}
	}

	if yesNo && policy.Fallback != nil {
		if _, ok := ParseFallback(policy.Fallback.Text); !ok {
			return fmt.Errorf(
				"jev: --fallback on a yes/no question takes true, false, yes or no, got '%s'",
				policy.Fallback.Text)
		}
	}

	return nil
}

func checkUnknownDesc(q *Question, vocabulary []string, flag string) error {
	for _, key := range q.DescOrder {
		if _, unknown := q.UnknownDesc[key]; !unknown {
			continue
		}

		return fmt.Errorf("jev: --desc names an unknown key '%s' in question '%s'. %s has: %s",
			key, q.ID, flag, strings.Join(vocabulary, ", "))
	}

	return nil
}

func checkSingle(p *Plan, cfg Config) error {
	// Both spellings of the raw mode take a single question. Only the -r spelling is exclusive
	// with -o, which is why the two are still distinguished above.
	raw := cfg.Raw || cfg.Output == "raw"

	if !raw && !cfg.Quiet {
		return nil
	}

	names := make([]string, 0, len(p.Questions))
	for _, question := range p.Questions {
		names = append(names, "'"+question.ID+"'")
	}

	flag := "-r"

	switch {
	case cfg.Quiet:
		flag = "-q"
	case !cfg.Raw:
		flag = "-o raw"
	}

	if len(p.Questions) != 1 {
		return fmt.Errorf("jev: %s needs a single question. %s were asked",
			flag, strings.Join(names, ", "))
	}

	only := p.Questions[0]
	if cfg.Quiet && only.Shape != Noul && only.Policy.MinConfidence == nil {
		return fmt.Errorf(
			"jev: -q on '%s' needs --min-confidence and --fallback. "+
				"Without a policy the exit code is always 0", only.ID)
	}

	return nil
}

func describedHint(q *Question, names, have []string) string {
	if len(have) == 0 || len(have) == len(names) {
		return ""
	}

	return fmt.Sprintf("warning: '%s' describes %s but not %s",
		q.ID, strings.Join(have, ", "), strings.Join(missing(names, have), ", "))
}

func described(q *Question) []string {
	var have []string

	for _, option := range q.Options {
		if option.Desc != nil {
			have = append(have, option.Name)
		}
	}

	for _, level := range q.Levels {
		if level.Desc != nil {
			have = append(have, level.Label)
		}
	}

	return have
}

func missing(all, have []string) []string {
	seen := make(map[string]struct{}, len(have))
	for _, name := range have {
		seen[name] = struct{}{}
	}

	var absent []string

	for _, name := range all {
		if _, ok := seen[name]; !ok {
			absent = append(absent, name)
		}
	}

	return absent
}

func firstDuplicate(names []string) string {
	seen := make(map[string]struct{}, len(names))

	for _, name := range names {
		if _, ok := seen[name]; ok {
			return name
		}

		seen[name] = struct{}{}
	}

	return ""
}

// ParseFallback reads a yes/no fallback value. The second result is false when the text is not one
// of true, yes, false or no, which validation reports.
func ParseFallback(text string) (bool, bool) {
	switch strings.ToLower(text) {
	case "true", "yes":
		return true, true
	case "false", "no":
		return false, true
	default:
		return false, false
	}
}

func reserved(id string) bool {
	if strings.HasPrefix(id, "__") {
		return true
	}

	switch id {
	case "answers", "error", "model", "usage", "questions", "state":
		return true
	default:
		return false
	}
}
