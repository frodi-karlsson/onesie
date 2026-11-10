package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/frodi-karlsson/onesie/internal/settingrow"
	"github.com/frodi-karlsson/onesie/internal/wording"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

const quietDefaultThreshold = 0.5

var errWrapRepeated = errors.New("--merge wraps an input that is not an object under 'state', " +
	"so --merge-key state would repeat that key. Pass another --merge-key")

func validated(
	cmd *cobra.Command, settings rootSettings, events []argv.Event, positional string, parsed *runFlags,
) (staged, error) {
	early, err := setting.ParseRun(settingrow.Table(), parsed.given, settingrow.CommandRoot)
	if err != nil {
		return staged{}, err
	}

	flags, err := newRunConfig(early, parsed.given, events, positional)
	if err != nil {
		return staged{}, err
	}

	flags.stdoutTTY = settings.stdoutTTY

	// Both modes sit ahead of the plan, since neither has a question and failing for want of one
	// would send the user to the wrong flag. They validate with no plan.
	if setting.Get(flags.merged, setting.ListModels) || flags.inputMode == input.Request {
		merged, mergeErr := mergeRun(settings, flags, parsed.given, settingrow.CommandRoot, nil)
		if mergeErr != nil {
			return staged{}, mergeErr
		}

		_, _, typedTwice, validateErr := validateRun(merged.config, runFacts{}, nil,
			commandLine{given: parsed.given, args: settings.args})
		if validateErr != nil {
			return staged{}, validateErr
		}

		if merged.refusal != nil {
			return staged{}, merged.refusal
		}

		return staged{config: merged.config}, announce(cmd.ErrOrStderr(), append(typedTwice, merged.notices...))
	}

	ready, err := assemble(settings, flags, parsed.given, settingrow.CommandRoot, false)

	// Warnings print whether or not validation succeeded, so a run that fails for one reason still
	// reports the others.
	if warnErr := warnAll(cmd.ErrOrStderr(), ready.warnings); warnErr != nil {
		return staged{}, warnErr
	}

	if err != nil {
		return staged{}, err
	}

	return staged{config: ready.config, plan: ready.plan, file: ready.file}, announce(cmd.ErrOrStderr(), ready.notices)
}

func announce(w io.Writer, notices []setting.Notice) error {
	for _, notice := range notices {
		if err := output.Notice(w, output.Printable(notice.String())); err != nil {
			return err
		}
	}

	return nil
}

type staged struct {
	config runConfig
	plan   *plan.Plan
	file   buildFile
}

func run(
	cmd *cobra.Command,
	settings rootSettings,
	ready staged,
	out *answersfile.File,
	resume answersfile.Resume,
) error {
	flags := ready.config

	if setting.Get(flags.merged, setting.ListModels) {
		return runListModels(cmd, settings, flags, out)
	}

	if flags.inputMode == input.Request {
		return runRequestBodies(cmd, settings, flags, out, resume)
	}

	inv, err := invocationOf(settings, flags, ready.plan, ready.file)
	if err != nil {
		return err
	}

	// Before the output mode, because a question file is not an output mode and -o has no meaning
	// for it. After validation, because a dry run that accepted a plan the real run would refuse
	// would be worse than useless.
	if setting.Get(flags.merged, setting.PrintQuestions) {
		out.BindWithoutFingerprint()

		return printQuestions(
			cmd.OutOrStdout(), cmd.ErrOrStderr(), inv.plan.Questions, inv.gate.Source(),
			inv.abstain.Source(), inv.loaded, settings.schemaURL)
	}

	return runPlan(cmd, settings, flags, inv, out, resume)
}

func runListModels(cmd *cobra.Command, settings rootSettings, flags runConfig, out *answersfile.File) error {
	out.BindWithoutFingerprint()

	return withStats(cmd, settings.now, flags, func(stats *collector) error {
		return listModels(cmd, settings, flags, stats)
	})
}

