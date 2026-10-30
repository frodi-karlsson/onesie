package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

func run(
	cmd *cobra.Command,
	settings rootSettings,
	events []argv.Event,
	positional string,
	parsed *runFlags,
	out *answersfile.File,
	resume answersfile.Resume,
) error {
	flags, err := newRunConfig(cmd, settings.lookupEnv, *parsed, events, positional)
	if err != nil {
		return err
	}

	// Both branches sit ahead of the plan, since neither has a question and failing for want of one
	// would send the user to the wrong flag. --list-models comes first, so it names itself rather
	// than the mode it was combined with.
	if flags.listModels {
		return runListModels(cmd, settings, flags, out)
	}

	if flags.inputMode == input.Request {
		return runRequestBodies(cmd, settings, flags, out, resume)
	}

	inv, warnings, err := build(settings, flags, false)

	// Warnings print whether or not validation succeeded, so a run that fails for one reason still
	// reports the others.
	if warnErr := warnAll(cmd.ErrOrStderr(), warnings); warnErr != nil {
		return warnErr
	}

	if err != nil {
		return err
	}

	if bodyErr := checkBodyState(flags, inv.loaded); bodyErr != nil {
		return bodyErr
	}

	// Before the output mode, because a question file is not an output mode and -o has no meaning
	// for it. After validation, because a dry run that accepted a plan the real run would reject
	// would be worse than useless.
	if flags.printQuestions {
		out.BindWithoutFingerprint()

		return printQuestions(
			cmd.OutOrStdout(), cmd.ErrOrStderr(), inv.Plan.Questions, inv.Gate.Source(),
			inv.Abstain.Source(), inv.loaded, settings.schemaURL)
	}

	return runPlan(cmd, settings, flags, inv, out, resume)
}

func runListModels(cmd *cobra.Command, settings rootSettings, flags runConfig, out *answersfile.File) error {
	// The only hint is -j on one record, which --list-models refuses and -i request never is.
	if _, checkErr := checkFlags(flags, runFacts{}); checkErr != nil {
		return checkErr
	}

	out.BindWithoutFingerprint()

	return withStats(cmd, settings.now, flags, func(stats *runner.Collector) error {
		return listModels(cmd, settings, flags, stats)
	})
}

func runRequestBodies(
	cmd *cobra.Command, settings rootSettings, flags runConfig, out *answersfile.File, resume answersfile.Resume,
) error {
	if _, checkErr := checkFlags(flags, runFacts{}); checkErr != nil {
		return checkErr
	}

	// Each body names its own model or takes the run's, and the fingerprint holds no model, as
	// it holds nothing else a body carries.
	if bindErr := bindOut(out, settings, flags, nil, answersfile.FingerprintInputs{
		Input: flags.inputMode.String(), LongContext: flags.env.longContext,
	}); bindErr != nil {
		return bindErr
	}

	return withStats(cmd, settings.now, flags, func(stats *runner.Collector) error {
		return streamRaw(cmd, settings, flags, stats, out, resume)
	})
}

func runPlan(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	inv *invocation,
	out *answersfile.File,
	resume answersfile.Resume,
) error {
	built, gate, abstain, inputMode := inv.Plan, inv.Gate, inv.Abstain, flags.inputMode

	model, err := resolveModel(settings, built.Model)
	if err != nil {
		return err
	}

	// Loaded before any mode writes, so a file that does not match the questions leaves no line.
	answers, err := answersFor(cmd, settings, flags, built, built.Questions, model)
	if err != nil {
		return err
	}

	// Both modes are parsed before the request, so a mistyped flag costs nothing.
	outputMode, err := output.ParseMode(
		outputName(flags), settings.stdoutTTY, inputMode.Streaming(), merging(flags))
	if err != nil {
		return err
	}

	// After the plan is built, since the fingerprint covers its questions, and before any mode
	// writes, since a refused resume must leave the file as it was.
	bindErr := bindOut(out, settings, flags, built.Questions, answersfile.FingerprintInputs{
		Model: model, Output: outputMode.String(), Input: inputMode.String(), Assert: gate.Source(),
		AbstainIf: abstain.Source(), Images: inv.images.fingerprint(), LongContext: built.LongContext,
	})
	if bindErr != nil {
		return bindErr
	}

	if inputMode.Streaming() {
		if warnErr := warnIgnoredBodyState(
			cmd.ErrOrStderr(), flags, inv.loaded, "a stream", inv.images, plan.WireAll(built.Questions)); warnErr != nil {
			return warnErr
		}

		if refuseErr := checkProviderNeeds(settings, flags, built, inv.images); refuseErr != nil {
			return refuseErr
		}

		return withStats(cmd, settings.now, flags, func(stats *runner.Collector) error {
			return stream(cmd, streamInputs{
				settings: settings, flags: flags, inv: inv, outputMode: outputMode, stats: stats, answers: out,
				resume: resume, answerers: answers,
			})
		})
	}

	if refuseErr := checkProviderNeeds(settings, flags, built, inv.images); refuseErr != nil {
		return refuseErr
	}

	return runOne(cmd, settings, flags, inv, outputMode, answers)
}

