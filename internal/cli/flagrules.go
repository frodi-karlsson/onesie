package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	lineField = "line"
	wrapKey   = "state"
)

var errNoQuestion = errors.New("no question given. Pass a question, --ask, or -f")

// DryRunRules returns the command tree, then by long name the flags both dry runs refuse and the
// ones only --print-questions refuses, so a tool that builds a dry run knows what to strip.
func DryRunRules() (root *cobra.Command, always, questionsOnly []string) {
	always = []string{"output", "raw", "quiet", "usage", "merge", "merge-key", "stats", "stop-on-assert", "mock"}
	questionsOnly = []string{
		flagInput, flagJobs, flagModel, flagState, flagStateFile, "unordered", "stop-on-error", "skip-blank",
	}

	return NewRootCmd(BuildInfo{}).Command, always, questionsOnly
}

func validatePlan(built *plan.Plan, config runConfig, facts runFacts) ([]string, error) {
	if len(built.Questions) == 0 {
		return nil, errNoQuestion
	}

	var warnings []string

	// checkFlags runs first, so a streaming -q reports the streaming message.
	hint, err := checkFlags(config, facts)
	if hint != "" {
		warnings = append(warnings, hint)
	}

	if err != nil {
		return warnings, err
	}

	if err = checkFallbackColumns(built, config); err != nil {
		return warnings, err
	}

	if err = checkLineField(built, config); err != nil {
		return warnings, err
	}

	questionWarnings, err := plan.Validate(built, config.file)
	warnings = append(warnings, questionWarnings...)

	if err != nil {
		return warnings, err
	}

	return warnings, checkSingle(built, config, facts)
}

func checkFlags(config runConfig, facts runFacts) (string, error) {
	// Ahead of the request mode rules, so a flag both of them reject is reported against
	// --list-models, which is the flag that made the mode meaningless.
	if _, err := validateRun(config, facts, nil, modeRules("list-models-alone")); err != nil {
		return "", err
	}

	if err := checkResume(config); err != nil {
		return "", err
	}

	if err := checkPrune(config); err != nil {
		return "", err
	}

	// Both ahead of the output and streaming rules, so a dry run is rejected before anything it
	// would have written reaches stdout.
	if _, err := validateRun(config, facts, nil, modeRules("dry-run-and-request")); err != nil {
		return "", err
	}

	if err := checkDelimiter(config); err != nil {
		return "", err
	}

	if err := checkAbstainNeedsAssert(config, facts); err != nil {
		return "", err
	}

	if config.raw && config.output != "" {
		return "", errors.New("-r and -o are mutually exclusive")
	}

	if config.quiet && markdownOutput(config) {
		return "", fmt.Errorf("-q suppresses output, which leaves -o %s nothing to write", config.output)
	}

	if config.hasState && config.hasStateFile {
		return "", errors.New("--state and --state-file are mutually exclusive")
	}

	if config.replace && config.file == "" {
		return "", errors.New("--replace applies to -f, which was not given")
	}

	hint, err := checkStreaming(config)
	if err != nil {
		return hint, err
	}

	// After the streaming rules, so --stop-on-assert on one record is told it needs a stream first.
	if config.stopOnAssert && !hasAssert(config, facts) {
		return hint, errors.New("--stop-on-assert needs --assert, since without one no assertion is false")
	}

	return hint, nil
}

func checkAbstainNeedsAssert(config runConfig, facts runFacts) error {
	if !hasAbstainIf(config, facts) || hasAssert(config, facts) {
		return nil
	}

	return fmt.Errorf("%s needs --assert, since without one every record is a yes",
		abstainFlag(config, facts))
}