func runRequestBodies(
	cmd *cobra.Command, settings rootSettings, flags runConfig, out *answersfile.File, resume answersfile.Resume,
) error {
	// Each body names its own model or takes the run's, and the fingerprint holds no model, as
	// it holds nothing else a body carries.
	if bindErr := bindOut(out, settings, flags, nil, answersfile.FingerprintInputs{
		Input: flags.inputMode.String(), LongContext: flags.env.longContext, Usage: setting.Get(flags.merged, setting.Usage),
	}); bindErr != nil {
		return bindErr
	}

	return withStats(cmd, settings.now, flags, func(stats *collector) error {
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
	built, gate, abstain, inputMode := inv.plan, inv.gate, inv.abstain, flags.inputMode

	model, err := resolveModel(settings, flags, built.Model)
	if err != nil {
		return err
	}

	// Loaded before any mode writes, so a file that does not match the questions leaves no line.
	answers, err := answersFor(cmd, settings, flags, built, built.Questions)
	if err != nil {
		return err
	}

	outputMode, err := output.ParseMode(setting.Derived(setting.NewView(flags.merged, built, setting.Facts{StdoutTTY: flags.stdoutTTY}), setting.Output))
	if err != nil {
		return err
	}

	// After the plan is built, since the fingerprint covers its questions, and before any mode
	// writes, since a refused resume must leave the file as it was.
	bindErr := bindOut(out, settings, flags, built.Questions, answersfile.FingerprintInputs{
		Model: model, Output: outputMode.String(), Input: inputMode.String(), Assert: gate.Source(),
		AbstainIf: abstain.Source(), Images: inv.images.fingerprint(), LongContext: built.LongContext,
		Usage: setting.Get(flags.merged, setting.Usage),
	})
	if bindErr != nil {
		return bindErr
	}

	if inputMode.Streaming() {
		if noteErr := noteOverriddenBodyState(
			cmd.ErrOrStderr(), inv.loaded, inv.images, plan.WireAll(built.Questions)); noteErr != nil {
			return noteErr
		}

		if refuseErr := checkProviderNeeds(settings, flags, built, inv.images); refuseErr != nil {
			return refuseErr
		}

		return withStats(cmd, settings.now, flags, func(stats *collector) error {
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
	answers answererFactory,
) error {
	built := inv.plan

	resolved, err := resolveState(cmd.Context(), settings, flags, inv.loaded, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	if requests(flags) && resolved.Source == input.SourceNone && len(flags.list("image")) == 0 {
		return noState(resolved)
	}

	sent := resolved.Wire
	if resolved.Source != input.SourceNone {
		sent, err = mapped(cmd.Context(), inv.mapper, resolved.State, resolved.Wire)
		if err != nil {
			return err
		}
	}

	sent, assets, err := inv.images.attach(sentOrNil(resolved, sent))
	if err != nil {
		return err
	}

	if len(flags.list("image")) > 0 {
		resolved, err = imagedState(sent)
		if err != nil {
			return err
		}
	}

	// After the state is resolved, so the body carries the state a real run would send.
	if setting.Get(flags.merged, setting.PrintRequest) {
		chosen, err := connected(settings, flags)
		if err != nil {
			return err
		}

		model := chosen.modelFor(built.Model)
		req := requestFor(built, model, plan.WireAll(built.Questions), sent, assets)

		return printRequest(cmd.OutOrStdout(), chosen.provider, req, resolved.Source != input.SourceNone)
	}

	// Checked here rather than at write time, so a taken key costs no request.
	if merging(flags) && hasKey(resolved.State, mergeKey(flags)) {
		return fmt.Errorf(
			"--merge would overwrite the input's '%s' key. Pass --merge-key",
			mergeKey(flags))
	}

	if merging(flags) && wrapsUnderKey(resolved.Raw, resolved.Wire, mergeKey(flags)) {
		return errWrapRepeated
	}

	return withStats(cmd, settings.now, flags, func(stats *collector) error {
		return ask(cmd, settings, flags, inv, singleState{resolved: resolved, sent: sent, assets: assets},
			outputMode, stats, answers)
	})
}

func noState(resolved input.Resolved) error {
	if resolved.EmptyStdin {
		return errors.New("no state given, since stdin was empty. Pipe one to stdin, or pass --state")
	}

	return errors.New("no state given. Pipe one to stdin, or pass --state")
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

func noteOverriddenBodyState(w io.Writer, loaded *qfile.File, images *imaging, questions onesie.Questions) error {
	if loaded == nil || !loaded.HasState {
		return nil
	}

	dropped, err := images.dropUnreferenced(questions)
	if err != nil {
		return err
	}

	note := ""
	if dropped > 0 {
		note = " and its " + wording.Plural(dropped, "asset", "assets")
	}

	return output.Notice(w, "the input records override the request body's state"+note)
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
	stdinTTY := settings.stdinTTY || len(flags.list("image")) > 0

	return input.Resolve(ctx, input.Query{
		Mode:     flags.inputMode,
		Stdin:    settings.stdin,
		StdinTTY: stdinTTY,
		State:    setting.Get(flags.merged, setting.State),
		HasState: flags.typed(setting.State.Name()),
		Hint:     hint,
		Clock:    settings.stdinClock,
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
	chosen, err := connected(settings, flags)
	if err != nil {
		return answersfile.FingerprintInputs{}, err
	}

	inputs.Provider = chosen.provider.Name()
	inputs.MapSource = setting.Get(flags.merged, setting.Map)
	inputs.IDSource = setting.Get(flags.merged, setting.ID)

	if merging(flags) {
		inputs.MergeKey = mergeKey(flags)
	}

	// A resume by position skips one record per stored line, and --skip-blank changes which record
	// a line stands for everywhere but csv and tsv, which skip a blank line either way.
	inputs.SkipBlank = setting.Get(flags.merged, setting.SkipBlank) && !resumesByID(flags) && !delimitedInput(inputs.Input)
	inputs.Mock = flags.mockPath != ""
	inputs.Delimiter = flags.parsedDelimiter
	inputs.Profile = chosen.profile

	// A mock run ignores the base URL and the account.
	if !inputs.Mock {
		inputs.BaseURL, inputs.AccountID = endpointOf(chosen)
	}

	return inputs, nil
}

func endpointOf(chosen resolution) (baseURL, account string) {
	resolved, source, err := chosen.provider.ResolveBaseURL(chosen.baseURL, chosen.accountID, noEnvironment)

	switch {
	case err != nil:
		// The client refuses the same base URL before any request, so this fingerprint is never kept.
		return strings.TrimSpace(chosen.baseURL), ""
	case source == onesie.BaseURLFromAccount:
		return "", chosen.accountID
	case chosen.provider.CustomBaseURL(resolved) || chosen.provider.EnvAccountID() != "":
		// A cloudflare account URL given whole is the provider's own, yet it names the account.
		return resolved, ""
	default:
		return "", ""
	}
}

func resumesByID(flags runConfig) bool {
	return setting.Get(flags.merged, setting.ID) != ""
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

func watched(stop func(line) bool, stopped *atomic.Bool) func(line) bool {
	if stop == nil {
		return nil
	}

	return func(l line) bool {
		if !stop(l) {
			return false
		}

		stopped.Store(true)

		return true
	}
}

func billOnce(l *line) {
	if l.call == nil {
		return
	}

	if !l.call.billed {
		l.call.billed = true

		return
	}

	// A group made one request, so only its first written line reports the tokens, and a sum over
	// the output stays true. An error line that carried no usage keeps carrying none.
	if l.record.Usage != nil {
		l.record.Usage = &onesie.Usage{}
	}
}

func mapped(ctx context.Context, mapper *jq.Expr, state, wire any) (any, error) {
	if mapper == nil {
		return wire, nil
	}

	value, err := mapper.One(ctx, jqValue(state, wire))
	if err != nil {
		return nil, fmt.Errorf("--map %w", err)
	}

	if checkErr := input.CheckState(value); checkErr != nil {
		return nil, fmt.Errorf("--map: %w", checkErr)
	}

	encoded, err := jq.Marshal(value, limits.MaxLineBytes)
	if err != nil {
		return nil, fmt.Errorf("--map: %w", err)
	}

	return json.RawMessage(encoded), nil
}

func interrupted(ctx context.Context) bool {
	return ctx.Err() != nil
}

func jqValue(state, wire any) any {
	raw, isJSON := wire.(json.RawMessage)
	if !isJSON {
		return state
	}

	// Decoded again with numbers kept as written, since the parsed state holds float64 and a
	// large id would lose digits on its way through the expression.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return state
	}

	return value
}

func rowLine(record output.Record, rec namedRecord) line {
	var fields map[string]any
	if object, ok := rec.State.(map[string]any); ok {
		fields = object
	}

	record.ID = rec.id

	return line{record: record, raw: rec.Raw, state: rec.Wire, header: rec.Header, fields: fields}
}

func wrapsUnderKey(raw string, wire any, key string) bool {
	return key == setting.WrapKey && output.Wraps(raw, wire)
}

func hasKey(state any, key string) bool {
	object, ok := state.(map[string]any)
	if !ok {
		return false
	}

	_, exists := object[key]

	return exists
}

func compact(raw string) []byte {
	var flat bytes.Buffer

	if err := json.Compact(&flat, []byte(raw)); err != nil {
		// Unreachable for a line that parsed, the only kind compact is called with.
		return []byte(raw)
	}

	return flat.Bytes()
}

func stopping(stops stopSet) func(line) bool {
	if !stops.judges() {
		// Nil rather than a predicate that always says no, so the engine asks nothing of a run
		// that did not pass the flag.
		return nil
	}

	return func(l line) bool {
		// An error stops the run through the engine's StopOnError, which also gives it the error's
		// exit code.
		verdict := l.record.Verdict()

		return verdict != output.Error && stops.has(verdict)
	}
}

type line struct {
	record output.Record
	raw    string
	state  any // The record as read, before --map, so --merge keeps a text line's type and a JSON line's digits.
	header []string
	fields map[string]any
	slot   int
	halted bool
	call   *sharedCall
}

func streamResult(result engine.Result, judged output.Tally) error {
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

	// After the errors and before the successful return. A stream carrying both exits 6, since a
	// record that never answered says more than a gate that answered no.
	if judged[output.Fail] > 0 {
		return &failError{}
	}

	// Last, since a gate that could not decide says less than one that answered no.
	if judged[output.Unsure] > 0 {
		return &abstainError{}
	}

	return nil
}

func stoppedAtInput(cause error) bool {
	// --stop-on=error ended the run at a record that never became a request. The exit code table
	// has no code of its own for that, so it exits 6 as the same record does without the flag.
	return !aborting(cause) && !errors.Is(cause, context.Canceled) && classify(cause).kind == output.KindInput
}

func aborting(err error) bool {
	// Every later record would fail the same way, so a bad key is reported on one line for the
	// whole run.
	return authFailure(err) || halting(err) || repeatsKey(err)
}

func repeatsKey(err error) bool {
	var repeated *repeatedKeyError

	return errors.As(err, &repeated)
}

type repeatedKeyError struct {
	cause error
}

func (e *repeatedKeyError) Error() string {
	return e.cause.Error()
}

func (e *repeatedKeyError) Unwrap() error {
	return e.cause
}

func ask(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	inv *invocation,
	state singleState,
	outputMode output.Mode,
	stats *collector,
	answerers answererFactory,
) error {
	asker, err := answerers(cmd.Context(), stats)
	if err != nil {
		return err
	}

	built, gate, abstain := inv.plan, inv.gate, inv.abstain

	questions := plan.WireAll(built.Questions)

	chosen, err := connected(settings, flags)
	if err != nil {
		return err
	}

	record, _, err := askAndRecord(cmd.Context(), asking{
		provider: chosen.provider, asker: asker, built: built, model: chosen.modelFor(built.Model), questions: questions,
		withUsage: setting.Get(flags.merged, setting.Usage), stats: stats,
	}, recordKey{position: 1, line: 1}, state.sent, state.assets)
	if halting(err) {
		return err
	}

	if err != nil {
		// The exit code still comes from the error. This adds the fallback word the caller asked
		// for, so a shell guard reads a decision. An interrupt is skipped, since a transport record
		// written into the pipe the caller was closing reports a fault that never happened.
		if !errors.Is(err, context.Canceled) {
			record.Failure.Sentence = sentence(withKeyHint(err, settings, flags))

			if writeErr := writeFailure(cmd, settings, outputMode, flags, inv, state.resolved, record); writeErr != nil {
				return writeErr
			}

			if rowShowsFailure(settings, flags, outputMode) {
				return &shownError{cause: err}
			}
		}

		return err
	}

	quiet := setting.Get(flags.merged, setting.Quiet)

	// Only a record that arrived is evaluated. A failed request would read as all zeros, so a gate
	// such as answer.value < 0.5 would hold for a request that never happened.
	record = judge(askedDecider(gate, abstain, quiet, built.Questions[0]), record)
	stats.judged(record.Gate)

	if quiet {
		return verdictResult(record.Verdict())
	}

	if writeErr := writeRecord(cmd, settings, outputMode, flags, inv, state.resolved, record); writeErr != nil {
		return writeErr
	}

	return verdictResult(record.Verdict())
}

type singleState struct {
	resolved input.Resolved
	sent     any
	assets   onesie.Assets
}

func verdictResult(verdict output.Verdict) error {
	switch verdict {
	case output.Fail:
		// A false assertion is the same statement -q makes when it judges one answer no, so it
		// takes the same error and the same exit code.
		return &failError{}
	case output.Unsure:
		return &abstainError{}
	default:
		return nil
	}
}

func judge(decide decider, record output.Record) output.Record {
	record.Gate = decide(record)

	return record
}

type decider func(output.Record) output.Verdict

func askedDecider(gate, abstain *assert.Expr, quiet bool, first plan.Question) decider {
	// Beside an assertion -q only silences the output. A gate of its own on top would make a
	// safety assertion such as value < 0.5 impossible to pass.
	if quiet && gate == nil {
		return quietly(first)
	}

	return gated(gate, abstain)
}

func gated(gate, abstain *assert.Expr) decider {
	return func(record output.Record) output.Verdict {
		switch {
		case gate == nil:
			return output.Answered
		case assert.Eval(gate, record):
			return output.Pass
		// Eval holds for a nil expression, which is right for a missing gate and wrong here, where a
		// missing abstain expression leaves every false assertion a no.
		case abstain != nil && assert.Eval(abstain, record):
			return output.Unsure
		default:
			return output.Fail
		}
	}
}

func quietly(question plan.Question) decider {
	return func(record output.Record) output.Verdict {
		if quietAccepts(question, record.Answers[0].Answer) {
			return output.Pass
		}

		return output.Fail
	}
}

func quietAccepts(question plan.Question, a *answer.Answer) bool {
	if question.Shape == plan.Noul {
		// The threshold defaults to a half, so a yes or no question is meaningful under -q with no
		// policy at all, unlike a pick question which has nothing to compare.
		threshold := quietDefaultThreshold
		if question.Policy.Threshold != nil {
			threshold = *question.Policy.Threshold
		}

		probability, ok := a.Value.(float64)

		return ok && probability >= threshold
	}

	// A low confidence fallback exits zero in every other output mode. Under -q it is the one
	// signal the exit code has, so it reports a fail instead.
	return a.Fallback != "low_confidence"
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
	if setting.Get(flags.merged, setting.Quiet) {
		return nil
	}

	return writeRecord(cmd, settings, mode, flags, inv, resolved, record)
}

func rowShowsFailure(settings rootSettings, flags runConfig, mode output.Mode) bool {
	// Under --out the row goes to a file, so the terminal shows only the stderr line.
	return mode == output.Table && !setting.Get(flags.merged, setting.Quiet) && setting.Get(flags.merged, setting.Out) == "" && settings.stdoutTTY && settings.sharedTTY
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
		return output.WriteMarkdown(cmd.OutOrStdout(), record, markdownOptions(inv.gate, inv.abstain))
	}

	if mode == output.CSV || mode == output.TSV {
		return delimited(cmd.OutOrStdout(), mode, inv.plan, inv.gate != nil, false, false, flags.parsedDelimiter.Comma).
			Write(record, nil, nil)
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

func markdown(
	out io.Writer,
	mode output.Mode,
	built *plan.Plan,
	withGate bool,
	withID bool,
) *output.MarkdownTable {
	if mode != output.Markdown {
		return nil
	}

	ids := make([]string, 0, len(built.Questions))
	for _, question := range built.Questions {
		ids = append(ids, question.ID)
	}

	return output.NewMarkdownTable(out, output.MarkdownTableOptions{IDs: ids, ID: withID, Gate: withGate})
}

func delimited(
	out io.Writer,
	mode output.Mode,
	built *plan.Plan,
	withAssert bool,
	withID bool,
	headerWritten bool,
	comma rune,
) *output.Delimited {
	if mode != output.CSV && mode != output.TSV {
		return nil
	}

	ids := make([]string, 0, len(built.Questions))
	for _, question := range built.Questions {
		ids = append(ids, question.ID)
	}

	return output.NewDelimited(out, mode, output.DelimitedOptions{
		IDs: ids, Boxes: boxIDs(built.Questions), Fallbacks: fallbackIDs(built.Questions), ID: withID,
		Assert: withAssert, Header: !headerWritten, Comma: comma,
	})
}

func boxIDs(questions []plan.Question) []string {
	var ids []string

	for _, question := range questions {
		if question.Shape == plan.Box {
			ids = append(ids, question.ID)
		}
	}

	return ids
}

func fallbackIDs(questions []plan.Question) []string {
	var ids []string

	for _, question := range questions {
		if question.Policy.Fallback != nil {
			ids = append(ids, question.ID)
		}
	}

	return ids
}

func merging(flags runConfig) bool {
	return setting.Get(flags.merged, setting.MergeInput) || setting.Get(flags.merged, setting.MergeKey) != ""
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
	if setting.Get(flags.merged, setting.MergeKey) != "" {
		return setting.Get(flags.merged, setting.MergeKey)
	}

	return "answers"
}

func askAndRecord(
	ctx context.Context, a asking, key recordKey, state any, assets onesie.Assets,
) (output.Record, *sharedCall, error) {
	body, err := a.provider.Prepare(requestFor(a.built, a.model, a.questions, state, assets))
	if err != nil {
		refused := a.refused(key, err)

		// No request stood behind it, as with -i request and a body the provider refuses.
		a.stats.recordFailure(refused, false, 0)

		return failureRecord(a.built, refused), nil, refused
	}

	ready := newPrepared(body)

	// A record the mock file does not answer shares with nothing, so its error names its own line.
	group := a.shared

	salt, shares := a.asker.salt(key)
	if !shares {
		group = nil
	}

	return askOnce(ctx, group, a.stats, ready.key(salt), func() (output.Record, error) {
		return counted(ctx, a, key, ready)
	})
}

type asking struct {
	provider  onesie.Provider
	asker     answerer
	built     *plan.Plan
	model     string
	questions onesie.Questions
	withUsage bool
	stats     *collector
	shared    *dedup
	numbered  bool
}

func (a asking) refused(key recordKey, err error) error {
	if !a.numbered {
		return err
	}

	return &input.LineError{Line: key.line, Err: embed("", err)}
}

func counted(ctx context.Context, a asking, key recordKey, ready prepared) (output.Record, error) {
	record, usage, cached, err := answered(ctx, a.asker, key, a.built, ready, a.withUsage)
	if halting(err) {
		// No request stood behind it, so it is a record and not a request, and no attempt ended.
		a.stats.recordFailure(err, false, 0)

		return record, err
	}

	if err != nil {
		// The request was made whatever went wrong afterwards, and the questions went with it, so a
		// failed record still carries them into the count --stats reports.
		a.stats.recordFailure(err, true, len(a.questions))
		a.stats.terminalAttempt(err)
		a.stats.spend(usage)

		return record, err
	}

	if cached {
		a.stats.cachedRecord(record.Model)

		return record, nil
	}

	a.stats.record(record.Model, usage, len(a.questions))

	return record, nil
}

func answered(
	ctx context.Context, asker answerer, key recordKey, built *plan.Plan, ready prepared, withUsage bool,
) (output.Record, onesie.Usage, bool, error) {
	got, err := asker.answer(ctx, key, ready)
	if halting(err) {
		return failureRecord(built, err), onesie.Usage{}, false, err
	}

	if err != nil {
		failed := failureRecord(built, err)

		var unusable *onesie.ResponseError
		if !errors.As(err, &unusable) || unusable.Usage == nil {
			return failed, onesie.Usage{}, false, err
		}

		if withUsage {
			failed = spent(failed, unusable.Usage)
		}

		return failed, *unusable.Usage, false, err
	}

	result := got.result
	record := output.Record{Model: result.Model}
	if withUsage {
		// A copy, since a pointer into the result would keep the whole response alive for as long
		// as the record is held, and a stream holds one per distinct request.
		usage := result.Usage
		record.Usage = &usage
	}

	normalized := answer.NormalizeAll(built.Questions, result)

	for i, question := range built.Questions {
		answer.Apply(question, normalized[i])

		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: normalized[i], Box: question.Shape == plan.Box})
	}

	return record, result.Usage, got.cached, nil
}

func requestFor(
	built *plan.Plan, model string, questions onesie.Questions, state any, assets onesie.Assets,
) onesie.Request {
	return onesie.Request{
		State: state, Model: model, Questions: questions, Assets: assets, LongContext: built.LongContext,
	}
}

func spent(failed output.Record, usage *onesie.Usage) output.Record {
	// A 200 onesie could not use still cost its tokens, so --usage reports them on the error line.
	failed.Usage = usage

	return failed
}

func failureRecord(built *plan.Plan, cause error) output.Record {
	record := output.Record{Failure: describe(cause)}

	for _, question := range built.Questions {
		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: answer.Failed(question), Box: question.Shape == plan.Box})
	}

	return record
}

func describe(cause error) *output.Failure {
	found := classify(cause)

	return &output.Failure{Kind: found.kind, Status: found.status, Message: text(cause)}
}

type runFlags struct {
	provider  string
	profile   string
	mock      string
	out       string
	resume    bool
	prune     bool
	output    string
	quiet     bool
	usage     bool
	input     string
	state     string
	model     string
	baseURL   string
	allowHTTP bool
	file      string
	replace   bool

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
	stopOn        string
	skipBlank     bool
	noDedup       bool
	merge         bool
	mergeKey      string
	delimiter     string
	mapSource     string
	idSource      string
	images        []string
	imageFields   []string
	longContext   bool

	given *setting.Given
}

func (f *runFlags) providerChosen() bool {
	return f.typed(setting.Provider.Name())
}

func (f *runFlags) profileChosen() bool {
	return f.typed(setting.Profile.Name())
}

func (f *runFlags) typed(name settingrow.Name) bool {
	return f.given.Count(name) > 0
}
