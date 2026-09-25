package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func run(
	cmd *cobra.Command,
	settings rootSettings,
	events []argv.Event,
	positional string,
	flags *runFlags,
	out *outFile,
) error {
	cfg, inputMode, err := configOf(cmd, events, positional, flags)
	if err != nil {
		return err
	}

	// Both branches sit ahead of the plan, since neither has a question and failing for want of one
	// would send the user to the wrong flag. --list-models comes first, so it names itself rather
	// than the mode it was combined with.
	if flags.listModels {
		if checkErr := checkFlags(cmd, cfg); checkErr != nil {
			return checkErr
		}

		out.bindWithoutFingerprint()

		return withStats(cmd, settings.now, flags, func(stats *collector) error {
			return listModels(cmd, settings, stats)
		})
	}

	if inputMode == input.Request {
		if checkErr := checkFlags(cmd, cfg); checkErr != nil {
			return checkErr
		}

		// Each body names its own model, and onesie sends it as written, so the fingerprint holds
		// no model for the default to fill.
		if bindErr := bindOut(out, settings, flags, nil, fingerprintInputs{input: inputMode.String()}); bindErr != nil {
			return bindErr
		}

		return withStats(cmd, settings.now, flags, func(stats *collector) error {
			return streamRaw(cmd, settings, flags, stats, out)
		})
	}

	inv, warnings, err := build(settings, cfg, events, positional, flags, false)

	// Warnings print whether or not validation succeeded, so a run that fails for one reason still
	// reports the others.
	for _, warning := range warnings {
		if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), warning); printErr != nil {
			return printErr
		}
	}

	if err != nil {
		return err
	}

	built := inv.plan
	gate := inv.gate
	abstain := inv.abstain
	mapper := inv.mapper
	namer := inv.namer
	loaded := inv.loaded

	// Before the output mode, because a question file is not an output mode and -o has no meaning
	// for it. After validation, because a dry run that accepted a plan the real run would reject
	// would be worse than useless.
	if flags.printQuestions {
		out.bindWithoutFingerprint()

		return printQuestions(
			cmd.OutOrStdout(), cmd.ErrOrStderr(), built.Questions, gate.Source(),
			abstain.Source(), loaded)
	}

	model, err := resolveModel(settings, flags, built.Model)
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
	bindErr := bindOut(out, settings, flags, built.Questions, fingerprintInputs{
		model: model, output: outputMode.String(), input: inputMode.String(), assert: gate.Source(),
		abstainIf: abstain.Source(),
	})
	if bindErr != nil {
		return bindErr
	}

	if inputMode.Streaming() {
		return withStats(cmd, settings.now, flags, func(stats *collector) error {
			return stream(
				cmd, settings, built, mapper, namer, inputMode, outputMode, flags, gate, abstain, stats,
				out)
		})
	}

	resolved, err := input.Resolve(input.Query{
		Mode:         inputMode,
		Stdin:        settings.stdin,
		StdinTTY:     settings.stdinTTY,
		State:        flags.state,
		HasState:     cfg.HasState,
		StateFile:    flags.stateFile,
		HasStateFile: cfg.HasStateFile,
		ReadFile:     settings.readFile,
	})
	if err != nil {
		return err
	}

	// Resolve reporting no source is exactly the case where stdin, --state and --state-file all
	// supplied nothing, which is when a body's own state gets its turn.
	if resolved.Source == input.SourceNone && loaded != nil && loaded.HasState {
		resolved = input.Resolved{
			Source: input.SourceBody,
			State:  loaded.State,
			Raw:    string(loaded.StateWire),
			// The ordered bytes the loader kept, rather than a re-marshalled Go map, so the body
			// reaches the wire in the order it was written.
			Wire: loaded.StateWire,
		}

		if checkErr := input.CheckState(resolved.State); checkErr != nil {
			return fmt.Errorf("onesie: %w", checkErr)
		}
	}

	if requests(flags) && resolved.Source == input.SourceNone {
		return errors.New(
			"onesie: no state given. Pipe one to stdin, or pass --state or --state-file")
	}

	sent := resolved.Wire
	if resolved.Source != input.SourceNone {
		sent, err = mapped(cmd.Context(), mapper, resolved.State, resolved.Wire)
		if err != nil {
			return fmt.Errorf("onesie: %w", err)
		}
	}

	// After the state is resolved, so the body carries the state a real run would send.
	if flags.printRequest {
		model, err := resolveModel(settings, flags, built.Model)
		if err != nil {
			return err
		}

		return printRequest(cmd.OutOrStdout(), built.Questions, resolved.Source, sent, model)
	}

	// Checked here rather than at write time, so a taken key costs no request. Section 11 opens
	// with every check running before any network call.
	if merging(flags) && hasKey(resolved.State, mergeKey(flags)) {
		return fmt.Errorf(
			"onesie: --merge would overwrite the input's '%s' key. Pass --merge-key",
			mergeKey(flags))
	}

	return withStats(cmd, settings.now, flags, func(stats *collector) error {
		return ask(cmd, settings, built, resolved, sent, outputMode, flags, gate, abstain, stats)
	})
}

