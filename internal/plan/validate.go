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

	var warnings []string

	// Ahead of checkSingle, so a streaming -q reports the streaming message rather than the single
	// question policy one.
	flagHint, flagErr := CheckFlags(cfg)
	if flagHint != "" {
		warnings = append(warnings, flagHint)
	}

	if flagErr != nil {
		return warnings, flagErr
	}

	if err := checkBodyPolicy(p, cfg.FileName); err != nil {
		return warnings, err
	}

	if err := checkOrphans(p); err != nil {
		return warnings, err
	}

	if err := checkSources(p); err != nil {
		return warnings, err
	}

	if err := checkDuplicateIDs(p); err != nil {
		return warnings, err
	}

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

// CheckFlags reports the flag combination rules, which are the rules that need no plan. It is
// exported because -i request carries its own questions and so never builds one.
func CheckFlags(cfg Config) (string, error) {
	// Ahead of the request mode rules, so a flag both of them reject is reported against
	// --list-models, which is the flag that made the mode meaningless.
	if err := checkListModels(cfg); err != nil {
		return "", err
	}

	// Ahead of every other rule, so a flag -i request rejects outright is named by the message
	// that says so rather than by a general one that happens to fire first.
	if err := checkRequestMode(cfg); err != nil {
		return "", err
	}

	// Ahead of the output and streaming rules, so a dry run is rejected before anything it would
	// have written reaches stdout. -i request reaches this too, which is why it lives here rather
	// than in checkStreaming.
	if err := checkPrintFlags(cfg); err != nil {
		return "", err
	}

	if cfg.Raw && cfg.Output != "" {
		return "", errors.New("jev: -r and -o are mutually exclusive")
	}

	if cfg.HasState && cfg.HasStateFile {
		return "", errors.New("jev: --state and --state-file are mutually exclusive")
	}

	if cfg.Replace && cfg.FileName == "" {
		return "", errors.New("jev: --replace applies to -f, which was not given")
	}

	return checkStreaming(cfg)
}

// Config carries the invocation settings validation needs beyond the questions themselves.
type Config struct {
	Raw          bool
	Quiet        bool
	Output       string
	HasState     bool
	HasStateFile bool
	Replace      bool

	// FileName is the -f argument, empty when the flag was not given. It names the file in the
	// body policy message and marks whether -f was used at all.
	FileName string

	// HasAsk and HasPositional record which question source the user typed, which -i request
	// rejects one message apiece. A plan cannot answer this, since -i request never builds one.
	HasAsk        bool
	HasPositional bool

	// GroupFlags names every group local flag the user typed, in argv order. A Config field per
	// flag would say nothing a name cannot, and these only ever need to be rejected.
	GroupFlags []string

	// HasModel records that -m was given, which an empty Model string cannot do on its own.
	HasModel       bool
	Usage          bool
	PrintQuestions bool
	PrintRequest   bool

	// Stats is --stats, which summarises a run that made requests. It sits here rather than beside
	// the timing flags because the only rules it has are the print flags it cannot be combined
	// with.
	Stats bool

	// Streaming is true for an input mode that reads one record per line.
	Streaming bool

	// RequestMode is true for -i request, which carries its own questions and forwards raw
	// responses. Streaming is true for it too, so the two are not interchangeable.
	RequestMode bool

	// ListModels is --list-models, which asks no question, reads nothing and makes one request of
	// its own. Every flag around a question run is rejected for it.
	ListModels bool

	// InputName is the -i value as the user spelled it, so a message names the mode they gave.
	InputName string
	// HasInput records that -i was given, which InputName cannot do since it names the default
	// mode when the flag was absent.
	HasInput    bool
	Unordered   bool
	StopOnError bool
	SkipBlank   bool
	Merge       bool
	// MergeName is the merge flag as the user spelled it, so a message names --merge-key when that
	// is what was given. It defaults to --merge when empty.
	MergeName string
	Jobs      int
	// JobsSet distinguishes -j 0 from -j absent, which an int cannot do on its own.
	JobsSet bool

	// Timeout is --timeout in seconds. TimeoutSet distinguishes an explicit 0 from the flag being
	// absent, the same problem JobsSet solves.
	Timeout    int
	TimeoutSet bool

	Retries    int
	RetriesSet bool

	MaxRetryAfter    int
	MaxRetryAfterSet bool
}

