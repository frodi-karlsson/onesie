package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

var reservedIDs = []string{
	"$schema", "abstain", "abstain_if", "answers", "assert", "error", "id", "model", "usage",
	"questions", "state",
}

// Validate checks the questions of a plan and returns the non fatal warnings plus the first fatal
// error. fileName is the -f value a body policy refusal names.
func Validate(p *Plan, fileName string) ([]string, error) {
	if err := checkBodyPolicy(p, fileName); err != nil {
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

	return warnings, nil
}

func checkBodyPolicy(p *Plan, name string) error {
	body := 0

	for _, question := range p.Questions {
		if question.Origin == OriginBody {
			body++
		}
	}

	if body <= 1 {
		return nil
	}

	// A policy flag that found no question to bind to is still a policy flag aimed at the body,
	// since Assemble only binds top level flags when exactly one question was asked.
	for _, event := range p.Orphans {
		if policyFlag(event.Name) {
			return bodyPolicyError(name, body)
		}
	}

	for _, question := range p.Questions {
		if question.Origin != OriginBody {
			continue
		}

		if question.Policy.Threshold != nil || question.Policy.MinConfidence != nil ||
			question.Policy.Fallback != nil {
			return bodyPolicyError(name, body)
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

func bodyPolicyError(name string, questions int) error {
	if name == "" {
		name = unnamedFile
	}

	return fmt.Errorf(
		"policy flags apply to a request body only when it has one question. "+
			"%s has %d, freeze with --print-questions first", name, questions)
}

func checkOrphans(p *Plan) error {
	if len(p.Orphans) == 0 {
		return nil
	}

	orphan := p.Orphans[0].Name
	err := fmt.Errorf("--%s given with no --ask to bind to and %d questions asked",
		orphan, len(p.Questions))

	keys := fileKeysFor(orphan)
	if keys == "" || !slices.ContainsFunc(p.Questions, func(q Question) bool { return q.Origin == OriginFile }) {
		return err
	}

	return fmt.Errorf("%w. Set %s on a question in the -f file instead", err, keys)
}

func fileKeysFor(flag string) string {
	switch flag {
	case "threshold":
		return "threshold"
	case "min-confidence":
		return "min_confidence and fallback"
	case "fallback":
		return "fallback"
	default:
		return ""
	}
}

func checkSources(p *Plan) error {
	named, positional := 0, 0

	for _, question := range p.Questions {
		if question.Origin.Chosen() {
			named++

			continue
		}

		positional++
	}

	if named > 0 && positional > 0 {
		return errors.New(
			"a positional question cannot be combined with --ask or -f")
	}

	return nil
}

// FallbackColumn names the csv and tsv column that says whether a question's fallback was used.
func FallbackColumn(id string) string {
	return id + "_fallback"
}

// BoxColumns are the four columns a box question writes under -o csv and tsv, in place of its own.
func BoxColumns(id string) []string {
	return []string{id + "_left", id + "_top", id + "_right", id + "_bottom"}
}

func checkDuplicateIDs(p *Plan) error {
	ids := make([]string, 0, len(p.Questions))
	for _, question := range p.Questions {
		ids = append(ids, question.ID)
	}

	// The request body is keyed by id, so a repeated one would collapse into a single question
	// while the output still carries the key twice.
	if dupe, found := firstDuplicate(ids); found {
		return fmt.Errorf("question id '%s' is given twice", dupe)
	}

	return nil
}

func checkQuestion(q *Question) (string, error) {
	// The positional question is keyed answer by design, so the reserved list only applies to an
	// id the user chose. A request body may carry answer, since --print-request writes a positional
	// question under it and -f reads that body back.
	if q.Origin.Chosen() && (reserved(q.ID) || (q.ID == PositionalID && q.Origin != OriginBody)) {
		return "", fmt.Errorf("question id '%s' is reserved", q.ID)
	}

	if err := checkBlankQuestion(q); err != nil {
		return "", err
	}

	if shapes := countTrue(len(q.Options) > 0, len(q.Levels) > 0, q.Boxed); shapes > 1 {
		if q.Boxed {
			return "", errors.New(
				"--box, --pick and --rate are mutually exclusive. A question is one of them")
		}

		return "", errors.New(
			"--pick and --rate are mutually exclusive. A question is one or the other")
	}

	switch q.Shape {
	case Pick:
		return checkPick(q)
	case Rate:
		return "", checkRate(q)
	case Box:
		return "", checkBox(q)
	default:
		return "", checkNoul(q)
	}
}

func checkBlankQuestion(q *Question) error {
	const cannot = "an empty question is one the model cannot answer"

	switch text := q.Instructions.(type) {
	case string:
		if strings.TrimSpace(text) != "" {
			return nil
		}
	case json.RawMessage:
		if trimmed := bytes.TrimSpace(text); !bytes.Equal(trimmed, []byte("{}")) && !bytes.Equal(trimmed, []byte("[]")) {
			return nil
		}
	case nil:
		return fmt.Errorf("question '%s' has %s, %s", q.ID, nullKey(q.Origin), cannot)
	default:
		return fmt.Errorf("question '%s' has %s, so the model has no question to answer",
			q.ID, notText(q.Origin))
	}

	switch q.Origin {
	case OriginPositional:
		return errors.New("the question is blank, " + cannot)
	case OriginFlag:
		return fmt.Errorf("--ask %s= is blank, %s", q.ID, cannot)
	case OriginFile:
		return fmt.Errorf("question '%s' has a blank 'ask', %s", q.ID, cannot)
	default:
		return fmt.Errorf("question '%s' has blank 'instructions', %s", q.ID, cannot)
	}
}

func nullKey(origin Origin) string {
	if origin == OriginBody {
		return "null 'instructions'"
	}

	return "a null 'ask'"
}

func notText(origin Origin) string {
	if origin == OriginBody {
		return "'instructions' that are not text or a mapping"
	}

	return "an 'ask' that is not text or a mapping"
}

func checkPick(q *Question) (string, error) {
	names := make([]string, 0, len(q.Options))
	for _, option := range q.Options {
		names = append(names, option.Name)
	}

	pick := ShapeName(q, "--pick")

	switch {
	case len(q.Options) < limits.MinChoiceOptions:
		return "", tooFew(q, pick, "options", len(q.Options), names)
	case len(q.Options) > limits.MaxChoiceOptions:
		return "", tooMany(q, pick, "options", limits.MaxChoiceOptions, len(q.Options))
	}

	if err := checkBlankNames(q, pick, "option", names); err != nil {
		return "", err
	}

	if dupe, found := firstDuplicate(names); found {
		return "", fmt.Errorf(
			"%s option '%s' is listed twice in question '%s'", pick, dupe, q.ID)
	}

	if err := checkUnknownDesc(q, names, pick); err != nil {
		return "", err
	}

	if err := checkPolicy(q, false); err != nil {
		return "", err
	}

	// A body's null criteria are already frozen and legal, so the partial description warning
	// names nothing the user can act on.
	if q.Origin == OriginBody {
		return "", nil
	}

	return describedHint(q, names, described(q)), nil
}

func checkRate(q *Question) error {
	var labels []string

	// A body's levels carry no labels at all, so every rule written around them is skipped and
	// nothing prints an empty list.
	if q.Labelled {
		labels = make([]string, 0, len(q.Levels))
		for _, level := range q.Levels {
			labels = append(labels, level.Label)
		}
	}

	rate := ShapeName(q, "--rate")

	switch {
	case len(q.Levels) < limits.MinScoreLevels:
		return tooFew(q, rate, "levels", len(q.Levels), labels)
	case len(q.Levels) > limits.MaxScoreLevels:
		return tooMany(q, rate, "levels", limits.MaxScoreLevels, len(q.Levels))
	}

	if q.Labelled {
		if err := checkRubric(q, rate, labels); err != nil {
			return err
		}
	}

	return checkPolicy(q, false)
}

func checkBlankNames(q *Question, shape, noun string, names []string) error {
	for i, name := range names {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("%s %s %d%s is empty", shape, noun, i+1, questionClause(q))
		}
	}

	return nil
}

func tooFew(q *Question, name, noun string, count int, names []string) error {
	// A single unnamed entry is the empty list spelling, from --pick with an empty value or from
	// a body's unlabelled levels. Naming it would print the separator and nothing else.
	listed := strings.Join(names, ", ")
	if listed == "" {
		return fmt.Errorf("%s needs at least two %s%s, got %d",
			name, noun, questionClause(q), count)
	}

	return fmt.Errorf("%s needs at least two %s%s, got %d: %s",
		name, noun, questionClause(q), count, listed)
}

func tooMany(q *Question, name, noun string, limit, count int) error {
	return fmt.Errorf("%s takes at most %d %s%s, got %d",
		name, limit, noun, questionClause(q), count)
}

func checkRubric(q *Question, rate string, labels []string) error {
	if err := checkBlankNames(q, rate, "level", labels); err != nil {
		return err
	}

	if dupe, found := firstDuplicate(labels); found {
		return fmt.Errorf("%s label '%s' is listed twice in question '%s'", rate, dupe, q.ID)
	}

	if err := checkUnknownDesc(q, labels, rate); err != nil {
		return err
	}

	// A mixed rubric calibrates poorly and a warning would be invisible in a pipeline, so this is
	// fatal where the equivalent for --pick is only a warning.
	have := described(q)
	if len(have) != 0 && len(have) != len(labels) {
		return fmt.Errorf(
			"%s levels must all be described or all bare%s. Described %s but not %s",
			rate, questionClause(q), strings.Join(have, ", "),
			strings.Join(missing(labels, have), ", "),
		)
	}

	return nil
}

func questionClause(q *Question) string {
	// The positional question's id is the reserved answer, which the user never typed. Naming it
	// would point the reader at something they cannot find in their own command line.
	if q.Origin == OriginPositional {
		return ""
	}

	return " in question '" + q.ID + "'"
}

func checkNoul(q *Question) error {
	for _, key := range q.DescOrder {
		if key != "yes" && key != "no" {
			return fmt.Errorf(
				"--desc on a yes/no question takes 'yes' or 'no', got '%s'", key)
		}
	}

	return checkPolicy(q, true)
}

func checkBox(q *Question) error {
	if len(q.DescOrder) > 0 {
		return fmt.Errorf(
			"--desc does not apply to box question '%s', which has no options", q.ID)
	}

	for _, flag := range []struct {
		name string
		set  bool
	}{
		{"--threshold", q.Policy.Threshold != nil},
		{"--min-confidence", q.Policy.MinConfidence != nil},
		{"--fallback", q.Policy.Fallback != nil},
	} {
		if flag.set {
			return fmt.Errorf("%s does not apply to box question '%s'. Gate a box with %s",
				Spelling(q.Origin, flag.name), q.ID, Spelling(q.Origin, "--assert"))
		}
	}

	return nil
}

func countTrue(conditions ...bool) int {
	count := 0

	for _, condition := range conditions {
		if condition {
			count++
		}
	}

	return count
}

func checkPolicy(q *Question, yesNo bool) error {
	policy := q.Policy
	threshold := Spelling(q.Origin, "--threshold")
	confidence := Spelling(q.Origin, "--min-confidence")
	fallback := Spelling(q.Origin, "--fallback")

	if policy.Threshold != nil {
		if !yesNo {
			noun := "options"
			if q.Shape == Rate {
				noun = "levels"
			}

			return fmt.Errorf(
				"%s cuts a yes/no probability. '%s' has %s, use %s with %s",
				threshold, q.ID, noun, confidence, fallback)
		}

		if outsideUnit(*policy.Threshold) {
			return fmt.Errorf("%s must be between 0 and 1, got %s",
				threshold, strconv.FormatFloat(*policy.Threshold, 'g', -1, 64))
		}
	}

	if policy.MinConfidence != nil {
		if yesNo {
			return fmt.Errorf(
				"%s needs a confidence value. '%s' is a yes/no question, "+
					"use %s, or add %s or %s",
				confidence, q.ID, threshold,
				Spelling(q.Origin, "--pick"), Spelling(q.Origin, "--rate"))
		}

		if outsideUnit(*policy.MinConfidence) {
			return fmt.Errorf("%s must be between 0 and 1, got %s",
				confidence, strconv.FormatFloat(*policy.MinConfidence, 'g', -1, 64))
		}

		if policy.Fallback == nil {
			return fmt.Errorf(
				"%s needs %s, nothing to substitute for '%s'", confidence, fallback, q.ID)
		}
	}

	if yesNo && policy.Fallback != nil {
		if _, ok := ParseFallback(policy.Fallback.Text); !ok {
			return fmt.Errorf(
				"%s on a yes/no question takes true, false, yes or no, got '%s'",
				fallback, policy.Fallback.Text)
		}
	}

	return nil
}

func outsideUnit(value float64) bool {
	return math.IsNaN(value) || value < 0 || value > 1
}

func checkUnknownDesc(q *Question, vocabulary []string, shape string) error {
	for _, key := range q.DescOrder {
		if _, unknown := q.UnknownDesc[key]; !unknown {
			continue
		}

		// The key itself always comes from --desc, since a file has no way to name one. Only the
		// vocabulary it missed is spelled the way the question was written.
		return fmt.Errorf("--desc names an unknown key '%s' in question '%s'. %s has: %s",
			key, q.ID, shape, strings.Join(vocabulary, ", "))
	}

	return nil
}

// ShapeName spells a question's shape flag the way its origin wrote it, so a message points at the
// thing the reader typed. It is exported because assert checks an assertion against the same plan.
func ShapeName(q *Question, flag string) string {
	// A body has neither flags nor file keys. Its options and levels are the entries of a single
	// criteria key, which is the only thing a message can point the reader at.
	if q.Origin == OriginBody {
		return "'criteria'"
	}

	return Spelling(q.Origin, flag)
}

// Spelling renders a flag the way the origin spells it. A file says 'min_confidence' where the
// command line says --min-confidence. It is exported for the same reason ShapeName is.
func Spelling(origin Origin, flag string) string {
	if origin != OriginFile {
		return flag
	}

	switch flag {
	case "--pick":
		return "'pick'"
	case "--rate":
		return "'rate'"
	case "--threshold":
		return "'threshold'"
	case "--min-confidence":
		return "'min_confidence'"
	case "--fallback":
		return "'fallback'"
	case "--assert":
		return "'assert'"
	case "--abstain-if":
		return "'abstain_if'"
	default:
		return flag
	}
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

func firstDuplicate(names []string) (string, bool) {
	seen := make(map[string]struct{}, len(names))

	for _, name := range names {
		if _, ok := seen[name]; ok {
			return name, true
		}

		seen[name] = struct{}{}
	}

	return "", false
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

	return slices.Contains(reservedIDs, id)
}

// ReservedIDs lists the ids no question may take, apart from the positional id and any id that
// starts with two underscores.
func ReservedIDs() []string {
	return slices.Clone(reservedIDs)
}