func bindOut(
	out *outFile,
	settings rootSettings,
	flags *runFlags,
	questions []plan.Question,
	inputs fingerprintInputs,
) error {
	if out == nil {
		return nil
	}

	provider, err := resolveProvider(settings, flags)
	if err != nil {
		return err
	}

	inputs.provider = provider.Name
	inputs.mapSource = flags.mapSource
	inputs.idSource = flags.idSource

	if merging(flags) {
		inputs.mergeKey = mergeKey(flags)
	}

	fingerprint, err := fingerprintOf(questions, inputs)
	if err != nil {
		return err
	}

	return out.bind(fingerprint)
}

func checkFlags(cmd *cobra.Command, cfg plan.Config) error {
	warning, err := plan.CheckFlags(cfg)

	// No rule warns for either of the two paths that call this today, so the print is here for a
	// warning a later rule may add. Validate prints the same warnings on the path that builds a
	// plan, and no run reaches both, so nothing is reported twice.
	if warning != "" {
		if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), warning); printErr != nil {
			return printErr
		}
	}

	return err
}

func asked(events []argv.Event) bool {
	for _, event := range events {
		if event.Name == "ask" {
			return true
		}
	}

	return false
}

func eventNames(events []argv.Event) []string {
	names := make([]string, 0, len(events))
	for _, event := range events {
		names = append(names, event.Name)
	}

	return names
}

func inputName(flag string) string {
	// Reachable only by passing -i with an empty value, since the flag defaults to text. Every
	// message that names the mode would otherwise read -i with nothing after it.
	if flag == "" {
		return "text"
	}

	return flag
}