func checkListModels(cfg Config) error {
	if !cfg.ListModels {
		return nil
	}

	if cfg.HasPositional {
		return errors.New("jev: --list-models asks no question. Drop the question argument")
	}

	// Every group flag at once, since a listing has no use for any of them and the only part of
	// the message that varies is the name the user typed.
	if len(cfg.GroupFlags) > 0 {
		return fmt.Errorf("jev: --list-models asks no question. Drop --%s", cfg.GroupFlags[0])
	}

	// In the order section 13 lists the flags, for the same reason checkRequestMode is.
	for _, rule := range []struct {
		given   bool
		message string
	}{
		{cfg.FileName != "", "jev: -f does not apply to --list-models, which asks no question"},
		{cfg.Replace, "jev: --replace applies to -f, which --list-models does not accept"},
		{cfg.HasState, "jev: --state does not apply to --list-models, which reads no state"},
		{cfg.HasStateFile, "jev: --state-file does not apply to --list-models, " +
			"which reads no state"},
		{cfg.HasInput, "jev: -i does not apply to --list-models, which reads no input"},
		{cfg.Output != "", "jev: -o does not apply to --list-models, " +
			"which writes a fixed listing"},
		{cfg.Raw, "jev: -r does not apply to --list-models, which writes a fixed listing"},
		{cfg.Quiet, "jev: -q suppresses output, which leaves --list-models nothing to write"},
		{cfg.Usage, "jev: --usage reports the tokens a question cost, " +
			"which --list-models does not ask"},
		{cfg.Merge, "jev: " + mergeFlag(cfg) + " needs answers to fold in, " +
			"which --list-models does not produce"},
		{cfg.JobsSet, "jev: -j does not apply to --list-models, which makes one request"},
		{cfg.HasModel, "jev: -m names a model to ask, which --list-models does not do"},
		{cfg.Unordered, "jev: --unordered applies to streaming input, " +
			"which --list-models does not read"},
		{cfg.StopOnError, "jev: --stop-on-error applies to streaming input, " +
			"which --list-models does not read"},
		{cfg.SkipBlank, "jev: --skip-blank applies to streaming input, " +
			"which --list-models does not read"},
		{cfg.PrintRequest, "jev: --print-request and --list-models each write a different " +
			"thing to stdout. Pass one"},
		{cfg.PrintQuestions, "jev: --print-questions and --list-models each write a different " +
			"thing to stdout. Pass one"},
	} {
		if rule.given {
			return errors.New(rule.message)
		}
	}

	return nil
}

func checkRequestMode(cfg Config) error {
	if !cfg.RequestMode {
		return nil
	}

	// In the order section 13 lists the flags, so a command line with several offenders reports a
	// predictable one rather than whichever check happened to be written first.
	for _, rule := range []struct {
		given   bool
		message string
	}{
		{cfg.HasPositional, "jev: -i request carries its own questions. " +
			"Drop the question argument"},
		{cfg.HasAsk, "jev: -i request carries its own questions. Drop --ask"},
		{cfg.FileName != "", "jev: -f does not apply to -i request, " +
			"which carries its own questions"},
		{cfg.Replace, "jev: --replace applies to -f, which -i request does not accept"},
		{given(cfg, "pick"), "jev: -i request carries its own questions. Drop --pick"},
		{given(cfg, "rate"), "jev: -i request carries its own questions. Drop --rate"},
		{given(cfg, "desc"), "jev: -i request carries its own questions. Drop --desc"},
		{given(cfg, "sep"), "jev: -i request carries its own questions. Drop --sep"},
		{given(cfg, "threshold"), "jev: --threshold does not apply to -i request, " +
			"which carries no policy"},
		{given(cfg, "min-confidence"), "jev: --min-confidence does not apply to -i request, " +
			"which carries no policy"},
		{given(cfg, "fallback"), "jev: --fallback does not apply to -i request, " +
			"which carries no policy"},
		{cfg.HasState, "jev: --state does not apply to -i request, " +
			"whose bodies carry their own state"},
		{cfg.HasStateFile, "jev: --state-file does not apply to -i request, " +
			"whose bodies carry their own state"},
		{cfg.Output != "", "jev: -o does not apply to -i request, which forwards raw responses"},
		{cfg.Raw, "jev: -r does not apply to -i request, which forwards raw responses"},
		{cfg.Quiet, "jev: -q needs a policy to report, which -i request has none of"},
		{cfg.Usage, "jev: --usage does not apply to -i request, " +
			"whose response bodies already carry usage"},
		{cfg.Merge, "jev: " + mergeFlag(cfg) +
			" does not apply to -i request, which forwards raw responses"},
		{cfg.HasModel, "jev: -m does not apply to -i request, " +
			"whose bodies carry their own model"},
		{cfg.PrintQuestions, "jev: --print-questions needs questions of its own, " +
			"which -i request does not build"},
	} {
		if rule.given {
			return errors.New(rule.message)
		}
	}

	return nil
}

