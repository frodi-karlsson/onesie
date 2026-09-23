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

	"github.com/frodi-karlsson/jev-cli/internal/answer"
	"github.com/frodi-karlsson/jev-cli/internal/argv"
	"github.com/frodi-karlsson/jev-cli/internal/assert"
	"github.com/frodi-karlsson/jev-cli/internal/engine"
	"github.com/frodi-karlsson/jev-cli/internal/input"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/output"
	"github.com/frodi-karlsson/jev-cli/internal/plan"
	"github.com/frodi-karlsson/jev-cli/internal/qfile"
)

func run(
	cmd *cobra.Command,
	settings rootSettings,
	events []argv.Event,
	positional string,
	flags *runFlags,
) error {
	inputMode, err := input.ParseMode(flags.input)
	if err != nil {
		return err
	}

	cfg := plan.Config{
		Raw:            flags.raw,
		Quiet:          flags.quiet,
		HasAssert:      len(flags.assert) > 0,
		Output:         flags.output,
		HasState:       cmd.Flags().Changed(flagState),
		HasStateFile:   cmd.Flags().Changed(flagStateFile),
		Replace:        flags.replace,
		FileName:       flags.file,
		HasAsk:         asked(events),
		GroupFlags:     eventNames(events),
		HasPositional:  positional != "",
		HasModel:       cmd.Flags().Changed(flagModel),
		Usage:          flags.usage,
		PrintQuestions: flags.printQuestions,
		PrintRequest:   flags.printRequest,
		Stats:          flags.stats,
		Streaming:      inputMode.Streaming(),
		RequestMode:    inputMode == input.Request,
		ListModels:     flags.listModels,
		InputName:      inputName(flags.input),
		HasInput:       cmd.Flags().Changed(flagInput),
		Unordered:      flags.unordered,
		StopOnError:    flags.stopOnError,
		StopOnAssert:   flags.stopOnAssert,
		SkipBlank:      flags.skipBlank,
		Merge:          merging(flags),
		MergeName:      mergeName(flags),
		Jobs:           flags.jobs,
		JobsSet:        cmd.Flags().Changed(flagJobs),

		Timeout:          flags.timeout,
		TimeoutSet:       cmd.Flags().Changed(flagTimeout),
		Retries:          flags.retries,
		RetriesSet:       cmd.Flags().Changed(flagRetries),
		MaxRetryAfter:    flags.maxRetryAfter,
		MaxRetryAfterSet: cmd.Flags().Changed(flagMaxRetryAfter),
	}

	// Both branches sit ahead of the plan, because neither has a question to assemble one from and
	// failing for want of a question the user was right not to give would send them to the wrong
	// flag. --list-models comes first, so it names itself rather than the mode it was combined
	// with.
	if flags.listModels {
		if checkErr := checkFlags(cmd, cfg); checkErr != nil {
			return checkErr
		}

		return withStats(cmd, flags, func(stats *collector) error {
			return listModels(cmd, settings, stats)
		})
	}

	if inputMode == input.Request {
		if checkErr := checkFlags(cmd, cfg); checkErr != nil {
			return checkErr
		}

		return withStats(cmd, flags, func(stats *collector) error {
			return streamRaw(cmd, settings, flags, stats)
		})
	}

	// Every other mode needs a question, and reporting that here rather than from Assemble keeps
	// the message the same whichever source was missing.
	if positional == "" && len(events) == 0 && flags.file == "" {
		return errors.New("jev: no question given. Pass a question, --ask, or -f")
	}

	var loaded *qfile.File

	if flags.file != "" {
		data, readErr := settings.readFile(flags.file)
		if readErr != nil {
			return fmt.Errorf("jev: reading %s: %w", flags.file, readErr)
		}

		loaded, err = qfile.Load(data)
		if err != nil {
			return err
		}

		// The file's assertion is the same gate --assert is, §17.5, so it answers the -q rule the
		// same way. The config is built before the file is read, which is why this is not up there
		// with the flags.
		if loaded.Assert != "" && !cfg.HasAssert {
			cfg.AssertName = plan.Spelling(plan.OriginFile, "--assert")
		}

		cfg.HasAssert = cfg.HasAssert || loaded.Assert != ""
	}

	source := plan.Source{
		Events:     events,
		Positional: positional,
		FileName:   flags.file,
		Replace:    flags.replace,
		ReadFile:   settings.readFile,
	}

	if loaded != nil {
		source.File = loaded.Questions
	}

	built, err := plan.Assemble(source)
	if err != nil {
		return err
	}

	built.Model = flags.model
	if built.Model == "" && loaded != nil {
		built.Model = loaded.Model
	}

	warnings, err := plan.Validate(built, cfg)

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

	// After validation, since the checker reads a plan the run has already accepted, and before
	// every mode below, because §17.3 has an assertion checked against the plan rather than
	// against an answer and §11 opens with every check running before any network call.
	gate, err := gateOf(fileAssertion(loaded), flags.assert, built)
	if err != nil {
		return err
	}

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
		return withStats(cmd, flags, func(stats *collector) error {
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
			return fmt.Errorf("jev: %w", err)
		}
	}

	if requests(flags) && resolved.Source == input.SourceNone {
		return errors.New(
			"jev: no state given. Pipe one to stdin, or pass --state or --state-file")
	}

	// After the state is resolved, so the body carries the state a real run would send.
	if flags.printRequest {
		return printRequest(cmd.OutOrStdout(), built.Questions, resolved,
			jev.ResolveModel(built.Model, settings.lookupEnv))
	}

	// Checked here rather than at write time, so a taken key costs no request. Section 11 opens
	// with every check running before any network call.
	if merging(flags) && hasKey(resolved.State, mergeKey(flags)) {
		return fmt.Errorf(
			"jev: --merge would overwrite the input's '%s' key. Pass --merge-key",
			mergeKey(flags))
	}

	return withStats(cmd, flags, func(stats *collector) error {
		return ask(cmd, settings, built, resolved, outputMode, flags, gate, stats)
	})
}

