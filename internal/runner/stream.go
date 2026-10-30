// Package runner asks every record of an input and writes each answer, the run loop onesie and
// onesie-mcp share.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Stream asks every record of an input, writes each answer and resumes an answers file. An error is
// one the run could not carry on past. The records' own outcome comes back in the StreamOutcome.
func Stream(ctx context.Context, config StreamConfig) (StreamOutcome, error) {
	run, err := newStreamRun(ctx, config)
	if err != nil {
		return StreamOutcome{}, err
	}

	result, err := engine.Run(ctx, engine.Config[NamedRecord, line]{
		Source:      run.source,
		Evaluate:    run.evaluate,
		Write:       run.write,
		Jobs:        config.Jobs,
		Unordered:   config.Unordered,
		StopOnError: config.StopOnError,
		Abort:       Aborting,
		Stop:        run.stop,
	})

	return run.finish(ctx, result, err)
}

// StreamConfig is everything a stream reads, asks with and writes to.
type StreamConfig struct {
	Input      io.Reader
	InputMode  input.Mode
	SkipBlank  bool
	Out        io.Writer
	ErrOut     io.Writer
	OutputMode output.Mode
	Ask        Ask
	Gate       *assert.Expr
	Abstain    *assert.Expr
	Mapper     *jq.Expr
	Namer      *jq.Expr
	// Images attaches the images of a record. Nil means no images.
	Images       Images
	Answerers    AnswererFactory
	Stats        *Collector
	Answers      *answersfile.File
	Resume       answersfile.Resume
	Jobs         int
	Unordered    bool
	StopOnError  bool
	StopOnAssert bool
	NoDedup      bool
	// DedupLimit is the most distinct requests held to deduplicate against. Zero means
	// limits.MaxDedupRequests.
	DedupLimit int
	Merge      bool
	// MergeKey is the key a merged record carries its answers under.
	MergeKey string
	Prune    bool
	Speller  plan.Speller
}

// Images wraps a text line under its image field and attaches the images a record names.
type Images interface {
	WrapLine(state, wire any) (any, any)
	Attach(state any) (any, onesie.Assets, error)
}

// StreamOutcome is the engine's result with the gate outcomes of the records. NoticeErr is a notice
// the run failed to print, which the records' own outcome outranks.
type StreamOutcome struct {
	engine.Result

	FalseAsserts int
	Abstains     int
	NoticeErr    error
}

func newStreamRun(ctx context.Context, config StreamConfig) (*streamRun, error) {
	answerer, err := config.Answerers(ctx, config.Stats)
	if err != nil {
		return nil, err
	}

	format := answersfile.Format{
		Mode: config.OutputMode, Merge: config.Merge, MergeKey: config.MergeKey, Namer: config.Namer,
		Gated: config.Gate != nil,
	}

	book, err := answersfile.ResumeLedger(ctx, config.Answers, format)
	if err != nil {
		return nil, err
	}

	resume, err := answersfile.ResumedVerdicts(ctx, config.Answers, format, config.StopOnError, config.Resume)
	if err != nil {
		return nil, err
	}

	built, gated := config.Ask.Plan, config.Gate != nil
	run := &streamRun{
		config:   config,
		answerer: answerer,
		shared:   dedupFor(config),
		source:   NewNaming(ctx, recordsConfig(config, book, resume)),
		book:     book,
		table: DelimitedFor(
			config.Out, config.OutputMode, built, gated, config.Namer != nil && !config.Merge, resume.Header),
		md: MarkdownFor(config.Out, config.OutputMode, built, gated, config.Namer != nil),
	}

	run.stop = watched(stopping(config.StopOnAssert), &run.stopped)

	return run, nil
}

func dedupFor(config StreamConfig) *dedup {
	if config.NoDedup {
		return nil
	}

	limit := config.DedupLimit
	if limit == 0 {
		limit = limits.MaxDedupRequests
	}

	return newDedup(limit, func() error {
		return output.Notice(config.ErrOut, fmt.Sprintf(
			"%d distinct requests held, so later records are deduplicated only against those", limit))
	})
}

func recordsConfig(config StreamConfig, book *answersfile.Ledger, resume answersfile.Resume) RecordsConfig {
	return RecordsConfig{
		ResumedConfig: ResumedConfig{
			Resume:       resume,
			OutputMode:   config.OutputMode,
			StopOnAssert: config.StopOnAssert,
			StopOnError:  config.StopOnError,
			Speller:      config.Speller,
		},
		Input:     config.Input,
		InputMode: config.InputMode,
		SkipBlank: config.SkipBlank,
		Namer:     config.Namer,
		Book:      book,
	}
}

type streamRun struct {
	config   StreamConfig
	answerer Answerer
	shared   *dedup
	source   *Naming
	book     *answersfile.Ledger
	table    *output.Delimited
	md       *output.MarkdownTable
	stop     func(line) bool
	stopped  atomic.Bool
	halted   bool
}

func (r *streamRun) evaluate(ctx context.Context, rec NamedRecord) (line, error) {
	l, err := r.lineFor(ctx, rec)
	l.slot = rec.Slot

	if r.config.InputMode == input.Lines {
		l.record.Line = rec.Line
	}

	return l, err
}