func stream(
	cmd *cobra.Command,
	settings rootSettings,
	built *plan.Plan,
	mapper *jq.Expr,
	namer *jq.Expr,
	inputMode input.Mode,
	outputMode output.Mode,
	flags *runFlags,
	gate *assert.Expr,
	abstain *assert.Expr,
	stats *collector,
	answers *outFile,
) error {
	if flags.printRequest {
		return streamRequests(cmd, settings, built, mapper, namer, inputMode, flags)
	}

	client, err := settings.newClient(cmd.Context(), observing(stats)...)
	if err != nil {
		return err
	}

	questions := wireAll(built.Questions)

	model, err := resolveModel(settings, flags, built.Model)
	if err != nil {
		return err
	}

	book, err := resumeLedger(cmd.Context(), answers, flags, namer, outputMode, gate != nil)
	if err != nil {
		return err
	}

	stored, err := resumedVerdicts(cmd.Context(), answers, namer, answersFormat{
		mode: outputMode, merge: merging(flags), mergeKey: mergeKey(flags), gated: gate != nil,
	}, flags)
	if err != nil {
		return err
	}

	source := records(cmd.Context(), settings, inputMode, flags, namer, book, stored, outputMode)
	out := cmd.OutOrStdout()
	merge := merging(flags)
	table := delimited(out, outputMode, built, gate != nil, namer != nil && !merge, flags)
	md := markdown(out, outputMode, built, gate != nil, namer != nil)

	evaluateOne := func(ctx context.Context, rec namedRecord) (line, error) {
		if rec.Err != nil {
			// A line onesie could not read is a record that never became a request, and it
			// carried no questions to the wire either.
			stats.recordFailure(rec.Err, false, 0)

			return line{record: failureRecord(built, rec.Err), raw: rec.Raw, header: rec.Header}, rec.Err
		}

		if interrupted(ctx) {
			return line{}, ctx.Err()
		}

		if rec.idErr != nil {
			bad := &input.LineError{Line: rec.Line, Err: rec.idErr}
			stats.recordFailure(bad, false, 0)

			return rowLine(failureRecord(built, bad), rec), bad
		}

		// -o csv puts the answers in columns, so there is no merge key to overwrite.
		if merge && table == nil && hasKey(rec.State, mergeKey(flags)) {
			stats.recordFailure(nil, false, 0)

			// Detected here rather than inside Write, since the engine counts a failure from
			// the evaluator's error and a rewrite inside Write would count as a success.
			//
			// A LineError gets kind input rather than the transport default from describe, so
			// --stop-on-error reaches the row, and the line number comes along without a onesie
			// prefix inside the JSON.
			taken := &input.LineError{
				Line: rec.Line,
				Err: fmt.Errorf(
					"--merge would overwrite the input's '%s' key, pass --merge-key",
					mergeKey(flags)),
			}

			return line{
				record: failureRecord(built, taken),
				// raw is deliberately left empty, which is what sends merge down the wrapper
				// path rather than the splice. The object goes under state as raw JSON rather
				// than a Go map, so its key order survives.
				state: json.RawMessage(compact(rec.Raw)),
			}, taken
		}

		sent, mapErr := mapped(ctx, mapper, rec.State, rec.Wire)
		if interrupted(ctx) {
			return line{}, ctx.Err()
		}

		if mapErr != nil {
			bad := &input.LineError{Line: rec.Line, Err: mapErr}
			stats.recordFailure(bad, false, 0)

			return rowLine(failureRecord(built, bad), rec), bad
		}

		record, evalErr := evaluate(
			ctx, client, built, model, questions, sent, flags.usage, stats)
		if evalErr != nil {
			// Returned before the gate is asked, per §17.4's third row. A failed record reads
			// as all zeros, so a gate such as answer.value < 0.5 would hold for a request that
			// never happened.
			return rowLine(record, rec), evalErr
		}

		// §17.6 asks the gate per record. A false assertion or an abstain is not an engine
		// failure: the record succeeded and the answer is complete, so the outcome is carried
		// on the line rather than returned as an error the engine would count against
		// result.Failed.
		record = judge(gate, abstain, record, stats)

		return rowLine(record, rec), nil
	}

	write := func(l line) error {
		if md != nil {
			return md.Write(l.record)
		}

		if table != nil {
			if !merge {
				return table.Write(l.record, nil, nil)
			}

			return table.Write(l.record, l.header, l.fields)
		}

		if !merge {
			return output.Write(out, outputMode, l.record)
		}

		return output.WriteMerged(out, outputMode, l.record, l.raw, l.state, mergeKey(flags))
	}

	var stopped atomic.Bool

	result, err := engine.Run(cmd.Context(), engine.Config[namedRecord, line]{
		Source: source,
		Evaluate: func(ctx context.Context, rec namedRecord) (line, error) {
			l, evalErr := evaluateOne(ctx, rec)
			l.slot = rec.slot

			return l, evalErr
		},
		Write: func(l line) error {
			if book == nil {
				return write(l)
			}

			start := answers.offset()
			writeErr := write(l)
			book.wrote(l.slot, span{start: start, end: answers.offset()})

			return writeErr
		},
		Jobs:        flags.jobs,
		Unordered:   flags.unordered,
		StopOnError: flags.stopOnError,
		Abort:       aborting,
		Stop:        watched(stopping(flags), &stopped),
	})
	source.stop()

	if md != nil && !result.Broken {
		whole := err == nil && !result.Aborted && !stopped.Load() && !interrupted(cmd.Context())

		// A consumer that stopped reading before the summary leaves the records' exit code to stand.
		finishErr := md.Finish(whole)
		if finishErr != nil && !engine.BrokenPipe(finishErr) && err == nil && !result.Aborted {
			return finishErr
		}
	}

	skips := source.skipped()
	stats.skip(skips)

	// A stored failure a resume skipped fails the run as it failed the one that wrote it.
	result.Failed += skips.failed

	if freshAbort := source.freshRunAbort(); freshAbort != nil {
		return freshAbort
	}

	if err != nil {
		// The source stopping the run is the worse outcome and takes the exit code, since a
		// truncated stream is not something a caller can tell from a complete one. The records
		// that had already failed are named in the message rather than dropped.
		return &sourceError{cause: err, failed: result.Failed}
	}

	// Only a run that read and wrote every record rewrites the file. Anything short of that leaves
	// the appended lines as they are, for the next resume to finish.
	if book != nil && !result.Aborted && !result.Broken && !stopped.Load() && book.complete() {
		answers.compactInto(book.takeOrder(flags.prune), outputMode)
	}

	return streamResult(result, stats.falseAssertions(), stats.abstains())
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

func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
	}

	if noun == "retry" {
		return fmt.Sprintf("%d retries", count)
	}

	return fmt.Sprintf("%d %ss", count, noun)
}