func runOne(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	inv *invocation,
	outputMode output.Mode,
	answers runner.AnswererFactory,
) error {
	built := inv.Plan

	resolved, err := resolveState(cmd.Context(), settings, flags, inv.loaded, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	if requests(flags) && resolved.Source == input.SourceNone && len(flags.images) == 0 {
		return noState(resolved)
	}

	sent := resolved.Wire
	if resolved.Source != input.SourceNone {
		sent, err = runner.Mapped(cmd.Context(), inv.Mapper, resolved.State, resolved.Wire, plan.Flags)
		if err != nil {
			return err
		}
	}

	sent, assets, err := inv.images.Attach(sentOrNil(resolved, sent))
	if err != nil {
		return err
	}

	if len(flags.images) > 0 {
		resolved, err = imagedState(sent)
		if err != nil {
			return err
		}
	}

	// After the state is resolved, so the body carries the state a real run would send.
	if flags.printRequest {
		chosen, err := settings.holder.resolved()
		if err != nil {
			return err
		}

		model := chosen.ModelFor(built.Model, settings.lookupEnv)
		req := runner.Ask{Plan: built, Model: model, Questions: plan.WireAll(built.Questions)}.Request(sent, assets)

		return printRequest(cmd.OutOrStdout(), chosen.Provider, req, resolved.Source != input.SourceNone)
	}

	// Checked here rather than at write time, so a taken key costs no request.
	if merging(flags) && runner.HasKey(resolved.State, mergeKey(flags)) {
		return fmt.Errorf(
			"--merge would overwrite the input's '%s' key. Pass --merge-key",
			mergeKey(flags))
	}

	return withStats(cmd, settings.now, flags, func(stats *runner.Collector) error {
		return ask(cmd, settings, flags, inv, singleState{resolved: resolved, sent: sent, assets: assets},
			outputMode, stats, answers)
	})
}

func noState(resolved input.Resolved) error {
	if resolved.EmptyStdin {
		return errors.New("no state given, since stdin was empty. Pipe one to stdin, or pass --state or --state-file")
	}

	return errors.New("no state given. Pipe one to stdin, or pass --state or --state-file")
}

func sentOrNil(resolved input.Resolved, sent any) any {
	if resolved.Source == input.SourceNone {
		return nil
	}

	return sent
}

func imagedState(sent any) (input.Resolved, error) {
	encoded, isRaw := sent.(json.RawMessage)
	if !isRaw {
		return input.Resolved{}, errors.New("the state beside --image is not encoded")
	}

	var state any
	if err := json.Unmarshal(encoded, &state); err != nil {
		return input.Resolved{}, fmt.Errorf("reading the state: %w", err)
	}

	return input.Resolved{Source: input.SourceImages, State: state, Raw: string(encoded), Wire: sent}, nil
}

func checkBodyState(flags runConfig, loaded *qfile.File) error {
	if loaded == nil || !loaded.HasState {
		return nil
	}

	var flag string

	switch {
	case flags.hasState:
		flag = "--" + flagState
	case flags.hasStateFile:
		flag = "--" + flagStateFile
	case len(flags.images) > 0:
		flag = "--image"
	default:
		return nil
	}

	return fmt.Errorf("%s and the state in %s both give a state. Drop one", flag, flags.file)
}

func warnIgnoredBodyState(
	w io.Writer, flags runConfig, loaded *qfile.File, sender string, images *imaging, questions onesie.Questions,
) error {
	if loaded == nil || !loaded.HasState {
		return nil
	}

	dropped, err := images.dropUnreferenced(questions)
	if err != nil {
		return err
	}

	note := ""
	if dropped > 0 {
		note = fmt.Sprintf(" and its %s", plural(dropped, "asset"))
	}

	return output.Warning(w, fmt.Sprintf(
		"ignoring the state in %s%s, since %s sends each record as its state", flags.file, note, sender))
}

func resolveState(
	ctx context.Context,
	settings rootSettings,
	flags runConfig,
	loaded *qfile.File,
	hint io.Writer,
) (input.Resolved, error) {
	// Stdin is never read beside a body's state. Whether a pipe holds text cannot be known without
	// a read that may block, so the body alone decides.
	if loaded != nil && loaded.HasState {
		resolved := input.Resolved{
			Source: input.SourceBody,
			State:  loaded.State,
			Raw:    string(loaded.StateWire),
			// The ordered bytes the loader kept, not a Go map marshalled again, so the body
			// reaches the wire in the order it was written.
			Wire: loaded.StateWire,
		}

		if err := input.CheckState(resolved.State); err != nil {
			return input.Resolved{}, err
		}

		return resolved, nil
	}

	// --image is a state source, so stdin is read only when --state asks for stdin.
	stdinTTY := settings.stdinTTY || len(flags.images) > 0

	return input.Resolve(ctx, input.Query{
		Mode:         flags.inputMode,
		Stdin:        settings.stdin,
		StdinTTY:     stdinTTY,
		State:        flags.state,
		HasState:     flags.hasState,
		StateFile:    flags.stateFile,
		HasStateFile: flags.hasStateFile,
		ReadFile:     settings.readFile,
		Hint:         hint,
		Clock:        settings.stdinClock,
	})
}

func bindOut(
	out *answersfile.File,
	settings rootSettings,
	flags runConfig,
	questions []plan.Question,
	inputs answersfile.FingerprintInputs,
) error {
	if out == nil {
		return nil
	}

	inputs, err := runInputs(settings, flags, inputs)
	if err != nil {
		return err
	}

	return wordAnswersError(out.Bind(questions, plan.WireAll(questions), inputs))
}

func outFingerprint(
	settings rootSettings, flags runConfig, questions []plan.Question, inputs answersfile.FingerprintInputs,
) (answersfile.Print, error) {
	inputs, err := runInputs(settings, flags, inputs)
	if err != nil {
		return answersfile.Print{}, err
	}

	return answersfile.Fingerprint(questions, plan.WireAll(questions), inputs)
}

func runInputs(settings rootSettings, flags runConfig, inputs answersfile.FingerprintInputs) (answersfile.FingerprintInputs, error) {
	chosen, err := settings.holder.resolved()
	if err != nil {
		return answersfile.FingerprintInputs{}, err
	}

	inputs.Provider = chosen.Provider.Name()
	inputs.MapSource = flags.mapSource
	inputs.IDSource = flags.idSource

	if merging(flags) {
		inputs.MergeKey = mergeKey(flags)
	}

	// A resume by position skips one record per stored line, and --skip-blank changes which record
	// a line stands for everywhere but csv and tsv, which skip a blank line either way.
	inputs.SkipBlank = flags.skipBlank && !resumesByID(flags.runFlags) && !delimitedInput(inputs.Input)
	inputs.Mock = flags.mockPath != ""

	return inputs, nil
}

func resumesByID(flags runFlags) bool {
	return flags.idSource != ""
}

func delimitedInput(name string) bool {
	mode, err := input.ParseMode(name)

	return err == nil && mode.Delimited()
}

func asked(events []argv.Event) bool {
	for _, event := range events {
		if event.Name == "ask" {
			return true
		}
	}

	return false
}

func inputName(flag string) string {
	// Reachable only by passing -i with an empty value, since the flag defaults to text. Every
	// message that names the mode would otherwise read -i with nothing after it.
	if flag == "" {
		return "text"
	}

	return flag
}

func interrupted(ctx context.Context) bool {
	return ctx.Err() != nil
}

func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}

	if noun == "retry" {
		return fmt.Sprintf("%d retries", count)
	}

	return fmt.Sprintf("%d %ss", count, noun)
}

