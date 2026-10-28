package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync/atomic"

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

	inputMode := flags.inputMode

	// Both branches sit ahead of the plan, since neither has a question and failing for want of one
	// would send the user to the wrong flag. --list-models comes first, so it names itself rather
	// than the mode it was combined with.
	if flags.listModels {
		// The only hint is -j on one record, which --list-models refuses and -i request never is.
		if _, checkErr := checkFlags(flags, runFacts{}); checkErr != nil {
			return checkErr
		}

		out.BindWithoutFingerprint()

		return withStats(cmd, settings.now, flags, func(stats *collector) error {
			return listModels(cmd, settings, flags, stats)
		})
	}

	if inputMode == input.Request {
		if _, checkErr := checkFlags(flags, runFacts{}); checkErr != nil {
			return checkErr
		}

		// Each body names its own model or takes the run's, and the fingerprint holds no model, as
		// it holds nothing else a body carries.
		if bindErr := bindOut(out, settings, flags, nil, answersfile.FingerprintInputs{
			Input: inputMode.String(), LongContext: flags.env.longContext,
		}); bindErr != nil {
			return bindErr
		}

		return withStats(cmd, settings.now, flags, func(stats *collector) error {
			return streamRaw(cmd, settings, flags, stats, out, resume)
		})
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

	built := inv.plan
	gate := inv.gate
	abstain := inv.abstain
	loaded := inv.loaded

	if bodyErr := checkBodyState(flags, loaded); bodyErr != nil {
		return bodyErr
	}

	// Before the output mode, because a question file is not an output mode and -o has no meaning
	// for it. After validation, because a dry run that accepted a plan the real run would reject
	// would be worse than useless.
	if flags.printQuestions {
		out.BindWithoutFingerprint()

		return printQuestions(
			cmd.OutOrStdout(), cmd.ErrOrStderr(), built.Questions, gate.Source(),
			abstain.Source(), loaded, settings.schemaURL)
	}

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
			cmd.ErrOrStderr(), flags, loaded, "a stream", inv.images, wireAll(built.Questions)); warnErr != nil {
			return warnErr
		}

		if refuseErr := checkProviderNeeds(settings, flags, built, inv.images); refuseErr != nil {
			return refuseErr
		}

		return withStats(cmd, settings.now, flags, func(stats *collector) error {
			return stream(cmd, settings, flags, inv, outputMode, stats, out, resume, answers)
		})
	}

	if refuseErr := checkProviderNeeds(settings, flags, built, inv.images); refuseErr != nil {
		return refuseErr
	}

	resolved, err := resolveState(cmd.Context(), settings, flags, loaded, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	if requests(flags) && resolved.Source == input.SourceNone && len(flags.images) == 0 {
		return errors.New(
			"no state given. Pipe one to stdin, or pass --state or --state-file")
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

	if len(flags.images) > 0 {
		resolved, err = imagedState(sent)
		if err != nil {
			return err
		}
	}

	// After the state is resolved, so the body carries the state a real run would send.
	if flags.printRequest {
		model, err := resolveModel(settings, built.Model)
		if err != nil {
			return err
		}

		return printRequest(cmd.OutOrStdout(), built.Questions, resolved.Source, sent, assets, built.LongContext, model)
	}

	// Checked here rather than at write time, so a taken key costs no request.
	if merging(flags) && hasKey(resolved.State, mergeKey(flags)) {
		return fmt.Errorf(
			"--merge would overwrite the input's '%s' key. Pass --merge-key",
			mergeKey(flags))
	}

	return withStats(cmd, settings.now, flags, func(stats *collector) error {
		return ask(cmd, settings, flags, inv, singleState{resolved: resolved, sent: sent, assets: assets},
			outputMode, stats, answers)
	})
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

	return wordAnswersError(out.Bind(questions, wireAll(questions), inputs))
}

func outFingerprint(
	settings rootSettings, flags runConfig, questions []plan.Question, inputs answersfile.FingerprintInputs,
) (string, error) {
	inputs, err := runInputs(settings, flags, inputs)
	if err != nil {
		return "", err
	}

	return answersfile.Fingerprint(questions, wireAll(questions), inputs)
}

func runInputs(settings rootSettings, flags runConfig, inputs answersfile.FingerprintInputs) (answersfile.FingerprintInputs, error) {
	chosen, err := settings.holder.resolved()
	if err != nil {
		return answersfile.FingerprintInputs{}, err
	}

	inputs.Provider = chosen.provider.Name()
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

func stream(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	inv *invocation,
	outputMode output.Mode,
	stats *collector,
	answers *answersfile.File,
	resume answersfile.Resume,
	answerers answererFactory,
) error {
	if flags.printRequest {
		return streamRequests(cmd, settings, flags, inv, resume)
	}

	built, mapper, namer, images := inv.plan, inv.mapper, inv.namer, inv.images
	gate, abstain, inputMode := inv.gate, inv.abstain, flags.inputMode

	asker, err := answerers(cmd.Context(), stats)
	if err != nil {
		return err
	}

	questions := wireAll(built.Questions)

	model, err := resolveModel(settings, built.Model)
	if err != nil {
		return err
	}

	format := answersfile.Format{
		Mode: outputMode, Merge: merging(flags), MergeKey: mergeKey(flags), Namer: namer, Gated: gate != nil,
	}

	book, err := answersfile.ResumeLedger(cmd.Context(), answers, format)
	if err != nil {
		return err
	}

	resume, err = answersfile.ResumedVerdicts(cmd.Context(), answers, format, flags.stopOnError, resume)
	if err != nil {
		return err
	}

	source := records(cmd.Context(), settings, inputMode, flags, namer, book, resume, outputMode)
	out := cmd.OutOrStdout()
	merge := merging(flags)
	table := delimited(out, outputMode, built, gate != nil, namer != nil && !merge, resume.Header)
	md := markdown(out, outputMode, built, gate != nil, namer != nil)

	var (
		shared    *dedup
		noticeErr error
	)

	if !flags.noDedup {
		shared = newDedup(settings.dedupLimit, func() {
			noticeErr = output.Notice(cmd.ErrOrStderr(), fmt.Sprintf(
				"%d distinct requests held, so later records are deduplicated only against those",
				settings.dedupLimit))
		})
	}

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

		state, wire := images.wrapLine(rec.State, rec.Wire)

		sent, mapErr := mapped(ctx, mapper, state, wire)
		if interrupted(ctx) {
			return line{}, ctx.Err()
		}

		if mapErr != nil {
			bad := &input.LineError{Line: rec.Line, Err: mapErr}
			stats.recordFailure(bad, false, 0)

			return rowLine(failureRecord(built, bad), rec), bad
		}

		// In the worker rather than the reader, so at most -j records hold image bytes at once.
		sent, assets, imageErr := images.attach(sent)
		if imageErr != nil {
			bad := &input.LineError{Line: rec.Line, Err: imageErr}
			stats.recordFailure(bad, false, 0)

			return rowLine(failureRecord(built, bad), rec), bad
		}

		key := recordKey{position: rec.Index + 1, line: rec.Line, id: rec.id}

		// A record the mock file does not answer shares with nothing, so its error names its own line.
		group := shared

		salt, shares := asker.salt(key)
		if !shares {
			group = nil
		}

		record, call, evalErr := askOnce(ctx, group, stats,
			onesie.Request{
				State: sent, Model: model, Questions: questions, Assets: assets, LongContext: built.LongContext,
			}, salt,
			func() (output.Record, error) {
				return evaluate(ctx, asker, key, built, model, questions, sent, assets, flags.usage, stats)
			})
		if evalErr != nil {
			// Returned before the gate is asked. A failed record reads as all zeros, so a gate such
			// as answer.value < 0.5 would hold for a request that never happened.
			failed := rowLine(record, rec)
			failed.halted = halting(evalErr)
			failed.call = call

			return failed, evalErr
		}

		// A false assertion or an abstain is not an engine failure: the record succeeded and the
		// answer is complete, so the outcome is carried on the line rather than returned as an
		// error the engine would count against result.Failed.
		record = judge(gate, abstain, record, stats)

		answeredLine := rowLine(record, rec)
		answeredLine.call = call

		return answeredLine, nil
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

	// Set by the first record that halts the run, such as one a mock file does not answer. Every line
	// after it is dropped too, so the output holds only the records before it and a resume asks the rest.
	var stoppedAtHalt bool

	result, err := engine.Run(cmd.Context(), engine.Config[namedRecord, line]{
		Source: source,
		Evaluate: func(ctx context.Context, rec namedRecord) (line, error) {
			l, evalErr := evaluateOne(ctx, rec)
			l.slot = rec.slot

			if inputMode == input.Lines {
				l.record.Line = rec.Line
			}

			return l, evalErr
		},
		Write: func(l line) error {
			if stoppedAtHalt || l.halted {
				stoppedAtHalt = true

				return nil
			}

			if flags.usage {
				billOnce(&l)
			}

			if book == nil {
				return write(l)
			}

			start := answers.Offset()
			writeErr := write(l)
			book.Wrote(l.slot, answersfile.Span{Start: start, End: answers.Offset()})

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
	result.Failed += skips.Failed

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
	if book != nil && !result.Aborted && !result.Broken && !stopped.Load() && book.Complete() {
		answers.CompactInto(book.TakeOrder(flags.prune), outputMode)
	}

	// The records' own outcome wins, since a note that failed to print is the smaller loss.
	outcome := streamResult(result, stats.falseAssertions(), stats.abstains())
	if outcome == nil && noticeErr != nil {
		return noticeErr
	}

	return outcome
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
		// Unreachable for a line that parsed, the only kind compact is called with.
		return []byte(raw)
	}

	return flat.Bytes()
}

func aborting(err error) bool {
	// Every later record would fail the same way, and a bad key should be reported once rather
	// than once per line.
	return authFailure(err) || halting(err)
}

func stopping(flags runConfig) func(line) bool {
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
	halted bool
	call   *sharedCall
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
	return !aborting(cause) && !errors.Is(cause, context.Canceled) && classify(cause).kind == output.KindInput
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

	questions := wireAll(built.Questions)

	model, err := resolveModel(settings, built.Model)
	if err != nil {
		return err
	}

	record, err := evaluate(
		cmd.Context(), asker, recordKey{position: 1, line: 1}, built, model, questions, state.sent, state.assets,
		flags.usage, stats)
	if halting(err) {
		return err
	}

	if err != nil {
		// The exit code still comes from the error. This adds the fallback word the caller asked
		// for, so a shell guard reads a decision. An interrupt is skipped, since a transport record
		// written into the pipe the caller was closing reports a fault that never happened.
		if !errors.Is(err, context.Canceled) {
			if writeErr := writeFailure(cmd, settings, outputMode, flags, inv, state.resolved, record); writeErr != nil {
				return writeErr
			}
		}

		return err
	}

	// Only a record that arrived is evaluated. A failed request would read as all zeros, so a gate
	// such as answer.value < 0.5 would hold for a request that never happened.
	record = judge(gate, abstain, record, stats)

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
		return delimited(cmd.OutOrStdout(), mode, inv.plan, inv.gate != nil, false, false).Write(record, nil, nil)
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
		Assert: withAssert, Header: !headerWritten,
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

func evaluate(
	ctx context.Context,
	asker answerer,
	key recordKey,
	built *plan.Plan,
	model string,
	questions onesie.Questions,
	state any,
	assets onesie.Assets,
	withUsage bool,
	stats *collector,
) (output.Record, error) {
	record, usage, cached, err := answered(ctx, asker, key, built, model, questions, state, assets, withUsage)
	if halting(err) {
		// No request stood behind it, so it is a record and not a request, and no attempt ended.
		stats.recordFailure(err, false, 0)

		return record, err
	}

	if err != nil {
		// The request was made whatever went wrong afterwards, and the questions went with it, so a
		// failed record still carries them into the count --stats reports.
		stats.recordFailure(err, true, len(questions))
		stats.terminalAttempt(err)
		stats.spend(usage)

		return record, err
	}

	if cached {
		stats.cachedRecord(record.Model)

		return record, nil
	}

	stats.record(record.Model, usage, len(questions))

	return record, nil
}

func answered(
	ctx context.Context,
	asker answerer,
	key recordKey,
	built *plan.Plan,
	model string,
	questions onesie.Questions,
	state any,
	assets onesie.Assets,
	withUsage bool,
) (output.Record, onesie.Usage, bool, error) {
	req := onesie.Request{
		State: state, Model: model, Questions: questions, Assets: assets, LongContext: built.LongContext,
	}

	got, err := asker.answer(ctx, key, req)
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

	normalized, normErr := answer.NormalizeAll(built.Questions, result)
	if normErr != nil {
		return spent(failureRecord(built, normErr), record.Usage), result.Usage, got.cached, normErr
	}

	for i, question := range built.Questions {
		answer.Apply(question, normalized[i])

		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: normalized[i], Box: question.Shape == plan.Box})
	}

	return record, result.Usage, got.cached, nil
}

func hasBox(questions onesie.Questions) bool {
	return slices.ContainsFunc(questions, func(named onesie.NamedQuestion) bool {
		_, isBox := named.Question.(onesie.Box)

		return isBox
	})
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
