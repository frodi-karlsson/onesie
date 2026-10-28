package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

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

	run, err := newStreamRun(cmd, settings, flags, inv, outputMode, stats, answers, resume, answerers)
	if err != nil {
		return err
	}

	result, err := engine.Run(cmd.Context(), engine.Config[namedRecord, line]{
		Source:      run.source,
		Evaluate:    run.evaluate,
		Write:       run.write,
		Jobs:        flags.jobs,
		Unordered:   flags.unordered,
		StopOnError: flags.stopOnError,
		Abort:       aborting,
		Stop:        run.stop,
	})

	return run.finish(cmd.Context(), result, err)
}

func newStreamRun(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	inv *invocation,
	outputMode output.Mode,
	stats *collector,
	answers *answersfile.File,
	resume answersfile.Resume,
	answerers answererFactory,
) (*streamRun, error) {
	asker, err := answerers(cmd.Context(), stats)
	if err != nil {
		return nil, err
	}

	model, err := resolveModel(settings, inv.plan.Model)
	if err != nil {
		return nil, err
	}

	format := answersfile.Format{
		Mode: outputMode, Merge: merging(flags), MergeKey: mergeKey(flags), Namer: inv.namer, Gated: inv.gate != nil,
	}

	book, err := answersfile.ResumeLedger(cmd.Context(), answers, format)
	if err != nil {
		return nil, err
	}

	resume, err = answersfile.ResumedVerdicts(cmd.Context(), answers, format, flags.stopOnError, resume)
	if err != nil {
		return nil, err
	}

	out := cmd.OutOrStdout()
	run := &streamRun{
		flags: flags, built: inv.plan, mapper: inv.mapper, images: inv.images, gate: inv.gate,
		abstain: inv.abstain, asker: asker, model: model, questions: wireAll(inv.plan.Questions), stats: stats,
		source: records(cmd.Context(), settings, flags.inputMode, flags, inv.namer, book, resume, outputMode),
		book:   book, answers: answers, out: out, outputMode: outputMode,
		table: delimited(out, outputMode, inv.plan, inv.gate != nil, inv.namer != nil && !merging(flags), resume.Header),
		md:    markdown(out, outputMode, inv.plan, inv.gate != nil, inv.namer != nil),
	}

	if !flags.noDedup {
		run.shared = newDedup(settings.dedupLimit, func() error {
			return output.Notice(cmd.ErrOrStderr(), fmt.Sprintf(
				"%d distinct requests held, so later records are deduplicated only against those",
				settings.dedupLimit))
		})
	}

	run.stop = watched(stopping(flags), &run.stopped)

	return run, nil
}

type streamRun struct {
	flags      runConfig
	built      *plan.Plan
	mapper     *jq.Expr
	images     *imaging
	gate       *assert.Expr
	abstain    *assert.Expr
	asker      answerer
	model      string
	questions  onesie.Questions
	stats      *collector
	shared     *dedup
	source     *naming
	book       *answersfile.Ledger
	answers    *answersfile.File
	out        io.Writer
	outputMode output.Mode
	table      *output.Delimited
	md         *output.MarkdownTable
	stop       func(line) bool
	stopped    atomic.Bool
	halted     bool
}

func (r *streamRun) evaluate(ctx context.Context, rec namedRecord) (line, error) {
	l, err := r.lineFor(ctx, rec)
	l.slot = rec.slot

	if r.flags.inputMode == input.Lines {
		l.record.Line = rec.Line
	}

	return l, err
}

func (r *streamRun) lineFor(ctx context.Context, rec namedRecord) (line, error) {
	if rec.Err != nil {
		// A line onesie could not read is a record that never became a request, and it
		// carried no questions to the wire either.
		r.stats.recordFailure(rec.Err, false, 0)

		return line{record: failureRecord(r.built, rec.Err), raw: rec.Raw, header: rec.Header}, rec.Err
	}

	if interrupted(ctx) {
		return line{}, ctx.Err()
	}

	if rec.idErr != nil {
		return r.failed(rec, rec.idErr)
	}

	if merging(r.flags) && r.table == nil && hasKey(rec.State, mergeKey(r.flags)) {
		return r.mergeCollision(rec)
	}

	state, wire := r.images.wrapLine(rec.State, rec.Wire)

	sent, mapErr := mapped(ctx, r.mapper, state, wire)
	if interrupted(ctx) {
		return line{}, ctx.Err()
	}

	if mapErr != nil {
		return r.failed(rec, mapErr)
	}

	// In the worker rather than the reader, so at most -j records hold image bytes at once.
	sent, assets, imageErr := r.images.attach(sent)
	if imageErr != nil {
		return r.failed(rec, imageErr)
	}

	return r.ask(ctx, rec, sent, assets)
}

