package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/jev-cli/internal/answer"
	"github.com/frodi-karlsson/jev-cli/internal/argv"
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
	var loaded *qfile.File

	if flags.file != "" {
		data, err := settings.readFile(flags.file)
		if err != nil {
			return fmt.Errorf("jev: reading %s: %w", flags.file, err)
		}

		loaded, err = qfile.Load(data)
		if err != nil {
			return err
		}
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

	inputMode, err := input.ParseMode(flags.input)
	if err != nil {
		return err
	}

	warnings, err := plan.Validate(built, plan.Config{
		Raw:          flags.raw,
		Quiet:        flags.quiet,
		Output:       flags.output,
		HasState:     cmd.Flags().Changed("state"),
		HasStateFile: cmd.Flags().Changed("state-file"),
		Replace:      flags.replace,
		FileName:     flags.file,
		Streaming:    inputMode.Streaming(),
		InputName:    inputName(flags.input),
		Unordered:    flags.unordered,
		StopOnError:  flags.stopOnError,
		SkipBlank:    flags.skipBlank,
		Merge:        merging(flags),
		Jobs:         flags.jobs,
		JobsSet:      cmd.Flags().Changed("jobs"),

		Timeout:          flags.timeout,
		TimeoutSet:       cmd.Flags().Changed("timeout"),
		Retries:          flags.retries,
		RetriesSet:       cmd.Flags().Changed("retries"),
		MaxRetryAfter:    flags.maxRetryAfter,
		MaxRetryAfterSet: cmd.Flags().Changed("max-retry-after"),
	})

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

	// Both modes are parsed before the request, so a mistyped flag costs nothing.
	outputMode, err := output.ParseMode(
		outputName(flags), settings.stdoutTTY, inputMode.Streaming(), merging(flags))
	if err != nil {
		return err
	}

	if inputMode.Streaming() {
		return stream(cmd, settings, built, inputMode, outputMode, flags)
	}

	resolved, err := input.Resolve(input.Request{
		Mode:         inputMode,
		Stdin:        settings.stdin,
		StdinTTY:     settings.stdinTTY,
		State:        flags.state,
		HasState:     cmd.Flags().Changed("state"),
		StateFile:    flags.stateFile,
		HasStateFile: cmd.Flags().Changed("state-file"),
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

	if resolved.Source == input.SourceNone {
		return errors.New(
			"jev: no state given. Pipe one to stdin, or pass --state or --state-file")
	}

	// Checked here rather than at write time, so a taken key costs no request. Section 11 opens
	// with every check running before any network call.
	if merging(flags) && hasKey(resolved.State, mergeKey(flags)) {
		return fmt.Errorf(
			"jev: --merge would overwrite the input's '%s' key. Pass --merge-key",
			mergeKey(flags))
	}

	return ask(cmd, settings, built, resolved, outputMode, flags)
}

func inputName(flag string) string {
	// The flag defaults to the empty string when it was not given, and every message that names
	// the mode would otherwise read -i with nothing after it.
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
) error {
	client, err := settings.newClient(cmd.Context())
	if err != nil {
		return err
	}

	questions := make(jev.Questions, 0, len(built.Questions))
	for _, question := range built.Questions {
		questions = append(questions, jev.NamedQuestion{ID: question.ID, Question: wire(question)})
	}

	source := input.NewStream(settings.stdin, inputMode, flags.skipBlank)
	out := cmd.OutOrStdout()
	merge := merging(flags)

	result, err := engine.Run(cmd.Context(), engine.Config[line]{
		Source: source,
		Evaluate: func(ctx context.Context, rec input.Record) (line, error) {
			if rec.Err != nil {
				return line{record: failureRecord(built, rec.Err), raw: rec.Raw}, rec.Err
			}

			if merge && hasKey(rec.State, mergeKey(flags)) {
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

			record, evalErr := evaluate(ctx, client, built, questions, rec.Wire, flags.usage)

			return line{record: record, raw: rec.Raw, state: rec.Wire}, evalErr
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
	})
	if err != nil {
		// The source stopping the run is the worse outcome and takes the exit code, since a
		// truncated stream is not something a caller can tell from a complete one. The records
		// that had already failed are named in the message rather than dropped.
		return &sourceError{cause: err, failed: result.Failed}
	}

	return streamResult(result)
}

type sourceError struct {
	cause  error
	failed int
}

func (e *sourceError) Error() string {
	if e.failed == 0 {
		return e.cause.Error()
	}

	return fmt.Sprintf("%v, after %s failed", e.cause, plural(e.failed, "record"))
}

func (e *sourceError) Unwrap() error {
	return e.cause
}

func plural(count int, noun string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, noun)
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

func merging(flags *runFlags) bool {
	return flags.merge || flags.mergeKey != ""
}

func mergeKey(flags *runFlags) string {
	if flags.mergeKey != "" {
		return flags.mergeKey
	}

	return "answers"
}

func streamResult(result engine.Result) error {
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

	return nil
}

type abortError struct {
	cause error
}

func (e *abortError) Error() string {
	return e.cause.Error()
}

func (e *abortError) Unwrap() error {
	return e.cause
}

type recordsError struct{}

func (*recordsError) Error() string {
	return "one or more records failed"
}

func ask(
	cmd *cobra.Command,
	settings rootSettings,
	built *plan.Plan,
	resolved input.Resolved,
	outputMode output.Mode,
	flags *runFlags,
) error {
	client, err := settings.newClient(cmd.Context())
	if err != nil {
		return err
	}

	questions := make(jev.Questions, 0, len(built.Questions))
	for _, question := range built.Questions {
		questions = append(questions, jev.NamedQuestion{ID: question.ID, Question: wire(question)})
	}

	record, err := evaluate(cmd.Context(), client, built, questions, resolved.Wire, flags.usage)
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

	if flags.quiet {
		return quietResult(built.Questions[0], record.Answers[0].Answer)
	}

	if merging(flags) {
		return writeMerged(cmd.OutOrStdout(), outputMode, record, resolved, flags)
	}

	if outputMode == output.Table {
		return output.WriteTable(cmd.OutOrStdout(), record, output.Width(settings.lookupEnv, terminalWidth))
	}

	return output.Write(cmd.OutOrStdout(), outputMode, record)
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

	if merging(flags) {
		return writeMerged(cmd.OutOrStdout(), mode, record, resolved, flags)
	}

	if mode == output.Table {
		return output.WriteTable(cmd.OutOrStdout(), record, output.Width(settings.lookupEnv, terminalWidth))
	}

	return output.Write(cmd.OutOrStdout(), mode, record)
}

func evaluate(
	ctx context.Context,
	client *jev.Client,
	built *plan.Plan,
	questions jev.Questions,
	state any,
	withUsage bool,
) (output.Record, error) {
	result, err := client.SystemOne(ctx, jev.Request{
		State:     state,
		Model:     built.Model,
		Questions: questions,
	})
	if err != nil {
		return failureRecord(built, err), err
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

			return failureRecord(built, missing), missing
		}

		normalized, normErr := answer.Normalize(question, raw)
		if normErr != nil {
			return failureRecord(built, normErr), normErr
		}

		answer.Apply(question, normalized)

		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: normalized})
	}

	return record, nil
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

func wire(q plan.Question) jev.Question {
	switch q.Shape {
	case plan.Pick:
		criteria := make(jev.Criteria, 0, len(q.Options))
		for _, option := range q.Options {
			criteria = append(criteria, jev.NamedCriterion{Name: option.Name, Desc: option.Desc})
		}

		return jev.Choice{Instructions: q.Instructions, Criteria: criteria}
	case plan.Rate:
		criteria := make([]any, 0, len(q.Levels))

		for _, level := range q.Levels {
			// An undescribed rubric sends the labels themselves, which is the documented
			// behaviour for a bare --rate. A body question has no labels, so substituting an
			// empty string would rewrite its criteria on the way out.
			if level.Desc == nil && q.Labelled {
				criteria = append(criteria, level.Label)

				continue
			}

			criteria = append(criteria, level.Desc)
		}

		return jev.Score{Instructions: q.Instructions, Criteria: criteria}
	default:
		if q.Criteria == nil {
			return jev.Noul{Instructions: q.Instructions}
		}

		return jev.Noul{
			Instructions: q.Instructions,
			Criteria:     &jev.NoulCriteria{True: q.Criteria.Yes, False: q.Criteria.No},
		}
	}
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

	jobs          int
	timeout       int
	retries       int
	maxRetryAfter int
	unordered     bool
	stopOnError   bool
	skipBlank     bool
	merge         bool
	mergeKey      string
}

type rejectedError struct{}

func (*rejectedError) Error() string {
	return "policy did not accept the answer"
}

type clientFactory func(ctx context.Context) (*jev.Client, error)

func defaultClientFactory(
	info BuildInfo,
	flags *runFlags,
	lookupEnv func(string) (string, bool),
) clientFactory {
	return func(context.Context) (*jev.Client, error) {
		opts := []jev.Option{
			jev.WithUserAgent("jev-cli/" + info.Version),
			jev.WithEnv(lookupEnv),
		}

		if transport := pooled(flags.jobs); transport != nil {
			opts = append(opts, jev.WithHTTPClient(&http.Client{Transport: transport}))
		}

		if flags.apiKey != "" {
			opts = append(opts, jev.WithAPIKey(flags.apiKey))
		}

		if flags.baseURL != "" {
			opts = append(opts, jev.WithBaseURL(flags.baseURL))
		}

		opts = append(opts, jev.WithAttemptTimeout(
			time.Duration(flags.timeout)*time.Second))

		// Started from the default rather than a zero value, because a Go struct cannot tell an
		// unset field from a zero one and the policy carries seven fields this run does not touch.
		policy := jev.DefaultRetryPolicy()
		policy.MaxRetries = flags.retries
		policy.MaxRetryAfter = time.Duration(flags.maxRetryAfter) * time.Second

		opts = append(opts, jev.WithRetry(policy))

		return jev.New(opts...)
	}
}

func pooled(jobs int) http.RoundTripper {
	if jobs < 1 {
		jobs = 1
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// Something replaced the default. Building one here would drop whatever proxy and dialer
		// settings that replacement carries, so the client keeps its own default instead.
		return nil
	}

	// Cloned rather than built from scratch, so proxy support, the dial timeouts and HTTP/2 come
	// along. The default of two idle connections per host means most of a -j run pays for a fresh
	// handshake on a request the server answers in a fraction of that time.
	transport := base.Clone()
	transport.MaxIdleConnsPerHost = jobs

	if transport.MaxIdleConns < jobs {
		transport.MaxIdleConns = jobs
	}

	return transport
}

func worthReporting(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}

	var rejected *rejectedError
	if errors.As(err, &rejected) {
		// -q suppresses output entirely, so its rejection is carried by the exit code alone.
		return false
	}

	var records *recordsError

	// The exit code already says a record failed, and the per record lines on stdout carry the
	// detail.
	return !errors.As(err, &records)
}
