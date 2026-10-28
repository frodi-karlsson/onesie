package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
)

func streamRaw(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	stats *collector,
	answers *answersfile.File,
	resume answersfile.Resume,
) error {
	run, err := newRawRun(cmd, settings, flags, stats)
	if err != nil {
		return err
	}

	resume, err = answersfile.ResumedVerdicts(cmd.Context(), answers, answersfile.Format{Forwarded: true}, flags.stopOnError, resume)
	if err != nil {
		return err
	}

	source := &resumed{
		source: input.NewStream(settings.stdin, input.Request, flags.skipBlank),
		left:   resume.Skip,
		stored: resume.Stored,
		// --stop-on-assert is refused under -i request, whose bodies carry no assertion.
		haltOnError: flags.stopOnError,
	}

	result, err := engine.Run(cmd.Context(), engine.Config[input.Record, []byte]{
		Source:      source,
		Evaluate:    run.evaluate,
		Write:       writeBodies(cmd.OutOrStdout()),
		Jobs:        flags.jobs,
		Unordered:   flags.unordered,
		StopOnError: flags.stopOnError,
		Abort:       aborting,
	})

	result, err = finishStream(cmd.ErrOrStderr(), source, stats, result, err)
	if err != nil {
		return err
	}

	// A request body carries no assertion, so no record can have failed one.
	return streamResult(result, 0, 0)
}

func newRawRun(cmd *cobra.Command, settings rootSettings, flags runConfig, stats *collector) (*rawRun, error) {
	// --print-request needs the resolution too, to print the model a body with none is sent with.
	chosen, err := settings.holder.resolved()
	if err != nil {
		return nil, err
	}

	run := &rawRun{
		flags: flags, stats: stats, provider: chosen.provider,
		defaultModel: chosen.modelFor("", settings.lookupEnv), long: flags.env.longContext,
	}

	// Built only when the run makes a request, so -i request --print-request needs no key.
	if requests(flags) {
		run.client, err = settings.newClient(cmd.Context(), chosen, runOptions(flags, stats)...)
		if err != nil {
			return nil, err
		}
	}

	// Every body would be refused, so the run stops before it reads any.
	if run.long {
		if refusal := run.provider.Refuse(run.defaultModel, onesie.Needs{LongContext: true}); refusal != nil {
			return nil, longContextCause(refusal, flags)
		}
	}

	return run, nil
}

type rawRun struct {
	flags        runConfig
	stats        *collector
	provider     onesie.Provider
	defaultModel string
	long         bool
	client       *onesie.Client
}

func (r *rawRun) evaluate(ctx context.Context, rec input.Record) ([]byte, error) {
	if rec.Err != nil {
		// A value alongside the error, because the engine writes every outcome. A nil value
		// would print a blank line in place of the record.
		r.stats.recordFailure(rec.Err, false, 0)

		return errorLine(rec.Err), rec.Err
	}

	// --print-request prints the body as it would be sent, with exit 0 and no network. The
	// same predicate decides this and the client above, so the two cannot disagree and leave
	// a nil client.
	if !requests(r.flags) {
		return printedBody(r.provider, rec.Raw, r.defaultModel, r.long)
	}

	body, err := r.provider.PrepareRaw(json.RawMessage(rec.Raw), r.client.DefaultModel(), longContextOptions(r.long)...)
	if err != nil {
		r.stats.recordFailure(err, false, 0)

		return errorLine(err), err
	}

	return r.send(ctx, body)
}

func (r *rawRun) send(ctx context.Context, body *onesie.PreparedBody) ([]byte, error) {
	result, response, err := r.client.SendPrepared(ctx, body)
	if err != nil {
		advised := explain(err, requestFacts{
			provider: r.provider, model: body.Model(), longContext: body.LongContext(), boxed: body.Box(),
		})

		var unusable *onesie.ResponseError
		if errors.As(err, &unusable) && unusable.Usage != nil {
			r.stats.spend(*unusable.Usage)
		}

		r.stats.recordFailure(advised, true, body.QuestionCount())
		r.stats.terminalAttempt(advised)

		return errorLine(advised), advised
	}

	r.stats.record(result.Model, result.Usage, body.QuestionCount())

	return oneLine(response), nil
}

func writeBodies(out io.Writer) func([]byte) error {
	return func(body []byte) error {
		_, err := fmt.Fprintln(out, string(body))

		return err
	}
}

func printedBody(provider onesie.Provider, raw, defaultModel string, long bool) ([]byte, error) {
	body, err := provider.PrepareRaw(json.RawMessage(raw), defaultModel, longContextOptions(long)...)
	if err != nil {
		return errorLine(err), err
	}

	return body.Bytes(), nil
}

func longContextOptions(on bool) []onesie.RequestOption {
	if !on {
		return nil
	}

	return []onesie.RequestOption{onesie.WithLongContext()}
}

func requests(flags runConfig) bool {
	// --print-request is why this is not folded into the early return --print-questions takes. It
	// includes any state supplied, so it goes through input.Resolve and needs a question only body
	// when nothing was.
	return !flags.printQuestions && !flags.printRequest
}

func oneLine(response []byte) []byte {
	// A server that newline terminates or pretty prints its JSON would otherwise turn one record
	// into several output lines, where this mode promises one line per record.
	var flat bytes.Buffer
	if err := json.Compact(&flat, response); err != nil {
		return response
	}

	return flat.Bytes()
}