func gateOf(fileSource string, sources []string, built *plan.Plan) (*assert.Expr, error) {
	exprs := make([]*assert.Expr, 0, len(sources)+1)

	// The file leads and the command line follows. The file's assertion is the more general gate,
	// and argv puts --assert after the -f that named the file. §17.5 combines the two by and, and
	// and is commutative, so the order is a matter of which one an error names first.
	if fileSource != "" {
		// Spelled the way the file spells it, which is the rule plan holds every other message to
		// that names where a setting came from.
		expr, err := gateExpr(fileSource, "'assert'", built)
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, expr)
	}

	for _, source := range sources {
		expr, err := gateExpr(source, "--assert", built)
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, expr)
	}

	return assert.Combine(exprs...), nil
}

func gateExpr(source, named string, built *plan.Plan) (*assert.Expr, error) {
	expr, err := assert.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("jev: %s: %w", named, err)
	}

	// Each source is checked on its own rather than the combined gate, so a message names the one
	// that carried the mistake. Every source is a boolean in its own right, so a combination adds
	// nothing for the checker to reject.
	if checkErr := assert.Check(expr, built); checkErr != nil {
		return nil, fmt.Errorf("jev: %s: %w", named, checkErr)
	}

	return expr, nil
}

func fileAssertion(loaded *qfile.File) string {
	if loaded == nil {
		return ""
	}

	return loaded.Assert
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
	model := jev.ResolveModel(built.Model, settings.lookupEnv)
	source := input.NewStream(settings.stdin, inputMode, flags.skipBlank)
	out := cmd.OutOrStdout()
	merge := merging(flags)

	result, err := engine.Run(cmd.Context(), engine.Config[line]{
		Source: source,
		Evaluate: func(ctx context.Context, rec input.Record) (line, error) {
			if rec.Err != nil {
				// A line jev could not read is a record that never became a request, and it
				// carried no questions to the wire either.
				stats.recordFailure(rec.Err, false, 0)

				return line{record: failureRecord(built, rec.Err), raw: rec.Raw}, rec.Err
			}

			if merge && hasKey(rec.State, mergeKey(flags)) {
				stats.recordFailure(nil, false, 0)

				// Detected here rather than inside Write, because the engine accounts a failure
				// from the evaluator's error and a rewrite inside Write would be counted as a
				// success. One such input is that record's problem, not the batch's.
				//
				// Built as a LineError so describe gives it kind input rather than the transport
				// default, so --stop-on-error reaches the row that exits 2, and so the line number
				// comes along without a jev prefix inside the JSON.
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
				// Returned before the gate is asked, which is §17.4's third row. A failed record
				// reaches the evaluator as an answer whose every value reads as zero, so a gate
				// such as answer.value < 0.5 would hold for a request that never happened. The
				// single shot path is safe because its error return precedes the evaluation, and
				// this path writes the record rather than returning it, so the check is explicit.
				return line{record: record, raw: rec.Raw, state: rec.Wire}, evalErr
			}

			// §17.6 asks the gate per record. A false assertion is not an engine failure: the
			// record succeeded and the answer is complete, so it is carried on the line rather
			// than returned as an error the engine would count against result.Failed.
			record.AssertFailed = asserted(gate, record, stats)

			return line{record: record, raw: rec.Raw, state: rec.Wire}, nil
		},
		Write: func(l line) error {
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
	// JSON line keeps its digits. It is nil for a record jev could not read.
	state any
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
	return errors.Is(err, jev.ErrAuthentication) || errors.Is(err, jev.ErrPermissionDenied)
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
		// Reported by returning it rather than by printing here. Execute already writes the error
		// to stderr and adds the jev prefix only when it is missing, so printing here as well
		// would report the abort twice, and once with a doubled prefix.
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
	model := jev.ResolveModel(built.Model, settings.lookupEnv)

	record, err := evaluate(
		cmd.Context(), client, built, model, questions, resolved.Wire, flags.usage, stats)
	if err != nil {
		// The exit code still comes from the error. This only adds the fallback word the caller
		// asked for, so a shell guard reads a decision rather than an empty string. An interrupt
		// is skipped: the caller ended the run themselves, and a transport record written into the
		// pipe they were closing reports a network fault that never happened.
		if !errors.Is(err, context.Canceled) {
			if writeErr := writeFailure(
				cmd, settings, outputMode, flags, resolved, record); writeErr != nil {
				return writeErr
			}
		}

		return err
	}

	// Only a record that arrived is evaluated, which is §17.4's third row. A failed request
	// returns above, and it would reach the evaluator as an answer whose every value reads as
	// zero, so a gate such as answer.value < 0.5 would hold for a request that never happened.
	record.AssertFailed = asserted(gate, record, stats)

	if flags.quiet {
		// Both the policy and the assertion are asked, so neither masks the other. §17.5.
		if rejectErr := quietResult(
			built.Questions[0], record.Answers[0].Answer); rejectErr != nil {
			return rejectErr
		}

		return assertResult(record)
	}

	// The record prints whatever the assertion said, §17.4, and the exit code follows it.
	if writeErr := writeRecord(cmd, settings, outputMode, flags, resolved, record); writeErr != nil {
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
) error {
	// -q suppresses output entirely, so the exit code carries the whole result.
	if flags.quiet {
		return nil
	}

	return writeRecord(cmd, settings, mode, flags, resolved, record)
}

func writeRecord(
	cmd *cobra.Command,
	settings rootSettings,
	mode output.Mode,
	flags *runFlags,
	resolved input.Resolved,
	record output.Record,
) error {
	if merging(flags) {
		return writeMerged(cmd.OutOrStdout(), mode, record, resolved, flags)
	}

	if mode == output.Table {
		return output.WriteTable(
			cmd.OutOrStdout(), record, output.Width(settings.lookupEnv, terminalWidth))
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
		// carry the same remedy. Every question on this path passed jev's own bounds check, so a
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
			// A failure record rather than an empty one. In a stream an empty record writes a bare
			// {} with no error key, which a consumer reads as a successful answer to nothing.
			// Typed rather than bare, so section 8 gives it kind response with status 200 rather
			// than the transport default, and a single shot run exits 4.
			missing := &jev.ResponseError{
				Status:  http.StatusOK,
				Message: fmt.Sprintf("jev: response carries no answer for '%s'", question.ID),
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
		// for a line jev could not read.
		return &output.Failure{Kind: "input", Message: cause.Error()}
	}

	var unusable *jev.ResponseError
	if errors.As(cause, &unusable) {
		// A 2xx whose body jev could not use is deterministic. Calling it http would name a status
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
	output    string
	raw       bool
	quiet     bool
	usage     bool
	input     string
	state     string
	stateFile string
	model     string
	apiKey    string
	baseURL   string
	file      string
	replace   bool

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