func rowLine(record output.Record, rec namedRecord) line {
	var fields map[string]any
	if object, ok := rec.State.(map[string]any); ok {
		fields = object
	}

	record.ID = rec.id

	return line{record: record, raw: rec.Raw, state: rec.Wire, header: rec.Header, fields: fields}
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
		// Unreachable for a line that parsed, which is the only way this is called.
		return []byte(raw)
	}

	return flat.Bytes()
}

func aborting(err error) bool {
	// Every later record would fail the same way, and a bad key should be reported once rather
	// than once per line.
	return errors.Is(err, jev.ErrAuthentication) || errors.Is(err, jev.ErrPermissionDenied) ||
		errors.Is(err, jev.ErrPaymentRequired)
}

func stopping(flags *runFlags) func(line) bool {
	if !flags.stopOnAssert {
		// Nil rather than a predicate that always says no, so the engine asks nothing of a run
		// that did not pass the flag.
		return nil
	}

	return func(l line) bool {
		return l.record.AssertFailed
	}
}

type line struct {
	record output.Record
	raw    string
	state  any // The record as read, before --map, so --merge keeps a text line's type and a JSON line's digits.
	header []string
	fields map[string]any
	slot   int
}

func mergeName(flags *runFlags) string {
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
		return &abortError{cause: result.Cause}
	}

	if result.Failed > 0 {
		return &recordsError{}
	}

	// After the failed records and before the successful return, which is where §8 places a false
	// assertion. A stream carrying both exits 6, since a record that never answered says more
	// than a gate that answered no.
	if falseAsserts > 0 {
		return &rejectedError{}
	}

	// Last, since a gate that could not decide says less than one that answered no.
	if abstains > 0 {
		return &abstainError{}
	}

	return nil
}

func ask(
	cmd *cobra.Command,
	settings rootSettings,
	built *plan.Plan,
	resolved input.Resolved,
	sent any,
	outputMode output.Mode,
	flags *runFlags,
	gate *assert.Expr,
	abstain *assert.Expr,
	stats *collector,
) error {
	client, err := settings.newClient(cmd.Context(), observing(stats)...)
	if err != nil {
		return err
	}

	questions := wireAll(built.Questions)

	model, err := resolveModel(settings, flags, built.Model)
	if err != nil {
		return err
	}

	record, err := evaluate(
		cmd.Context(), client, built, model, questions, sent, flags.usage, stats)
	if err != nil {
		// The exit code still comes from the error. This adds the fallback word the caller asked
		// for, so a shell guard reads a decision. An interrupt is skipped, since a transport record
		// written into the pipe the caller was closing reports a fault that never happened.
		if !errors.Is(err, context.Canceled) {
			if writeErr := writeFailure(
				cmd, settings, outputMode, flags, resolved, record, gate, abstain); writeErr != nil {
				return writeErr
			}
		}

		return err
	}

	// Only a record that arrived is evaluated, per §17.4's third row. A failed request would read
	// as all zeros, so a gate such as answer.value < 0.5 would hold for a request that never
	// happened.
	record = judge(gate, abstain, record, stats)

	if flags.quiet {
		// Beside an assertion -q only silences the output. Stacking its own gate on top made a
		// safety assertion such as value < 0.5 impossible to pass.
		if gate == nil {
			return quietResult(built.Questions[0], record.Answers[0].Answer)
		}

		return assertResult(record)
	}

	// The record prints whatever the assertion said, §17.4, and the exit code follows it.
	if writeErr := writeRecord(cmd, settings, outputMode, flags, resolved, record, gate, abstain); writeErr != nil {
		return writeErr
	}

	return assertResult(record)
}