func (r *streamRun) lineFor(ctx context.Context, rec NamedRecord) (line, error) {
	if rec.Err != nil {
		// A line onesie could not read is a record that never became a request, and it
		// carried no questions to the wire either.
		r.config.Stats.RecordFailure(rec.Err, false, 0)

		return line{record: r.config.Ask.Failure(rec.Err), raw: rec.Raw, header: rec.Header}, rec.Err
	}

	if interrupted(ctx) {
		return line{}, ctx.Err()
	}

	if rec.IDErr != nil {
		return r.failed(rec, rec.IDErr)
	}

	if r.config.Merge && r.table == nil && HasKey(rec.State, r.config.MergeKey) {
		return r.mergeCollision(rec)
	}

	state, wire := r.wrapLine(rec.State, rec.Wire)

	sent, mapErr := Mapped(ctx, r.config.Mapper, state, wire, r.config.Speller)
	if interrupted(ctx) {
		return line{}, ctx.Err()
	}

	if mapErr != nil {
		return r.failed(rec, mapErr)
	}

	// In the worker rather than the reader, so at most -j records hold image bytes at once.
	sent, assets, imageErr := r.attach(sent)
	if imageErr != nil {
		return r.failed(rec, imageErr)
	}

	return r.answer(ctx, rec, sent, assets)
}

func (r *streamRun) wrapLine(state, wire any) (any, any) {
	if r.config.Images == nil {
		return state, wire
	}

	return r.config.Images.WrapLine(state, wire)
}

func (r *streamRun) attach(state any) (any, onesie.Assets, error) {
	if r.config.Images == nil {
		return state, nil, nil
	}

	return r.config.Images.Attach(state)
}

func (r *streamRun) failed(rec NamedRecord, cause error) (line, error) {
	bad := &input.LineError{Line: rec.Line, Err: cause}
	r.config.Stats.RecordFailure(bad, false, 0)

	return rowLine(r.config.Ask.Failure(bad), rec), bad
}

func (r *streamRun) mergeCollision(rec NamedRecord) (line, error) {
	r.config.Stats.RecordFailure(nil, false, 0)

	// Detected here rather than inside Write, since the engine counts a failure from the evaluator's
	// error and a rewrite inside Write would count as a success.
	//
	// A LineError gets kind input rather than the transport default from describe, so --stop-on-error
	// reaches the row, and the line number comes along without a onesie prefix inside the JSON.
	speller := r.config.Speller
	taken := &input.LineError{
		Line: rec.Line,
		Err: fmt.Errorf("%s would overwrite the input's '%s' key, pass %s",
			speller.Spell("--merge"), r.config.MergeKey, speller.Spell("--merge-key")),
	}

	return line{
		record: r.config.Ask.Failure(taken),
		// raw is deliberately left empty, which is what sends merge down the wrapper path rather than
		// the splice. The object goes under state as raw JSON rather than a Go map, so its key order
		// survives.
		state: json.RawMessage(compact(rec.Raw)),
	}, taken
}

func (r *streamRun) answer(ctx context.Context, rec NamedRecord, sent any, assets onesie.Assets) (line, error) {
	key := RecordKey{Position: rec.Index + 1, Line: rec.Line, ID: rec.ID}
	ask, stats := r.config.Ask, r.config.Stats

	// A record the mock file does not answer shares with nothing, so its error names its own line.
	group := r.shared

	salt, shares := r.answerer.Salt(key)
	if !shares {
		group = nil
	}

	record, call, err := askOnce(ctx, group, stats, ask.Request(sent, assets), salt,
		func() (output.Record, error) {
			return ask.Record(ctx, r.answerer, key, sent, assets, stats)
		})
	if err != nil {
		// Returned before the gate is asked. A failed record reads as all zeros, so a gate such as
		// answer.value < 0.5 would hold for a request that never happened.
		failed := rowLine(record, rec)
		failed.halted = Halting(err)
		failed.call = call

		return failed, err
	}

	// A false assertion or an abstain is not an engine failure: the record succeeded and the answer
	// is complete, so the outcome is carried on the line rather than returned as an error the engine
	// would count against result.Failed.
	record = Judge(r.config.Gate, r.config.Abstain, record, stats)

	answered := rowLine(record, rec)
	answered.call = call

	return answered, nil
}

func (r *streamRun) write(l line) error {
	// The first record that halts the run, such as one a mock file does not answer, drops every line
	// after it too, so the output holds only the records before it and a resume asks the rest.
	if r.halted || l.halted {
		r.halted = true

		return nil
	}

	if r.config.Ask.Usage {
		billOnce(&l)
	}

	if r.book == nil {
		return r.writeLine(l)
	}

	answers := r.config.Answers
	start := answers.Offset()
	err := r.writeLine(l)
	r.book.Wrote(l.slot, answersfile.Span{Start: start, End: answers.Offset()})

	return err
}