func mergeName(flags runConfig) string {
	if flags.merge {
		return "--merge"
	}

	if flags.mergeKey != "" {
		return "--merge-key"
	}

	return ""
}

func streamResult(result engine.Result, falseAsserts, abstains int) error {
	if result.Broken {
		// The consumer stopped reading, which is its right. Nothing is reported and the run
		// succeeded.
		return nil
	}

	if result.Aborted {
		// Returned rather than printed. Execute already reports it, adding the onesie prefix, so
		// printing here would report the abort twice.
		if stoppedAtInput(result.Cause) {
			return &stoppedError{cause: result.Cause}
		}

		return &abortError{cause: result.Cause}
	}

	if result.Failed > 0 {
		return &recordsError{}
	}

	// After the failed records and before the successful return. A stream carrying both exits 6,
	// since a record that never answered says more than a gate that answered no.
	if falseAsserts > 0 {
		return &rejectedError{}
	}

	// Last, since a gate that could not decide says less than one that answered no.
	if abstains > 0 {
		return &abstainError{}
	}

	return nil
}

func stoppedAtInput(cause error) bool {
	// --stop-on-error ended the run at a record that never became a request. The exit code table
	// has no code of its own for that, so it exits 6 as the same record does without the flag.
	return !runner.Aborting(cause) && !errors.Is(cause, context.Canceled) && classify(cause).kind == output.KindInput
}