func checkPrintFlags(cfg Config) error {
	if cfg.PrintRequest && cfg.PrintQuestions {
		return errors.New("jev: --print-request and --print-questions each write a " +
			"different thing to stdout. Pass one")
	}

	name, writes := printFlag(cfg)
	if name == "" {
		return nil
	}

	// Section 8 rejects a flag that would quietly do nothing, and a dry run accepts none of these.
	for _, rule := range []struct {
		given   bool
		message string
	}{
		{cfg.Output != "", fmt.Sprintf(
			"jev: -o does not apply to %s, which writes %s", name, writes)},
		{cfg.Raw, fmt.Sprintf("jev: -r does not apply to %s, which writes %s", name, writes)},
		{cfg.Merge, fmt.Sprintf(
			"jev: %s needs answers to fold in, which %s does not produce", mergeFlag(cfg), name)},
		{cfg.Quiet, fmt.Sprintf(
			"jev: -q suppresses output, which leaves %s nothing to write", name)},
		{cfg.Stats, fmt.Sprintf(
			"jev: --stats has nothing to report with %s, which makes no request", name)},
	} {
		if rule.given {
			return errors.New(rule.message)
		}
	}

	return nil
}

func printFlag(cfg Config) (string, string) {
	if cfg.PrintRequest {
		return "--print-request", "a request body"
	}

	if cfg.PrintQuestions {
		return "--print-questions", "a question file"
	}

	return "", ""
}

func given(cfg Config, name string) bool {
	for _, flag := range cfg.GroupFlags {
		if flag == name {
			return true
		}
	}

	return false
}

func mergeFlag(cfg Config) string {
	if cfg.MergeName == "" {
		return "--merge"
	}

	return cfg.MergeName
}

func checkStreaming(cfg Config) (string, error) {
	// Checked for every mode, since section 7 defines --merge for the non streaming ones too.
	if cfg.Merge && !mergeable(cfg) {
		return "", fmt.Errorf("jev: %s needs -o json or -o values", mergeFlag(cfg))
	}

	// Also for every mode. The rule is rejected rather than clamped, and a typo in a shared alias
	// is exactly as wrong outside a stream as inside one. Gated on JobsSet because Jobs is an int
	// and its zero value cannot be told from the flag being absent.
	if cfg.JobsSet && cfg.Jobs < 1 {
		return "", fmt.Errorf("jev: -j takes a positive number of records in flight, got %d",
			cfg.Jobs)
	}

	if cfg.TimeoutSet && cfg.Timeout < 1 {
		return "", fmt.Errorf("jev: --timeout takes a positive number of seconds, got %d",
			cfg.Timeout)
	}

	if err := tooManySeconds("--timeout", cfg.Timeout, cfg.TimeoutSet); err != nil {
		return "", err
	}

	if cfg.RetriesSet && cfg.Retries < 0 {
		return "", fmt.Errorf("jev: --retries takes a retry count of zero or more, got %d",
			cfg.Retries)
	}

	// Positive, not zero or more, which is the message section 11 already publishes. Zero would
	// have to mean either never honour the header or honour it without bound, and the spec picks
	// neither, so it is rejected rather than given a meaning here.
	if cfg.MaxRetryAfterSet && cfg.MaxRetryAfter < 1 {
		return "", fmt.Errorf(
			"jev: --max-retry-after must be a positive number of seconds, got %d",
			cfg.MaxRetryAfter)
	}

	err := tooManySeconds("--max-retry-after", cfg.MaxRetryAfter, cfg.MaxRetryAfterSet)
	if err != nil {
		return "", err
	}

	if !cfg.Streaming {
		return checkSingleRecord(cfg)
	}

	// The flag the user actually typed and the mode they actually gave. Naming --state for a
	// --state-file mistake sends them to the wrong flag, and naming jsonl for a lines run sends
	// them to the wrong mode.
	if cfg.HasState {
		return "", fmt.Errorf("jev: --state cannot be combined with -i %s", cfg.InputName)
	}

	if cfg.HasStateFile {
		return "", fmt.Errorf("jev: --state-file cannot be combined with -i %s", cfg.InputName)
	}

	if cfg.Quiet {
		return "", fmt.Errorf(
			"jev: -q reads one record. Drop -i %s or use -o values and filter the stream",
			cfg.InputName)
	}

	// Section 8 asks for one JSON line per input line. A table is several lines with a repeated
	// header, and section 7 gave raw its streaming semantics explicitly where table has none.
	if cfg.Output == "table" {
		return "", fmt.Errorf("jev: -o table reads one record. Drop -i %s or use -o json",
			cfg.InputName)
	}

	return "", nil
}