func checkResume(config runConfig) error {
	if !config.resume {
		return nil
	}

	switch {
	case config.out == "":
		return errors.New("--resume needs --out, the file it picks up from")
	case config.listModels:
		return errors.New("--resume applies to streaming input, which --list-models does not read")
	case config.printQuestions:
		return errors.New("--resume applies to streaming input, which --print-questions does not read")
	case config.printRequest:
		return errors.New("--print-request writes request bodies, not answers, " +
			"so --resume has nothing to pick up. Drop --resume")
	case !config.inputMode.Streaming():
		return fmt.Errorf("--resume applies to streaming input. -i %s reads one record", inputName(config.input))
	case config.unordered && !config.hasID:
		return errors.New("--resume relies on input order, which --unordered gives up. " +
			"Pass --id to resume by id")
	case config.raw || config.output == "raw":
		return errors.New("--resume needs output that keeps each record's outcome, " +
			"which raw lines do not. Use -o values or -o json")
	case markdownOutput(config):
		return errors.New("--resume reads each record's outcome back from --out, " +
			"which a markdown table does not keep. Use -o json, values, csv or tsv")
	default:
		return nil
	}
}

func markdownOutput(config runConfig) bool {
	return config.output == "markdown" || config.output == "md"
}

func checkPrune(config runConfig) error {
	switch {
	case !config.prune:
		return nil
	case !config.resume:
		return errors.New("--prune applies to --resume, which was not given")
	case !config.hasID:
		return errors.New("--prune drops the answered ids the input no longer has, so it needs --id")
	default:
		return nil
	}
}

func checkDelimiter(config runConfig) error {
	if !config.hasDelimiter {
		return nil
	}

	switch {
	case config.parsedDelimiter.Detect && config.inputMode != input.CSV:
		return fmt.Errorf("--%s auto needs -i csv, since it reads the separator from the input's header row",
			flagDelimiter)
	case config.inputMode == input.CSV || config.output == "csv":
		return nil
	default:
		return fmt.Errorf("--%s applies to -i csv and -o csv. -i %s %s",
			flagDelimiter, inputName(config.input), delimiterlessReason(config.inputMode))
	}
}

func checkInputDelimiter(config runConfig) error {
	if !config.hasDelimiter || config.inputMode == input.CSV {
		return nil
	}

	return fmt.Errorf("--%s applies to -i csv. -i %s %s",
		flagDelimiter, inputName(config.input), delimiterlessReason(config.inputMode))
}

func delimiterlessReason(mode input.Mode) string {
	if mode == input.TSV {
		return "is tab separated by definition"
	}

	return "has no delimiter"
}

func hasAssert(config runConfig, facts runFacts) bool {
	return len(config.assert) > 0 || facts.assert != ""
}

func hasAbstainIf(config runConfig, facts runFacts) bool {
	return len(config.abstainIf) > 0 || facts.abstainIf != ""
}

func abstainFlag(config runConfig, facts runFacts) string {
	if len(config.abstainIf) == 0 && facts.abstainIf != "" {
		return plan.Spelling(plan.OriginFile, "--abstain-if")
	}

	return "--abstain-if"
}

