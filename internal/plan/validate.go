package plan

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

// Validate checks a plan and returns the non fatal warnings plus the first fatal error, so a
// caller can print the warnings whether or not validation succeeded.
func Validate(p *Plan, cfg Config) ([]string, error) {
	if len(p.Questions) == 0 {
		return nil, errors.New("onesie: no question given. Pass a question, --ask, or -f")
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

	if err := checkResume(cfg); err != nil {
		return "", err
	}

	if err := checkPrune(cfg); err != nil {
		return "", err
	}

	// Both ahead of the output and streaming rules, so a dry run is rejected before anything it
	// would have written reaches stdout.
	for _, check := range flagChecks(cfg) {
		if err := check(cfg); err != nil {
			return "", err
		}
	}

	if err := checkAbstainNeedsAssert(cfg); err != nil {
		return "", err
	}

	if cfg.Raw && cfg.Output != "" {
		return "", errors.New("onesie: -r and -o are mutually exclusive")
	}

	if cfg.Quiet && markdown(cfg) {
		return "", fmt.Errorf("onesie: -q suppresses output, which leaves -o %s nothing to write", cfg.Output)
	}

	if cfg.HasState && cfg.HasStateFile {
		return "", errors.New("onesie: --state and --state-file are mutually exclusive")
	}

	if cfg.Replace && cfg.FileName == "" {
		return "", errors.New("onesie: --replace applies to -f, which was not given")
	}

	hint, err := checkStreaming(cfg)
	if err != nil {
		return hint, err
	}

	// After the streaming rules, so --stop-on-assert on one record is told it needs a stream first.
	if cfg.StopOnAssert && !cfg.HasAssert {
		return hint, errors.New("onesie: --stop-on-assert needs --assert, since without one no assertion is false")
	}

	return hint, nil
}

func checkAbstainNeedsAssert(cfg Config) error {
	if !cfg.HasAbstainIf || cfg.HasAssert {
		return nil
	}

	return fmt.Errorf("onesie: %s needs --assert, since without one every record is a yes",
		abstainFlag(cfg))
}

func checkResume(cfg Config) error {
	if !cfg.Resume {
		return nil
	}

	switch {
	case cfg.Out == "":
		return errors.New("onesie: --resume needs --out, the file it picks up from")
	case cfg.ListModels:
		return errors.New("onesie: --resume applies to streaming input, which --list-models does not read")
	case cfg.PrintQuestions:
		return errors.New("onesie: --resume applies to streaming input, which --print-questions does not read")
	case cfg.PrintRequest:
		return errors.New("onesie: --print-request writes request bodies, not answers, " +
			"so --resume has nothing to pick up. Drop --resume")
	case !cfg.Streaming:
		return fmt.Errorf("onesie: --resume applies to streaming input. -i %s reads one record", cfg.InputName)
	case cfg.Unordered && !cfg.HasID:
		return errors.New("onesie: --resume relies on input order, which --unordered gives up. " +
			"Pass --id to resume by id")
	case cfg.Raw || cfg.Output == "raw":
		return errors.New("onesie: --resume needs output that keeps each record's outcome, " +
			"which raw lines do not. Use -o values or -o json")
	case markdown(cfg):
		return errors.New("onesie: --resume reads each record's outcome back from --out, " +
			"which a markdown table does not keep. Use -o json, values, csv or tsv")
	default:
		return nil
	}
}

func markdown(cfg Config) bool {
	return cfg.Output == "markdown" || cfg.Output == "md"
}

func checkPrune(cfg Config) error {
	switch {
	case !cfg.Prune:
		return nil
	case !cfg.Resume:
		return errors.New("onesie: --prune applies to --resume, which was not given")
	case !cfg.HasID:
		return errors.New("onesie: --prune drops the answered ids the input no longer has, so it needs --id")
	default:
		return nil
	}
}

func flagChecks(cfg Config) []func(Config) error {
	// A dry run forwards nothing, writes no response body and makes no request, which is what
	// every output facing reason in the request mode table claims. Its own message is the true one
	// here, so it answers first.
	if cfg.PrintRequest {
		return []func(Config) error{checkPrintFlags, checkRequestMode}
	}

	// Otherwise the request mode rules lead, so a flag -i request rejects outright is named by the
	// message that says so rather than by a general one that happens to fire first.
	return []func(Config) error{checkRequestMode, checkPrintFlags}
}

func checkListModels(cfg Config) error {
	if !cfg.ListModels {
		return nil
	}

	if cfg.HasPositional {
		return errors.New("onesie: --list-models asks no question. Drop the question argument")
	}

	// Every group flag at once, since a listing has no use for any of them and the only part of
	// the message that varies is the name the user typed.
	if len(cfg.GroupFlags) > 0 {
		return fmt.Errorf("onesie: --list-models asks no question. Drop --%s", cfg.GroupFlags[0])
	}

	// In the order section 13 lists the flags, for the same reason checkRequestMode is.
	for _, rule := range []struct {
		given   bool
		message string
	}{
		{cfg.FileName != "", "onesie: -f does not apply to --list-models, which asks no question"},
		{cfg.Replace, "onesie: --replace applies to -f, which --list-models does not accept"},
		{cfg.HasState, "onesie: --state does not apply to --list-models, which reads no state"},
		{cfg.HasStateFile, "onesie: --state-file does not apply to --list-models, " +
			"which reads no state"},
		{cfg.HasInput, "onesie: -i does not apply to --list-models, which reads no input"},
		{cfg.HasMap, "onesie: --map does not apply to --list-models, which reads no state"},
		{cfg.HasID, "onesie: --id does not apply to --list-models, which reads no input"},
		{cfg.Output != "", "onesie: -o does not apply to --list-models, " +
			"which writes a fixed listing"},
		{cfg.Raw, "onesie: -r does not apply to --list-models, which writes a fixed listing"},
		{cfg.Quiet, "onesie: -q suppresses output, which leaves --list-models nothing to write"},
		{cfg.HasAssert, "onesie: --assert judges an answer, " +
			"which --list-models does not produce"},
		{cfg.HasAbstainIf, "onesie: --abstain-if judges an answer, " +
			"which --list-models does not produce"},
		{cfg.Usage, "onesie: --usage reports the tokens a question cost, " +
			"which --list-models does not ask"},
		{cfg.Merge, "onesie: " + mergeFlag(cfg) + " needs answers to fold in, " +
			"which --list-models does not produce"},
		{cfg.JobsSet, "onesie: -j does not apply to --list-models, which makes one request"},
		{cfg.HasModel, "onesie: -m names a model to ask, which --list-models does not do"},
		{cfg.Unordered, "onesie: --unordered applies to streaming input, " +
			"which --list-models does not read"},
		{cfg.StopOnError, "onesie: --stop-on-error applies to streaming input, " +
			"which --list-models does not read"},
		{cfg.StopOnAssert, "onesie: --stop-on-assert applies to streaming input, " +
			"which --list-models does not read"},
		{cfg.SkipBlank, "onesie: --skip-blank applies to streaming input, " +
			"which --list-models does not read"},
		{cfg.PrintRequest, "onesie: --print-request and --list-models each write a different " +
			"thing to stdout. Pass one"},
		{cfg.PrintQuestions, "onesie: --print-questions and --list-models each write a different " +
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
		{cfg.HasPositional, "onesie: -i request carries its own questions. " +
			"Drop the question argument"},
		{cfg.HasAsk, "onesie: -i request carries its own questions. Drop --ask"},
		{cfg.FileName != "", "onesie: -f does not apply to -i request, " +
			"which carries its own questions"},
		{cfg.Replace, "onesie: --replace applies to -f, which -i request does not accept"},
		{given(cfg, "pick"), "onesie: -i request carries its own questions. Drop --pick"},
		{given(cfg, "rate"), "onesie: -i request carries its own questions. Drop --rate"},
		{given(cfg, "desc"), "onesie: -i request carries its own questions. Drop --desc"},
		{given(cfg, "sep"), "onesie: -i request carries its own questions. Drop --sep"},
		{given(cfg, "threshold"), "onesie: --threshold does not apply to -i request, " +
			"which carries no policy"},
		{given(cfg, "min-confidence"), "onesie: --min-confidence does not apply to -i request, " +
			"which carries no policy"},
		{given(cfg, "fallback"), "onesie: --fallback does not apply to -i request, " +
			"which carries no policy"},
		{cfg.HasState, "onesie: --state does not apply to -i request, " +
			"whose bodies carry their own state"},
		{cfg.HasStateFile, "onesie: --state-file does not apply to -i request, " +
			"whose bodies carry their own state"},
		{cfg.HasMap, "onesie: --map does not apply to -i request, " +
			"whose bodies carry their own state"},
		{cfg.HasID, "onesie: --id does not apply to -i request, which forwards raw responses"},
		{cfg.Output != "", "onesie: -o does not apply to -i request, which forwards raw responses"},
		{cfg.Raw, "onesie: -r does not apply to -i request, which forwards raw responses"},
		{cfg.Quiet, "onesie: -q needs a policy to report, which -i request has none of"},
		{cfg.HasAssert, "onesie: --assert does not apply to -i request, " +
			"which forwards raw responses"},
		{cfg.HasAbstainIf, "onesie: --abstain-if does not apply to -i request, " +
			"which forwards raw responses"},
		{cfg.Usage, "onesie: --usage does not apply to -i request, " +
			"whose response bodies already carry usage"},
		{cfg.Merge, "onesie: " + mergeFlag(cfg) +
			" does not apply to -i request, which forwards raw responses"},
		{cfg.HasModel, "onesie: -m does not apply to -i request, " +
			"whose bodies carry their own model"},
		{cfg.StopOnAssert, "onesie: --stop-on-assert does not apply to -i request, " +
			"whose bodies carry no assertion"},
		{cfg.PrintQuestions, "onesie: --print-questions needs questions of its own, " +
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
		return errors.New("onesie: --print-request and --print-questions each write a " +
			"different thing to stdout. Pass one")
	}

	name, writes := printFlag(cfg)
	if name == "" {
		return nil
	}

	// Section 8 rejects a flag that would quietly do nothing, and a dry run accepts none of these.
	// In the order section 13 lists the flags, for the same reason checkListModels is. The rules
	// gated on PrintQuestions are the ones --print-request genuinely consumes.
	for _, rule := range []struct {
		given   bool
		message string
	}{
		{
			cfg.PrintQuestions && cfg.HasState,
			"onesie: --state does not apply to --print-questions, which reads no state",
		},
		{
			cfg.PrintQuestions && cfg.HasStateFile,
			"onesie: --state-file does not apply to --print-questions, which reads no state",
		},
		{
			cfg.PrintQuestions && cfg.HasInput,
			"onesie: -i does not apply to --print-questions, which reads no input",
		},
		{
			cfg.PrintQuestions && cfg.HasMap,
			"onesie: --map does not apply to --print-questions, which reads no state",
		},
		{
			cfg.PrintQuestions && cfg.HasID,
			"onesie: --id does not apply to --print-questions, which reads no input",
		},
		{cfg.Output != "", fmt.Sprintf(
			"onesie: -o does not apply to %s, which writes %s", name, writes)},
		{cfg.Raw, fmt.Sprintf("onesie: -r does not apply to %s, which writes %s", name, writes)},
		{cfg.Quiet, fmt.Sprintf(
			"onesie: -q suppresses output, which leaves %s nothing to write", name)},
		// Guarded to --print-request, since --print-questions writes the assertion into the file
		// it prints and so is the one dry run that carries it. §17.5. A gate that only the file
		// carries is ignored, so a gated file can be dry run as it stands.
		{cfg.PrintRequest && cfg.HasAssert && !fromFile(cfg.AssertName, "--assert"), fmt.Sprintf(
			"onesie: %s judges an answer, which --print-request does not produce",
			assertFlag(cfg))},
		{cfg.PrintRequest && cfg.HasAbstainIf && !fromFile(cfg.AbstainIfName, "--abstain-if"), fmt.Sprintf(
			"onesie: %s judges an answer, which --print-request does not produce",
			abstainFlag(cfg))},
		{cfg.Usage, fmt.Sprintf(
			"onesie: --usage reports the tokens a question cost, which %s does not ask", name)},
		{cfg.Merge, fmt.Sprintf(
			"onesie: %s needs answers to fold in, which %s does not produce", mergeFlag(cfg), name)},
		{
			cfg.PrintQuestions && cfg.JobsSet,
			"onesie: -j does not apply to --print-questions, which makes no request",
		},
		{
			cfg.PrintQuestions && cfg.HasModel,
			"onesie: -m names a model to ask, which --print-questions does not do",
		},
		// --stop-on-error and --unordered are absent because streamRequests honours both. Only the
		// assertion is a judgment a dry run never makes, so only its stop condition is rejected.
		{
			cfg.PrintRequest && cfg.StopOnAssert,
			"onesie: --stop-on-assert ends a stream on a false assertion, " +
				"which --print-request does not produce",
		},
		{cfg.Stats, fmt.Sprintf(
			"onesie: --stats has nothing to report with %s, which makes no request", name)},
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

func fromFile(name, flag string) bool {
	return name == Spelling(OriginFile, flag)
}

func assertFlag(cfg Config) string {
	if cfg.AssertName == "" {
		return "--assert"
	}

	return cfg.AssertName
}

func abstainFlag(cfg Config) string {
	if cfg.AbstainIfName == "" {
		return "--abstain-if"
	}

	return cfg.AbstainIfName
}

func checkStreaming(cfg Config) (string, error) {
	// Checked for every mode, since section 7 defines --merge for the non streaming ones too.
	if cfg.Merge && !mergeable(cfg) {
		return "", fmt.Errorf("onesie: %s needs -o json, values, csv or tsv", mergeFlag(cfg))
	}

	if err := checkDelimited(cfg); err != nil {
		return "", err
	}

	if err := checkUsage(cfg); err != nil {
		return "", err
	}

	// Also for every mode. The rule is rejected rather than clamped, and a typo in a shared alias
	// is exactly as wrong outside a stream as inside one. Gated on JobsSet because Jobs is an int
	// and its zero value cannot be told from the flag being absent.
	if cfg.JobsSet && cfg.Jobs < 1 {
		return "", fmt.Errorf("onesie: -j takes a positive number of records in flight, got %d",
			cfg.Jobs)
	}

	if cfg.JobsSet && cfg.Jobs > limits.MaxJobs {
		return "", fmt.Errorf("onesie: -j takes at most %d records in flight, got %d",
			limits.MaxJobs, cfg.Jobs)
	}

	if cfg.TimeoutSet && cfg.Timeout < 1 {
		return "", fmt.Errorf("onesie: --timeout takes a positive number of seconds, got %d",
			cfg.Timeout)
	}

	if err := tooManySeconds("--timeout", cfg.Timeout, cfg.TimeoutSet); err != nil {
		return "", err
	}

	if cfg.RetriesSet && cfg.Retries < 0 {
		return "", fmt.Errorf("onesie: --retries takes a retry count of zero or more, got %d",
			cfg.Retries)
	}

	if cfg.RetriesSet && cfg.Retries > limits.MaxRetries {
		return "", fmt.Errorf("onesie: --retries takes at most %d, got %d",
			limits.MaxRetries, cfg.Retries)
	}

	// Positive, not zero or more, which is the message section 11 already publishes. Zero would
	// have to mean either never honour the header or honour it without bound, and the spec picks
	// neither, so it is rejected rather than given a meaning here.
	if cfg.MaxRetryAfterSet && cfg.MaxRetryAfter < 1 {
		return "", fmt.Errorf(
			"onesie: --max-retry-after must be a positive number of seconds, got %d",
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
		return "", fmt.Errorf("onesie: --state cannot be combined with -i %s", cfg.InputName)
	}

	if cfg.HasStateFile {
		return "", fmt.Errorf("onesie: --state-file cannot be combined with -i %s", cfg.InputName)
	}

	if cfg.Quiet {
		return "", fmt.Errorf(
			"onesie: -q reads one record. Drop -i %s or use -o values and filter the stream",
			cfg.InputName)
	}

	// Section 8 asks for one JSON line per input line. A table is several lines with a repeated
	// header, and section 7 gave raw its streaming semantics explicitly where table has none.
	if cfg.Output == "table" {
		return "", fmt.Errorf("onesie: -o table reads one record. Drop -i %s or use -o json",
			cfg.InputName)
	}

	return "", nil
}

func mergeFlag(cfg Config) string {
	if cfg.MergeName == "" {
		return "--merge"
	}

	return cfg.MergeName
}

func tooManySeconds(flag string, seconds int, set bool) error {
	if !set || seconds <= limits.MaxSeconds {
		return nil
	}

	// Seconds become a time.Duration downstream, and past a point that multiplication wraps. A
	// wrap to a positive value would hand a user who asked for centuries a fraction of a second.
	return fmt.Errorf("onesie: %s takes at most %d seconds, got %d", flag, limits.MaxSeconds, seconds)
}

func mergeable(cfg Config) bool {
	if cfg.Raw {
		return false
	}

	// auto resolves to json under a merge, so an explicit auto is as mergeable as an absent flag.
	// The other -o rejections deliberately fire on auto, since a mode they ignore does nothing there.
	switch cfg.Output {
	case "", "auto", "json", "values", "csv", "tsv":
		return true
	default:
		return false
	}
}

func checkDelimited(cfg Config) error {
	if cfg.Output != "csv" && cfg.Output != "tsv" {
		return nil
	}

	switch {
	case cfg.HasMergeKey:
		return fmt.Errorf("onesie: --merge-key does not apply to -o %s, which puts the answers in columns", cfg.Output)
	case cfg.Merge && cfg.InputName != "csv" && cfg.InputName != "tsv":
		return fmt.Errorf(
			"onesie: -o %s with --merge needs -i csv or -i tsv, since other input has no fixed columns", cfg.Output)
	case cfg.Usage:
		return fmt.Errorf("onesie: --usage does not apply to -o %s, which has no column for it", cfg.Output)
	default:
		return nil
	}
}

func checkUsage(cfg Config) error {
	if !cfg.Usage {
		return nil
	}

	var mode string

	switch {
	case cfg.Quiet:
		return errors.New("onesie: --usage does not apply to -q, which suppresses output")
	case cfg.Raw:
		mode = "-r"
	case cfg.Output == "values", cfg.Output == "raw":
		mode = "-o " + cfg.Output
	default:
		return nil
	}

	return fmt.Errorf("onesie: --usage does not apply to %s, which writes only the answers. Use -o json", mode)
}

func checkSingleRecord(cfg Config) (string, error) {
	for _, flag := range []struct {
		name string
		set  bool
	}{
		{"--unordered", cfg.Unordered},
		{"--stop-on-error", cfg.StopOnError},
		{"--stop-on-assert", cfg.StopOnAssert},
		{"--skip-blank", cfg.SkipBlank},
		{"--id", cfg.HasID},
	} {
		if flag.set {
			return "", fmt.Errorf(
				"onesie: %s applies to streaming input. -i %s reads one record",
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
		"onesie: policy flags apply to a request body only when it has one question. "+
			"%s has %d, freeze with --print-questions first", name, questions)
}

func checkOrphans(p *Plan) error {
	if len(p.Orphans) == 0 {
		return nil
	}

	return fmt.Errorf(
		"onesie: --%s given with no --ask to bind to and %d questions asked",
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
			"onesie: a positional question cannot be combined with --ask or -f")
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
		return fmt.Errorf("onesie: question id '%s' is given twice", dupe)
	}

	return nil
}

func checkQuestion(q *Question) (string, error) {
	// The positional question is keyed answer by design, so the reserved list only applies to an
	// id the user chose.
	if q.Origin.Chosen() && (reserved(q.ID) || q.ID == PositionalID) {
		return "", fmt.Errorf("onesie: question id '%s' is reserved", q.ID)
	}

	if len(q.Options) > 0 && len(q.Levels) > 0 {
		return "", errors.New(
			"onesie: --pick and --rate are mutually exclusive. A question is one or the other")
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

	pick := ShapeName(q, "--pick")

	switch {
	case len(q.Options) < limits.MinChoiceOptions:
		return "", tooFew(q, pick, "options", len(q.Options), names)
	case len(q.Options) > limits.MaxChoiceOptions:
		return "", tooMany(q, pick, "options", limits.MaxChoiceOptions, len(q.Options))
	}

	if dupe, found := firstDuplicate(names); found {
		return "", fmt.Errorf(
			"onesie: %s option '%s' is listed twice in question '%s'", pick, dupe, q.ID)
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

func tooFew(q *Question, name, noun string, count int, names []string) error {
	// A single unnamed entry is the empty list spelling, from --pick with an empty value or from
	// a body's unlabelled levels. Naming it would print the separator and nothing else.
	listed := strings.Join(names, ", ")
	if listed == "" {
		return fmt.Errorf("onesie: %s needs at least two %s%s, got %d",
			name, noun, questionClause(q), count)
	}

	return fmt.Errorf("onesie: %s needs at least two %s%s, got %d: %s",
		name, noun, questionClause(q), count, listed)
}

func tooMany(q *Question, name, noun string, limit, count int) error {
	return fmt.Errorf("onesie: %s takes at most %d %s%s, got %d",
		name, limit, noun, questionClause(q), count)
}

func checkRubric(q *Question, rate string, labels []string) error {
	if dupe, found := firstDuplicate(labels); found {
		return fmt.Errorf("onesie: %s label '%s' is listed twice in question '%s'", rate, dupe, q.ID)
	}

	if err := checkUnknownDesc(q, labels, rate); err != nil {
		return err
	}

	// A mixed rubric calibrates poorly and a warning would be invisible in a pipeline, so this is
	// fatal where the equivalent for --pick is only a warning.
	have := described(q)
	if len(have) != 0 && len(have) != len(labels) {
		return fmt.Errorf(
			"onesie: %s levels must all be described or all bare%s. Described %s but not %s",
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
				"onesie: --desc on a yes/no question takes 'yes' or 'no', got '%s'", key)
		}
	}

	return checkPolicy(q, true)
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
				"onesie: %s cuts a yes/no probability. '%s' has %s, use %s with %s",
				threshold, q.ID, noun, confidence, fallback)
		}

		if *policy.Threshold < 0 || *policy.Threshold > 1 {
			return fmt.Errorf("onesie: %s must be between 0 and 1, got %s",
				threshold, strconv.FormatFloat(*policy.Threshold, 'g', -1, 64))
		}
	}

	if policy.MinConfidence != nil {
		if yesNo {
			return fmt.Errorf(
				"onesie: %s needs a confidence value. '%s' is a yes/no question, "+
					"use %s, or add %s or %s",
				confidence, q.ID, threshold,
				Spelling(q.Origin, "--pick"), Spelling(q.Origin, "--rate"))
		}

		if *policy.MinConfidence < 0 || *policy.MinConfidence > 1 {
			return fmt.Errorf("onesie: %s must be between 0 and 1, got %s",
				confidence, strconv.FormatFloat(*policy.MinConfidence, 'g', -1, 64))
		}

		if policy.Fallback == nil {
			return fmt.Errorf(
				"onesie: %s needs %s, nothing to substitute for '%s'", confidence, fallback, q.ID)
		}
	}

	if yesNo && policy.Fallback != nil {
		if _, ok := ParseFallback(policy.Fallback.Text); !ok {
			return fmt.Errorf(
				"onesie: %s on a yes/no question takes true, false, yes or no, got '%s'",
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
		return fmt.Errorf("onesie: --desc names an unknown key '%s' in question '%s'. %s has: %s",
			key, q.ID, shape, strings.Join(vocabulary, ", "))
	}

	return nil
}

func checkSingle(p *Plan, cfg Config) error {
	// Both spellings of the raw mode take a single question. Only the -r spelling is exclusive
	// with -o, which is why the two are still distinguished above.
	raw := cfg.Raw || cfg.Output == "raw"

	// Beside an assertion -q only silences the output, since the assertion is the gate.
	quietGate := cfg.Quiet && !cfg.HasAssert

	if !raw && !quietGate {
		return nil
	}

	names := make([]string, 0, len(p.Questions))
	for _, question := range p.Questions {
		names = append(names, "'"+question.ID+"'")
	}

	flag := "-r"

	switch {
	case quietGate:
		flag = "-q"
	case !cfg.Raw:
		flag = "-o raw"
	}

	if len(p.Questions) != 1 {
		return fmt.Errorf("onesie: %s needs a single question. %s were asked",
			flag, strings.Join(names, ", "))
	}

	only := p.Questions[0]
	if quietGate && only.Shape != Noul && only.Policy.MinConfidence == nil {
		return fmt.Errorf(
			"onesie: -q on '%s' needs %s and %s, or --assert. Without one the exit code is always 0",
			only.ID, Spelling(only.Origin, "--min-confidence"), Spelling(only.Origin, "--fallback"))
	}

	return nil
}

// Config carries the invocation settings validation needs beyond the questions themselves.
type Config struct {
	Raw   bool
	Quiet bool

	// HasAssert records that an assertion was given, by the flag or by a question file's key. It is
	// the second way a pick or rate question satisfies the -q rule, per §17.5.
	HasAssert bool
	// AssertName is the assertion as the user spelled it, so a message names a file's 'assert' key
	// when that is where the gate came from. It defaults to --assert when empty.
	AssertName string
	// HasAbstainIf records that an abstain expression was given, by the flag or by a question
	// file's key.
	HasAbstainIf bool
	// AbstainIfName is the abstain expression as the user spelled it, the way AssertName spells the
	// assertion. It defaults to --abstain-if when empty.
	AbstainIfName string

	Output       string
	HasState     bool
	HasStateFile bool
	// HasMap records that --map was given, which chooses the state from each record.
	HasMap bool
	// HasID records that --id was given, which names each record of a stream. Its expression runs
	// one record at a time as the input is read, so it should be cheap, such as a field lookup.
	HasID   bool
	Replace bool

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

	// Stats is --stats, which summarises a run that made requests.
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
	// Out is the --out file the answers are written to, empty for stdout.
	Out string
	// Resume is --resume, which picks a stream up where an earlier run into Out stopped.
	Resume bool
	// Prune is --prune, which drops the answered ids a resume by id no longer finds in the input.
	Prune bool
	// StopOnAssert is --stop-on-assert, which ends a stream at the first record whose assertion
	// was false. §17.6.
	StopOnAssert bool
	SkipBlank    bool
	Merge        bool
	// MergeName is the merge flag as the user spelled it, so a message names --merge-key when that
	// is what was given. It defaults to --merge when empty.
	MergeName string
	// HasMergeKey is --merge-key, which -o csv and tsv refuse even when --merge is also given.
	HasMergeKey bool
	Jobs        int
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

// ShapeName spells a question's shape flag the way its origin wrote it, so a message points at the
// thing the reader typed. It is exported because §17 checks an assertion against the same plan.
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

	switch id {
	case "abstain", "abstain_if", "answers", "assert", "error", "id", "model", "usage", "questions", "state":
		return true
	default:
		return false
	}
}