func (r *streamRun) failed(rec namedRecord, cause error) (line, error) {
	bad := &input.LineError{Line: rec.Line, Err: cause}
	r.stats.recordFailure(bad, false, 0)

	return rowLine(failureRecord(r.built, bad), rec), bad
}

func (r *streamRun) mergeCollision(rec namedRecord) (line, error) {
	r.stats.recordFailure(nil, false, 0)

	// Detected here rather than inside Write, since the engine counts a failure from the evaluator's
	// error and a rewrite inside Write would count as a success.
	//
	// A LineError gets kind input rather than the transport default from describe, so --stop-on-error
	// reaches the row, and the line number comes along without a onesie prefix inside the JSON.
	taken := &input.LineError{
		Line: rec.Line,
		Err:  fmt.Errorf("--merge would overwrite the input's '%s' key, pass --merge-key", mergeKey(r.flags)),
	}

	return line{
		record: failureRecord(r.built, taken),
		// raw is deliberately left empty, which is what sends merge down the wrapper path rather than
		// the splice. The object goes under state as raw JSON rather than a Go map, so its key order
		// survives.
		state: json.RawMessage(compact(rec.Raw)),
	}, taken
}

func (r *streamRun) ask(ctx context.Context, rec namedRecord, sent any, assets onesie.Assets) (line, error) {
	key := recordKey{position: rec.Index + 1, line: rec.Line, id: rec.id}

	// A record the mock file does not answer shares with nothing, so its error names its own line.
	group := r.shared

	salt, shares := r.asker.salt(key)
	if !shares {
		group = nil
	}

	record, call, err := askOnce(ctx, group, r.stats,
		onesie.Request{
			State: sent, Model: r.model, Questions: r.questions, Assets: assets, LongContext: r.built.LongContext,
		}, salt,
		func() (output.Record, error) {
			return evaluate(ctx, r.asker, key, r.built, r.model, r.questions, sent, assets, r.flags.usage, r.stats)
		})
	if err != nil {
		// Returned before the gate is asked. A failed record reads as all zeros, so a gate such as
		// answer.value < 0.5 would hold for a request that never happened.
		failed := rowLine(record, rec)
		failed.halted = halting(err)
		failed.call = call

		return failed, err
	}

	// A false assertion or an abstain is not an engine failure: the record succeeded and the answer
	// is complete, so the outcome is carried on the line rather than returned as an error the engine
	// would count against result.Failed.
	record = judge(r.gate, r.abstain, record, r.stats)

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

	if r.flags.usage {
		billOnce(&l)
	}

	if r.book == nil {
		return r.writeLine(l)
	}

	start := r.answers.Offset()
	err := r.writeLine(l)
	r.book.Wrote(l.slot, answersfile.Span{Start: start, End: r.answers.Offset()})

	return err
}

func (r *streamRun) writeLine(l line) error {
	merge := merging(r.flags)

	switch {
	case r.md != nil:
		return r.md.Write(l.record)
	case r.table != nil && !merge:
		return r.table.Write(l.record, nil, nil)
	case r.table != nil:
		return r.table.Write(l.record, l.header, l.fields)
	case !merge:
		return output.Write(r.out, r.outputMode, l.record)
	default:
		return output.WriteMerged(r.out, r.outputMode, l.record, l.raw, l.state, mergeKey(r.flags))
	}
}

func (r *streamRun) finish(ctx context.Context, result engine.Result, err error) error {
	r.source.stop()

	if finishErr := r.finishMarkdown(ctx, result, err); finishErr != nil {
		return finishErr
	}

	result, err = finishStream(r.source, r.stats, result, err)
	if err != nil {
		return err
	}

	// Only a run that read and wrote every record rewrites the file. Anything short of that leaves
	// the appended lines as they are, for the next resume to finish.
	if r.book != nil && !result.Aborted && !result.Broken && !r.stopped.Load() && r.book.Complete() {
		r.answers.CompactInto(r.book.TakeOrder(r.flags.prune), r.outputMode)
	}

	// The records' own outcome wins, since a note that failed to print is the smaller loss.
	outcome := streamResult(result, r.stats.falseAssertions(), r.stats.abstains())
	if outcome == nil {
		return r.shared.noticeError()
	}

	return outcome
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

func finishStream(source resumable, stats *collector, result engine.Result, err error) (engine.Result, error) {
	skips := source.skipped()
	stats.skip(skips)

	// A stored failure a resume skipped fails the run as it failed the one that wrote it.
	result.Failed += skips.Failed

	if freshAbort := source.freshRunAbort(); freshAbort != nil {
		return result, freshAbort
	}

	if err != nil {
		// The source stopping the run is the worse outcome and takes the exit code, since a truncated
		// stream is not something a caller can tell from a complete one. The records that had already
		// failed are named in the message rather than dropped.
		return result, &sourceError{cause: err, failed: result.Failed}
	}

	return result, nil
}

type resumable interface {
	skipped() answersfile.Tally
	freshRunAbort() error
}