func checkStreaming(config runConfig) (string, error) {
	// Checked for every mode, since --merge applies to the non streaming ones too.
	if err := checkMerge(config); err != nil {
		return "", err
	}

	if err := checkDelimited(config); err != nil {
		return "", err
	}

	if err := checkUsage(config); err != nil {
		return "", err
	}

	// Also for every mode. The rule is rejected rather than clamped, and a typo in a shared alias
	// is exactly as wrong outside a stream as inside one. Gated on jobsSet because jobs is an int
	// and its zero value cannot be told from the flag being absent.
	if config.jobsSet && config.jobs < 1 {
		return "", fmt.Errorf("-j takes a positive number of records in flight, got %d",
			config.jobs)
	}

	if config.jobsSet && config.jobs > limits.MaxJobs {
		return "", fmt.Errorf("-j takes at most %d records in flight, got %d",
			limits.MaxJobs, config.jobs)
	}

	if config.timeoutSet && config.timeout < 1 {
		return "", fmt.Errorf("--timeout takes a positive number of seconds, got %d",
			config.timeout)
	}

	if err := tooManySeconds("--timeout", config.timeout, config.timeoutSet); err != nil {
		return "", err
	}

	if config.retriesSet && config.retries < 0 {
		return "", fmt.Errorf("--retries takes a retry count of zero or more, got %d",
			config.retries)
	}

	if config.retriesSet && config.retries > limits.MaxRetries {
		return "", fmt.Errorf("--retries takes at most %d, got %d",
			limits.MaxRetries, config.retries)
	}

	// Zero would have to mean either never honour the header or honour it without bound, so it is
	// rejected rather than given one of those meanings.
	if config.maxRetryAfterSet && config.maxRetryAfter < 1 {
		return "", fmt.Errorf(
			"--max-retry-after must be a positive number of seconds, got %d",
			config.maxRetryAfter)
	}

	err := tooManySeconds("--max-retry-after", config.maxRetryAfter, config.maxRetryAfterSet)
	if err != nil {
		return "", err
	}

	if !config.inputMode.Streaming() {
		return checkSingleRecord(config)
	}

	// The flag the user actually typed and the mode they actually gave. Naming --state for a
	// --state-file mistake sends them to the wrong flag, and naming jsonl for a lines run sends
	// them to the wrong mode.
	if config.hasState {
		return "", fmt.Errorf("--state cannot be combined with -i %s", inputName(config.input))
	}

	if config.hasStateFile {
		return "", fmt.Errorf("--state-file cannot be combined with -i %s", inputName(config.input))
	}

	if config.inputMode == input.Lines && len(config.imageFields) > 1 {
		return "", errors.New("-i lines takes one --image-field, since a line holds one path")
	}

	if config.quiet {
		return "", fmt.Errorf(
			"-q reads one record. Drop -i %s or use -o values and filter the stream",
			inputName(config.input))
	}

	// A stream writes one line per input line. A table is several lines with a repeated header, and
	// unlike raw it has no streaming form.
	if config.output == "table" {
		return "", fmt.Errorf("-o table reads one record. Drop -i %s or use -o json",
			inputName(config.input))
	}

	return "", nil
}

func checkMerge(config runConfig) error {
	if merging(config) && !mergeable(config) {
		return fmt.Errorf("%s needs -o json, values, csv or tsv", mergeName(config))
	}

	if config.mergeKey != wrapKey || (config.input != "text" && config.input != "lines") {
		return nil
	}

	return fmt.Errorf("--merge-key %s does not apply to -i %s, which wraps every record under a %s key",
		wrapKey, config.input, wrapKey)
}

func tooManySeconds(flag string, seconds int, set bool) error {
	if !set || seconds <= limits.MaxSeconds {
		return nil
	}

	// Seconds become a time.Duration downstream, and past a point that multiplication wraps. A
	// wrap to a positive value would hand a user who asked for centuries a fraction of a second.
	return fmt.Errorf("%s takes at most %d seconds, got %d", flag, limits.MaxSeconds, seconds)
}

func mergeable(config runConfig) bool {
	if config.raw {
		return false
	}

	// auto resolves to json under a merge, so an explicit auto is as mergeable as an absent flag.
	// The other -o rejections deliberately fire on auto, since a mode they ignore does nothing there.
	switch config.output {
	case "", "auto", "json", "values", "csv", "tsv":
		return true
	default:
		return false
	}
}

func checkDelimited(config runConfig) error {
	if config.output != "csv" && config.output != "tsv" {
		return nil
	}

	switch {
	case config.mergeKey != "":
		return fmt.Errorf("--merge-key does not apply to -o %s, which puts the answers in columns", config.output)
	case merging(config) && config.input != "csv" && config.input != "tsv":
		return fmt.Errorf(
			"-o %s with --merge needs -i csv or -i tsv, since other input has no fixed columns", config.output)
	case config.usage:
		return fmt.Errorf("--usage does not apply to -o %s, which has no column for it", config.output)
	default:
		return nil
	}
}

func checkUsage(config runConfig) error {
	if !config.usage {
		return nil
	}

	var mode string

	switch {
	case config.quiet:
		return errors.New("--usage does not apply to -q, which suppresses output")
	case config.raw:
		mode = "-r"
	case config.output == "values", config.output == "raw":
		mode = "-o " + config.output
	default:
		return nil
	}

	return fmt.Errorf("--usage does not apply to %s, which writes only the answers. Use -o json", mode)
}