func tooManySeconds(flag string, seconds int, set bool) error {
	if !set || seconds <= limits.MaxSeconds {
		return nil
	}

	// Seconds become a time.Duration downstream, and past a point that multiplication wraps. A
	// wrap to a positive value would hand a user who asked for centuries a fraction of a second.
	return fmt.Errorf("jev: %s takes at most %d seconds, got %d", flag, limits.MaxSeconds, seconds)
}

func mergeable(cfg Config) bool {
	if cfg.Raw {
		return false
	}

	return cfg.Output == "" || cfg.Output == "json" || cfg.Output == "values"
}

func checkSingleRecord(cfg Config) (string, error) {
	for _, flag := range []struct {
		name string
		set  bool
	}{
		{"--unordered", cfg.Unordered},
		{"--stop-on-error", cfg.StopOnError},
		{"--skip-blank", cfg.SkipBlank},
	} {
		if flag.set {
			return "", fmt.Errorf(
				"jev: %s applies to streaming input. -i %s reads one record",
				flag.name, cfg.InputName)
		}
	}

	if cfg.Jobs > 1 {
		// A resource hint rather than a semantic one, and it may reasonably come from a shared
		// alias, so it warns and proceeds rather than failing.
		return fmt.Sprintf(
			"warning: -j %d ignored. -i %s reads one record", cfg.Jobs, cfg.InputName), nil
	}

	return "", nil
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
		"jev: policy flags apply to a request body only when it has one question. "+
			"%s has %d, freeze with --print-questions first", name, questions)
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
		if question.Origin.Chosen() {
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
	if dupe, found := firstDuplicate(ids); found {
		return fmt.Errorf("jev: question id '%s' is given twice", dupe)
	}

	return nil
}