func ask(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	inv *invocation,
	state singleState,
	outputMode output.Mode,
	stats *runner.Collector,
	answerers runner.AnswererFactory,
) error {
	asker, err := answerers(cmd.Context(), stats)
	if err != nil {
		return err
	}

	built, gate, abstain := inv.Plan, inv.Gate, inv.Abstain

	model, err := resolveModel(settings, built.Model)
	if err != nil {
		return err
	}

	asking := runner.Ask{
		Plan: built, Model: model, Questions: plan.WireAll(built.Questions), Usage: flags.usage, Message: text,
	}

	record, err := asking.Record(cmd.Context(), asker, runner.RecordKey{Position: 1, Line: 1}, state.sent, state.assets, stats)
	if runner.Halting(err) {
		return err
	}

	if err != nil {
		// The exit code still comes from the error. This adds the fallback word the caller asked
		// for, so a shell guard reads a decision. An interrupt is skipped, since a transport record
		// written into the pipe the caller was closing reports a fault that never happened.
		if !errors.Is(err, context.Canceled) {
			record.Failure.Sentence = sentence(withKeyHint(err, settings, flags.mock))

			if writeErr := writeFailure(cmd, settings, outputMode, flags, inv, state.resolved, record); writeErr != nil {
				return writeErr
			}

			if rowShowsFailure(settings, flags, outputMode) {
				return &shownError{cause: err}
			}
		}

		return err
	}

	// Only a record that arrived is evaluated. A failed request would read as all zeros, so a gate
	// such as answer.value < 0.5 would hold for a request that never happened.
	record = runner.Judge(gate, abstain, record, stats)

	if flags.quiet {
		// Beside an assertion -q only silences the output. A gate of its own on top would make a
		// safety assertion such as value < 0.5 impossible to pass.
		if gate == nil {
			return quietResult(built.Questions[0], record.Answers[0].Answer)
		}

		return assertResult(record)
	}

	if writeErr := writeRecord(cmd, settings, outputMode, flags, inv, state.resolved, record); writeErr != nil {
		return writeErr
	}

	return assertResult(record)
}

type singleState struct {
	resolved input.Resolved
	sent     any
	assets   onesie.Assets
}

