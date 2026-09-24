package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func run(
	cmd *cobra.Command,
	settings rootSettings,
	events []argv.Event,
	positional string,
	flags *runFlags,
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

		return withStats(cmd, settings.now, flags, func(stats *collector) error {
			return listModels(cmd, settings, stats)
		})
	}

	if inputMode == input.Request {
		if checkErr := checkFlags(cmd, cfg); checkErr != nil {
			return checkErr
		}

		return withStats(cmd, settings.now, flags, func(stats *collector) error {
			return streamRaw(cmd, settings, flags, stats)
		})
	}

	inv, warnings, err := build(settings, cfg, events, positional, flags)

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
	loaded := inv.loaded

	// Before the output mode, because a question file is not an output mode and -o has no meaning
	// for it. After validation, because a dry run that accepted a plan the real run would reject
	// would be worse than useless.
	if flags.printQuestions {
		return printQuestions(
			cmd.OutOrStdout(), cmd.ErrOrStderr(), built.Questions, gate.Source(), loaded)
	}

	// Both modes are parsed before the request, so a mistyped flag costs nothing.
	outputMode, err := output.ParseMode(
		outputName(flags), settings.stdoutTTY, inputMode.Streaming(), merging(flags))
	if err != nil {
		return err
	}

	if inputMode.Streaming() {
		return withStats(cmd, settings.now, flags, func(stats *collector) error {
			return stream(cmd, settings, built, inputMode, outputMode, flags, gate, stats)
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

		if err := input.CheckState(resolved.State); err != nil {
			return fmt.Errorf("onesie: %w", err)
		}
	}

	if requests(flags) && resolved.Source == input.SourceNone {
		return errors.New(
			"onesie: no state given. Pipe one to stdin, or pass --state or --state-file")
	}

	// After the state is resolved, so the body carries the state a real run would send.
	if flags.printRequest {
		model, err := resolveModel(settings, flags, built.Model)
		if err != nil {
			return err
		}

		return printRequest(cmd.OutOrStdout(), built.Questions, resolved, model)
	}

	// Checked here rather than at write time, so a taken key costs no request. Section 11 opens
	// with every check running before any network call.
	if merging(flags) && hasKey(resolved.State, mergeKey(flags)) {
		return fmt.Errorf(
			"onesie: --merge would overwrite the input's '%s' key. Pass --merge-key",
			mergeKey(flags))
	}

	return withStats(cmd, settings.now, flags, func(stats *collector) error {
		return ask(cmd, settings, built, resolved, outputMode, flags, gate, stats)
	})
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
	inputMode input.Mode,
	outputMode output.Mode,
	flags *runFlags,
	gate *assert.Expr,
	stats *collector,
) error {
	if flags.printRequest {
		return streamRequests(cmd, settings, built, inputMode, flags)
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

	source := engine.Skip[input.Record](input.NewStream(settings.stdin, inputMode, flags.skipBlank), flags.resumeSkip)
	out := cmd.OutOrStdout()
	merge := merging(flags)
	table := delimited(out, outputMode, built, gate != nil, flags)

	result, err := engine.Run(cmd.Context(), engine.Config[input.Record, line]{
		Source: source,
		Evaluate: func(ctx context.Context, rec input.Record) (line, error) {
			if rec.Err != nil {
				// A line onesie could not read is a record that never became a request, and it
				// carried no questions to the wire either.
				stats.recordFailure(rec.Err, false, 0)

				return line{record: failureRecord(built, rec.Err), raw: rec.Raw, header: rec.Header}, rec.Err
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

			record, evalErr := evaluate(
				ctx, client, built, model, questions, rec.Wire, flags.usage, stats)
			if evalErr != nil {
				// Returned before the gate is asked, per §17.4's third row. A failed record reads
				// as all zeros, so a gate such as answer.value < 0.5 would hold for a request that
				// never happened.
				return rowLine(record, rec), evalErr
			}

			// §17.6 asks the gate per record. A false assertion is not an engine failure: the
			// record succeeded and the answer is complete, so it is carried on the line rather
			// than returned as an error the engine would count against result.Failed.
			record.AssertFailed = asserted(gate, record, stats)

			return rowLine(record, rec), nil
		},
		Write: func(l line) error {
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
		},
		Jobs:        flags.jobs,
		Unordered:   flags.unordered,
		StopOnError: flags.stopOnError,
		Abort:       aborting,
		Stop:        stopping(flags),
	})
	if err != nil {
		// The source stopping the run is the worse outcome and takes the exit code, since a
		// truncated stream is not something a caller can tell from a complete one. The records
		// that had already failed are named in the message rather than dropped.
		return &sourceError{cause: err, failed: result.Failed}
	}

	return streamResult(result, stats.falseAssertions())
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

type line struct {
	record output.Record
	raw    string
	// state is what was sent to the API, which --merge needs so a text line keeps its type and a
	// JSON line keeps its digits. It is nil for a record onesie could not read.
	state any
	// header and fields are the csv or tsv row the record answers, for a merge into -o csv.
	header []string
	fields map[string]any
}

func rowLine(record output.Record, rec input.Record) line {
	var fields map[string]any
	if object, ok := rec.State.(map[string]any); ok {
		fields = object
	}

	return line{record: record, raw: rec.Raw, state: rec.Wire, header: rec.Header, fields: fields}
}

func delimited(
	out io.Writer,
	mode output.Mode,
	built *plan.Plan,
	withAssert bool,
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
		IDs: ids, Assert: withAssert, Header: !flags.resumeHeader,
	})
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

func merging(flags *runFlags) bool {
	return flags.merge || flags.mergeKey != ""
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

func mergeKey(flags *runFlags) string {
	if flags.mergeKey != "" {
		return flags.mergeKey
	}

	return "answers"
}

func streamResult(result engine.Result, falseAsserts int) error {
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

	return nil
}

func ask(
	cmd *cobra.Command,
	settings rootSettings,
	built *plan.Plan,
	resolved input.Resolved,
	outputMode output.Mode,
	flags *runFlags,
	gate *assert.Expr,
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
		cmd.Context(), client, built, model, questions, resolved.Wire, flags.usage, stats)
	if err != nil {
		// The exit code still comes from the error. This adds the fallback word the caller asked
		// for, so a shell guard reads a decision. An interrupt is skipped, since a transport record
		// written into the pipe the caller was closing reports a fault that never happened.
		if !errors.Is(err, context.Canceled) {
			if writeErr := writeFailure(
				cmd, settings, outputMode, flags, resolved, record, gate != nil); writeErr != nil {
				return writeErr
			}
		}

		return err
	}

	// Only a record that arrived is evaluated, per §17.4's third row. A failed request would read
	// as all zeros, so a gate such as answer.value < 0.5 would hold for a request that never
	// happened.
	record.AssertFailed = asserted(gate, record, stats)

	if flags.quiet {
		// Beside an assertion -q only silences the output. Stacking its own gate on top made a
		// safety assertion such as value < 0.5 impossible to pass.
		if gate == nil {
			return quietResult(built.Questions[0], record.Answers[0].Answer)
		}

		return assertResult(record)
	}

	// The record prints whatever the assertion said, §17.4, and the exit code follows it.
	if writeErr := writeRecord(cmd, settings, outputMode, flags, resolved, record, gate != nil); writeErr != nil {
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

	return nil
}

func asserted(gate *assert.Expr, record output.Record, stats *collector) bool {
	if assert.Eval(gate, record) {
		return false
	}

	stats.assertFailed()

	return true
}

func writeFailure(
	cmd *cobra.Command,
	settings rootSettings,
	mode output.Mode,
	flags *runFlags,
	resolved input.Resolved,
	record output.Record,
	withAssert bool,
) error {
	// -q suppresses output entirely, so the exit code carries the whole result.
	if flags.quiet {
		return nil
	}

	return writeRecord(cmd, settings, mode, flags, resolved, record, withAssert)
}

func writeRecord(
	cmd *cobra.Command,
	settings rootSettings,
	mode output.Mode,
	flags *runFlags,
	resolved input.Resolved,
	record output.Record,
	withAssert bool,
) error {
	if mode == output.CSV || mode == output.TSV {
		built := &plan.Plan{}
		for _, named := range record.Answers {
			built.Questions = append(built.Questions, plan.Question{ID: named.ID})
		}

		return delimited(cmd.OutOrStdout(), mode, built, withAssert, flags).Write(record, nil, nil)
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

func writeMerged(
	w io.Writer,
	mode output.Mode,
	record output.Record,
	resolved input.Resolved,
	flags *runFlags,
) error {
	return output.WriteMerged(w, mode, record, resolved.Raw, resolved.Wire, mergeKey(flags))
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

		return failureRecord(built, advised), jev.Usage{}, advised
	}

	record := output.Record{Model: result.Model}
	if withUsage {
		record.Usage = &result.Usage
	}

	for _, question := range built.Questions {
		raw, ok := result.Answers[question.ID]
		if !ok {
			// A failure record rather than an empty one, since a bare {} in a stream reads as a
			// successful answer to nothing. Typed, so section 8 gives it kind response with status
			// 200 and a single shot run exits 4.
			missing := &jev.ResponseError{
				Status:  http.StatusOK,
				Message: fmt.Sprintf("onesie: response carries no answer for '%s'", question.ID),
			}

			return failureRecord(built, missing), result.Usage, missing
		}

		normalized, normErr := answer.Normalize(question, raw)
		if normErr != nil {
			return failureRecord(built, normErr), result.Usage, normErr
		}

		answer.Apply(question, normalized)

		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: normalized})
	}

	return record, result.Usage, nil
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
	var bad *input.LineError
	if errors.As(cause, &bad) {
		// No request was sent, so there is no status. Calling it transport would blame the network
		// for a line onesie could not read.
		return &output.Failure{Kind: "input", Message: cause.Error()}
	}

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

	return &output.Failure{Kind: "transport", Message: cause.Error()}
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

	assert []string

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
}