func (r *streamRun) writeLine(l line) error {
	merge := r.config.Merge

	switch {
	case r.md != nil:
		return r.md.Write(l.record)
	case r.table != nil && !merge:
		return r.table.Write(l.record, nil, nil)
	case r.table != nil:
		return r.table.Write(l.record, l.header, l.fields)
	case !merge:
		return output.Write(r.config.Out, r.config.OutputMode, l.record)
	default:
		return output.WriteMerged(r.config.Out, r.config.OutputMode, l.record, l.raw, l.state, r.config.MergeKey)
	}
}

func (r *streamRun) finish(ctx context.Context, result engine.Result, err error) (StreamOutcome, error) {
	r.source.Stop()

	if finishErr := r.finishMarkdown(ctx, result, err); finishErr != nil {
		return StreamOutcome{}, finishErr
	}

	stats := r.config.Stats

	result, err = FinishStream(r.config.ErrOut, r.source, stats, result, err)
	if err != nil {
		return StreamOutcome{}, err
	}

	// Only a run that read and wrote every record rewrites the file. Anything short of that leaves
	// the appended lines as they are, for the next resume to finish.
	if r.book != nil && !result.Aborted && !result.Broken && !r.stopped.Load() && r.book.Complete() {
		r.config.Answers.CompactInto(r.book.TakeOrder(r.config.Prune), r.config.OutputMode)
	}

	return StreamOutcome{
		Result:       result,
		FalseAsserts: stats.falseAssertions(),
		Abstains:     stats.abstains(),
		NoticeErr:    r.shared.noticeError(),
	}, nil
}

func (r *streamRun) finishMarkdown(ctx context.Context, result engine.Result, err error) error {
	if r.md == nil || result.Broken {
		return nil
	}

	whole := err == nil && !result.Aborted && !r.stopped.Load() && !interrupted(ctx)

	// A consumer that stopped reading before the summary leaves the records' exit code to stand.
	finishErr := r.md.Finish(whole)
	if finishErr != nil && !engine.BrokenPipe(finishErr) && err == nil && !result.Aborted {
		return finishErr
	}

	return nil
}

// FinishStream counts the records a resume skipped, prints the source's id hint and turns a source
// that stopped the run into a SourceError.
func FinishStream(
	w io.Writer, source Resumable, stats *Collector, result engine.Result, err error,
) (engine.Result, error) {
	skips := source.Skipped()
	stats.Skip(skips)

	if hint := source.IDHint(); hint != "" {
		if noticeErr := output.Notice(w, hint); noticeErr != nil {
			return result, noticeErr
		}
	}

	// A stored failure a resume skipped fails the run as it failed the one that wrote it.
	result.Failed += skips.Failed

	if freshAbort := source.FreshRunAbort(); freshAbort != nil {
		return result, freshAbort
	}

	if err != nil {
		// The source stopping the run is the worse outcome and takes the exit code, since a truncated
		// stream is not something a caller can tell from a complete one. The records that had already
		// failed are named in the message rather than dropped.
		return result, &SourceError{Err: err, Failed: result.Failed}
	}

	return result, nil
}

// Error names the cause, and the records that had failed before it.
func (e *SourceError) Error() string {
	if e.Failed == 0 {
		return text(e.Err)
	}

	return fmt.Sprintf("%s, after %s failed", text(e.Err), plural(e.Failed, "record"))
}

// Unwrap returns the cause.
func (e *SourceError) Unwrap() error {
	return e.Err
}

// SourceError is a source that stopped the run, after Failed records had failed.
type SourceError struct {
	Err    error
	Failed int
}

// Aborting reports whether err ends the whole run rather than its record.
func Aborting(err error) bool {
	// Every later record would fail the same way, so a bad key is reported on one line for the
	// whole run.
	return AuthFailure(err) || Halting(err)
}

// AuthFailure reports whether err is a refused key, a refused permission or an account out of
// credits.
func AuthFailure(err error) bool {
	return errors.Is(err, onesie.ErrAuthentication) || errors.Is(err, onesie.ErrPermissionDenied) ||
		errors.Is(err, onesie.ErrPaymentRequired)
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

func stopping(stopOnAssert bool) func(line) bool {
	if !stopOnAssert {
		// Nil rather than a predicate that always says no, so the engine asks nothing of a run
		// that did not pass the flag.
		return nil
	}

	return func(l line) bool {
		return l.record.AssertFailed
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

func rowLine(record output.Record, rec NamedRecord) line {
	var fields map[string]any
	if object, ok := rec.State.(map[string]any); ok {
		fields = object
	}

	record.ID = rec.ID

	return line{record: record, raw: rec.Raw, state: rec.Wire, header: rec.Header, fields: fields}
}

func compact(raw string) []byte {
	var flat bytes.Buffer

	if err := json.Compact(&flat, []byte(raw)); err != nil {
		// Unreachable for a line that parsed, the only kind compact is called with.
		return []byte(raw)
	}

	return flat.Bytes()
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

// DelimitedFor is the csv or tsv table a run writes, or nil for any other output mode.
func DelimitedFor(
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

// MarkdownFor is the markdown table a run writes, or nil for any other output mode.
func MarkdownFor(
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