func assertResult(record output.Record) error {
	if record.AssertFailed {
		// A false assertion is the same statement a policy rejection makes over one answer, so it
		// takes the same error and the same exit code.
		return &rejectedError{}
	}

	if record.Abstained {
		return &abstainError{}
	}

	return nil
}

func writeFailure(
	cmd *cobra.Command,
	settings rootSettings,
	mode output.Mode,
	flags runConfig,
	inv *invocation,
	resolved input.Resolved,
	record output.Record,
) error {
	if flags.quiet {
		return nil
	}

	return writeRecord(cmd, settings, mode, flags, inv, resolved, record)
}

func rowShowsFailure(settings rootSettings, flags runConfig, mode output.Mode) bool {
	// Under --out the row goes to a file, so the terminal shows only the stderr line.
	return mode == output.Table && !flags.quiet && flags.out == "" && settings.stdoutTTY && settings.sharedTTY
}

func writeRecord(
	cmd *cobra.Command,
	settings rootSettings,
	mode output.Mode,
	flags runConfig,
	inv *invocation,
	resolved input.Resolved,
	record output.Record,
) error {
	if mode == output.Markdown {
		return output.WriteMarkdown(cmd.OutOrStdout(), record, markdownOptions(inv.Gate, inv.Abstain))
	}

	if mode == output.CSV || mode == output.TSV {
		return runner.DelimitedFor(cmd.OutOrStdout(), mode, inv.Plan, inv.Gate != nil, false, false).Write(record, nil, nil)
	}

	if merging(flags) {
		return writeMerged(cmd.OutOrStdout(), mode, record, resolved, flags)
	}

	if mode == output.Table {
		return output.WriteTable(
			cmd.OutOrStdout(), record, output.Width(settings.lookupEnv, settings.terminalWidth))
	}

	return output.Write(cmd.OutOrStdout(), mode, record)
}

func markdownOptions(gate, abstain *assert.Expr) output.MarkdownOptions {
	return output.MarkdownOptions{Assert: gate.Source(), Abstain: abstain.Source()}
}

func merging(flags runConfig) bool {
	return flags.merge || flags.mergeKey != ""
}

func writeMerged(
	w io.Writer,
	mode output.Mode,
	record output.Record,
	resolved input.Resolved,
	flags runConfig,
) error {
	return output.WriteMerged(w, mode, record, resolved.Raw, resolved.Wire, mergeKey(flags))
}

func mergeKey(flags runConfig) string {
	if flags.mergeKey != "" {
		return flags.mergeKey
	}

	return "answers"
}

func quietResult(question plan.Question, a *answer.Answer) error {
	if question.Shape == plan.Noul {
		// The threshold defaults to a half, so a yes or no question is meaningful under -q with no
		// policy at all, unlike a pick question which has nothing to compare.
		threshold := 0.5
		if question.Policy.Threshold != nil {
			threshold = *question.Policy.Threshold
		}

		probability, ok := a.Value.(float64)
		if ok && probability >= threshold {
			return nil
		}

		return &rejectedError{}
	}

	// A low confidence fallback exits zero in every other output mode. Under -q it is the one
	// signal the exit code has, so it reports rejection instead.
	if a.Fallback == "low_confidence" {
		return &rejectedError{}
	}

	return nil
}

func outputName(flags runConfig) string {
	if flags.raw {
		return "raw"
	}

	return flags.output
}

type runFlags struct {
	version     bool
	provider    string
	providerSet bool
	profile     string
	profileSet  bool
	mock        string
	out         string
	resume      bool
	prune       bool
	output      string
	raw         bool
	quiet       bool
	usage       bool
	input       string
	state       string
	stateFile   string
	model       string
	baseURL     string
	allowHTTP   bool
	file        string
	replace     bool

	assert    []string
	abstainIf []string

	printQuestions bool
	printRequest   bool
	printSchema    bool
	listModels     bool
	stats          bool
	cache          bool

	jobs          int
	timeout       int
	retries       int
	maxRetryAfter int
	unordered     bool
	stopOnError   bool
	stopOnAssert  bool
	skipBlank     bool
	noDedup       bool
	merge         bool
	mergeKey      string
	mapSource     string
	idSource      string
	images        []string
	imageFields   []string
	longContext   bool
}