func checkQuestion(q *Question) (string, error) {
	// The positional question is keyed answer by design, so the reserved list only applies to an
	// id the user chose.
	if q.Origin.Chosen() && (reserved(q.ID) || q.ID == PositionalID) {
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

	pick := shapeName(q, "--pick")

	switch {
	case len(q.Options) < limits.MinChoiceOptions:
		return "", tooFew(q, pick, "options", len(q.Options), names)
	case len(q.Options) > limits.MaxChoiceOptions:
		return "", tooMany(q, pick, "options", limits.MaxChoiceOptions, len(q.Options))
	}

	if dupe, found := firstDuplicate(names); found {
		return "", fmt.Errorf(
			"jev: %s option '%s' is listed twice in question '%s'", pick, dupe, q.ID)
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

	rate := shapeName(q, "--rate")

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

func tooFew(q *Question, name, noun string, count int, names []string) error {
	// A single unnamed entry is the empty list spelling, from --pick with an empty value or from
	// a body's unlabelled levels. Naming it would print the separator and nothing else.
	listed := strings.Join(names, ", ")
	if listed == "" {
		return fmt.Errorf("jev: %s needs at least two %s%s, got %d",
			name, noun, questionClause(q), count)
	}

	return fmt.Errorf("jev: %s needs at least two %s%s, got %d: %s",
		name, noun, questionClause(q), count, listed)
}

func tooMany(q *Question, name, noun string, limit, count int) error {
	return fmt.Errorf("jev: %s takes at most %d %s%s, got %d",
		name, limit, noun, questionClause(q), count)
}

func checkRubric(q *Question, rate string, labels []string) error {
	if dupe, found := firstDuplicate(labels); found {
		return fmt.Errorf("jev: %s label '%s' is listed twice in question '%s'", rate, dupe, q.ID)
	}

	if err := checkUnknownDesc(q, labels, rate); err != nil {
		return err
	}

	// A mixed rubric calibrates poorly and a warning would be invisible in a pipeline, so this is
	// fatal where the equivalent for --pick is only a warning.
	have := described(q)
	if len(have) != 0 && len(have) != len(labels) {
		return fmt.Errorf(
			"jev: %s levels must all be described or all bare%s. Described %s but not %s",
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
				"jev: --desc on a yes/no question takes 'yes' or 'no', got '%s'", key)
		}
	}

	return checkPolicy(q, true)
}

func checkPolicy(q *Question, yesNo bool) error {
	policy := q.Policy
	threshold := spelling(q.Origin, "--threshold")
	confidence := spelling(q.Origin, "--min-confidence")
	fallback := spelling(q.Origin, "--fallback")

	if policy.Threshold != nil {
		if !yesNo {
			noun := "options"
			if q.Shape == Rate {
				noun = "levels"
			}

			return fmt.Errorf(
				"jev: %s cuts a yes/no probability. '%s' has %s, use %s with %s",
				threshold, q.ID, noun, confidence, fallback)
		}

		if *policy.Threshold < 0 || *policy.Threshold > 1 {
			return fmt.Errorf("jev: %s must be between 0 and 1, got %s",
				threshold, strconv.FormatFloat(*policy.Threshold, 'g', -1, 64))
		}
	}

	if policy.MinConfidence != nil {
		if yesNo {
			return fmt.Errorf(
				"jev: %s needs a confidence value. '%s' is a yes/no question, "+
					"use %s, or add %s or %s",
				confidence, q.ID, threshold,
				spelling(q.Origin, "--pick"), spelling(q.Origin, "--rate"))
		}

		if *policy.MinConfidence < 0 || *policy.MinConfidence > 1 {
			return fmt.Errorf("jev: %s must be between 0 and 1, got %s",
				confidence, strconv.FormatFloat(*policy.MinConfidence, 'g', -1, 64))
		}

		if policy.Fallback == nil {
			return fmt.Errorf(
				"jev: %s needs %s, nothing to substitute for '%s'", confidence, fallback, q.ID)
		}
	}

	if yesNo && policy.Fallback != nil {
		if _, ok := ParseFallback(policy.Fallback.Text); !ok {
			return fmt.Errorf(
				"jev: %s on a yes/no question takes true, false, yes or no, got '%s'",
				fallback, policy.Fallback.Text)
		}
	}

	return nil
}

func checkUnknownDesc(q *Question, vocabulary []string, shape string) error {
	for _, key := range q.DescOrder {
		if _, unknown := q.UnknownDesc[key]; !unknown {
			continue
		}

		// The key itself always comes from --desc, since a file has no way to name one. Only the
		// vocabulary it missed is spelled the way the question was written.
		return fmt.Errorf("jev: --desc names an unknown key '%s' in question '%s'. %s has: %s",
			key, q.ID, shape, strings.Join(vocabulary, ", "))
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
			"jev: -q on '%s' needs %s and %s. Without a policy the exit code is always 0",
			only.ID, spelling(only.Origin, "--min-confidence"), spelling(only.Origin, "--fallback"))
	}

	return nil
}

func shapeName(q *Question, flag string) string {
	// A body has neither flags nor file keys. Its options and levels are the entries of a single
	// criteria key, which is the only thing a message can point the reader at.
	if q.Origin == OriginBody {
		return "'criteria'"
	}

	return spelling(q.Origin, flag)
}

func spelling(origin Origin, flag string) string {
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

	switch id {
	case "answers", "assert", "error", "model", "usage", "questions", "state":
		return true
	default:
		return false
	}
}