func assertResult(record output.Record) error {
	if record.AssertFailed {
		// §17.4 calls a false assertion the same statement a policy rejection makes over one
		// answer, so it takes the same error and the same exit code.
		return &rejectedError{}
	}

	if record.Abstained {
		return &abstainError{}
	}

	return nil
}

func judge(gate, abstain *assert.Expr, record output.Record, stats *collector) output.Record {
	if assert.Eval(gate, record) {
		return record
	}

	// Eval holds for a nil expression, which is right for a missing gate and wrong here, where a
	// missing abstain expression leaves every false assertion a no.
	if abstain != nil && assert.Eval(abstain, record) {
		record.Abstained = true
		stats.abstained()

		return record
	}

	record.AssertFailed = true
	stats.assertFailed()

	return record
}

func writeFailure(
	cmd *cobra.Command,
	settings rootSettings,
	mode output.Mode,
	flags *runFlags,
	resolved input.Resolved,
	record output.Record,
	gate *assert.Expr,
	abstain *assert.Expr,
) error {
	// -q suppresses output entirely, so the exit code carries the whole result.
	if flags.quiet {
		return nil
	}

	return writeRecord(cmd, settings, mode, flags, resolved, record, gate, abstain)
}

func writeRecord(
	cmd *cobra.Command,
	settings rootSettings,
	mode output.Mode,
	flags *runFlags,
	resolved input.Resolved,
	record output.Record,
	gate *assert.Expr,
	abstain *assert.Expr,
) error {
	if mode == output.Markdown {
		return output.WriteMarkdown(cmd.OutOrStdout(), record, markdownOptions(gate, abstain))
	}

	if mode == output.CSV || mode == output.TSV {
		built := &plan.Plan{}
		for _, named := range record.Answers {
			built.Questions = append(built.Questions, plan.Question{ID: named.ID})
		}

		return delimited(cmd.OutOrStdout(), mode, built, gate != nil, false, flags).Write(record, nil, nil)
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
	flags *runFlags,
) *output.Delimited {
	if mode != output.CSV && mode != output.TSV {
		return nil
	}

	ids := make([]string, 0, len(built.Questions))
	for _, question := range built.Questions {
		ids = append(ids, question.ID)
	}

	return output.NewDelimited(out, mode, output.DelimitedOptions{
		IDs: ids, ID: withID, Assert: withAssert, Header: !flags.resumeHeader,
	})
}

func merging(flags *runFlags) bool {
	return flags.merge || flags.mergeKey != ""
}

func writeMerged(
	w io.Writer,
	mode output.Mode,
	record output.Record,
	resolved input.Resolved,
	flags *runFlags,
) error {
	return output.WriteMerged(w, mode, record, resolved.Raw, resolved.Wire, mergeKey(flags))
}

func mergeKey(flags *runFlags) string {
	if flags.mergeKey != "" {
		return flags.mergeKey
	}

	return "answers"
}

func evaluate(
	ctx context.Context,
	client *jev.Client,
	built *plan.Plan,
	model string,
	questions jev.Questions,
	state any,
	withUsage bool,
	stats *collector,
) (output.Record, error) {
	record, usage, err := answered(ctx, client, built, model, questions, state, withUsage)
	if err != nil {
		// The request was made whatever went wrong afterwards, and the questions went with it, so
		// a failed record still carries them into the count section 10 asks for.
		stats.recordFailure(err, true, len(questions))
		stats.terminalAttempt(err)
		stats.spend(usage)

		return record, err
	}

	stats.record(record.Model, usage, len(questions))

	return record, nil
}

func answered(
	ctx context.Context,
	client *jev.Client,
	built *plan.Plan,
	model string,
	questions jev.Questions,
	state any,
	withUsage bool,
) (output.Record, jev.Usage, error) {
	result, err := client.SystemOne(ctx, jev.Request{
		State:     state,
		Model:     built.Model,
		Questions: questions,
	})
	if err != nil {
		// Wrapped here rather than at either caller, so the stderr line and the streaming record
		// carry the same remedy. Every question on this path passed onesie's own bounds check, so a
		// count the server rejects says something about those bounds.
		advised := advise(err, model, true)
		failed := failureRecord(built, advised)

		var unusable *jev.ResponseError
		if !errors.As(err, &unusable) || unusable.Usage == nil {
			return failed, jev.Usage{}, advised
		}

		if withUsage {
			failed = spent(failed, unusable.Usage)
		}

		return failed, *unusable.Usage, advised
	}

	record := output.Record{Model: result.Model}
	if withUsage {
		record.Usage = &result.Usage
	}

	// SystemOne already refused a response missing any of these questions, since every caller asks
	// built.Questions.
	for _, question := range built.Questions {
		normalized, normErr := answer.Normalize(question, result.Answers[question.ID])
		if normErr != nil {
			return spent(failureRecord(built, normErr), record.Usage), result.Usage, normErr
		}

		answer.Apply(question, normalized)

		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: normalized})
	}

	return record, result.Usage, nil
}