func checkSingleRecord(config runConfig) (string, error) {
	for _, flag := range []struct {
		name string
		set  bool
	}{
		{"--unordered", config.unordered},
		{"--stop-on-error", config.stopOnError},
		{"--stop-on-assert", config.stopOnAssert},
		{"--skip-blank", config.skipBlank},
		{"--no-dedup", config.noDedup},
		{"--id", config.hasID},
		{"--image-field", len(config.imageFields) > 0},
	} {
		if flag.set {
			return "", fmt.Errorf(
				"%s applies to streaming input. -i %s reads one record",
				flag.name, inputName(config.input))
		}
	}

	if config.hasInput && len(config.images) > 0 && !config.hasState && !config.hasStateFile {
		return "", fmt.Errorf("-i %s holds one record, which beside --image reads stdin only through "+
			"--state -. Pass --state - to read the state from stdin", inputName(config.input))
	}

	if config.jobs > 1 {
		// A resource hint rather than a semantic one, and it may reasonably come from a shared
		// alias, so it warns and proceeds rather than failing.
		return fmt.Sprintf(
			"-j %d ignored. -i %s reads one record", config.jobs, inputName(config.input)), nil
	}

	return "", nil
}

func checkFallbackColumns(built *plan.Plan, config runConfig) error {
	if config.output != "csv" && config.output != "tsv" {
		return nil
	}

	ids := questionIDs(built.Questions)

	for _, question := range built.Questions {
		if question.Shape != plan.Box {
			continue
		}

		for _, column := range plan.BoxColumns(question.ID) {
			if slices.Contains(ids, column) {
				return fmt.Errorf("question '%s' takes a column name box question '%s' writes. "+
					"Rename one", column, question.ID)
			}
		}
	}

	for _, question := range built.Questions {
		if question.Policy.Fallback == nil {
			continue
		}

		if taken := plan.FallbackColumn(question.ID); slices.Contains(ids, taken) {
			return fmt.Errorf("question '%s' has the name of the fallback column -o %s writes for '%s'",
				taken, config.output, question.ID)
		}
	}

	return nil
}

func checkLineField(built *plan.Plan, config runConfig) error {
	if config.inputMode != input.Lines {
		return nil
	}

	for _, question := range built.Questions {
		if question.ID == lineField {
			return fmt.Errorf("question id '%s' is reserved under -i lines, "+
				"which writes the input line number as %s", lineField, lineField)
		}
	}

	return nil
}

func checkSingle(built *plan.Plan, config runConfig, facts runFacts) error {
	// Both spellings of the raw mode take a single question. Only the -r spelling is exclusive
	// with -o, so the two stay separate fields.
	raw := config.raw || config.output == "raw"

	// Beside an assertion -q only silences the output, since the assertion is the gate.
	quietGate := config.quiet && !hasAssert(config, facts)

	if !raw && !quietGate {
		return nil
	}

	names := make([]string, 0, len(built.Questions))
	for _, question := range built.Questions {
		names = append(names, "'"+question.ID+"'")
	}

	flag := "-r"

	switch {
	case quietGate:
		flag = "-q"
	case !config.raw:
		flag = "-o raw"
	}

	if len(built.Questions) != 1 && quietGate {
		return fmt.Errorf("-q needs a single question, or --assert to gate several. %s were asked",
			strings.Join(names, ", "))
	}

	if len(built.Questions) != 1 {
		return fmt.Errorf("%s needs a single question. %s were asked",
			flag, strings.Join(names, ", "))
	}

	only := built.Questions[0]
	if quietGate && only.Shape == plan.Box {
		return fmt.Errorf(
			"-q on '%s' needs %s. A box has no policy that could fail it",
			only.ID, plan.Spelling(only.Origin, "--assert"))
	}

	if quietGate && only.Shape != plan.Noul && only.Policy.MinConfidence == nil {
		return fmt.Errorf(
			"-q on '%s' needs %s and %s, or --assert. Without one the exit code is always 0",
			only.ID, plan.Spelling(only.Origin, "--min-confidence"), plan.Spelling(only.Origin, "--fallback"))
	}

	return nil
}