func spent(failed output.Record, usage *jev.Usage) output.Record {
	// A 200 onesie could not use still cost its tokens, so --usage reports them on the error line.
	failed.Usage = usage

	return failed
}

func failureRecord(built *plan.Plan, cause error) output.Record {
	record := output.Record{Failure: describe(cause)}

	for _, question := range built.Questions {
		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: answer.Failed(question)})
	}

	return record
}

func describe(cause error) *output.Failure {
	var unusable *jev.ResponseError
	if errors.As(cause, &unusable) {
		// A 2xx whose body onesie could not use is deterministic. Calling it http would name a status
		// the caller may retry, and calling it transport a connection blip that never happened.
		status := unusable.Status

		return &output.Failure{Kind: "response", Status: &status, Message: cause.Error()}
	}

	var api *jev.APIError
	if errors.As(cause, &api) {
		status := api.Status

		return &output.Failure{Kind: "http", Status: &status, Message: cause.Error()}
	}

	if errors.Is(cause, jev.ErrConnection) || errors.Is(cause, context.DeadlineExceeded) {
		return &output.Failure{Kind: "transport", Message: cause.Error()}
	}

	// No status arrived and the network did not fail, so the record never became a request, as with
	// a line onesie could not read. Calling it transport would blame the network, and exit 5 where a
	// fresh run exits 2.
	return &output.Failure{Kind: "input", Message: cause.Error()}
}

func quietResult(question plan.Question, a *answer.Answer) error {
	if question.Shape == plan.Noul {
		// The threshold defaults to a half, so a yes/no question is meaningful under -q with no
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

func outputName(flags *runFlags) string {
	if flags.raw {
		return "raw"
	}

	return flags.output
}

type runFlags struct {
	provider     string
	out          string
	resume       bool
	prune        bool
	resumeSkip   int
	resumeHeader bool
	output       string
	raw          bool
	quiet        bool
	usage        bool
	input        string
	state        string
	stateFile    string
	model        string
	apiKey       string
	baseURL      string
	file         string
	replace      bool

	assert    []string
	abstainIf []string

	printQuestions bool
	printRequest   bool
	listModels     bool
	stats          bool

	jobs          int
	timeout       int
	retries       int
	maxRetryAfter int
	unordered     bool
	stopOnError   bool
	stopOnAssert  bool
	skipBlank     bool
	merge         bool
	mergeKey      string
	mapSource     string
	idSource      string
}
