package cli

import (
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
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

func streamRaw(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	stats *runner.Collector,
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

	// --stop-on-assert is refused under -i request, whose bodies carry no assertion.
	source := runner.NewResumed(
		input.NewStream(settings.stdin, input.Request, input.StreamOptions{SkipBlank: flags.skipBlank}),
		runner.ResumedConfig{Resume: resume, StopOnError: flags.stopOnError, Speller: plan.Flags},
	)

	result, err := engine.Run(cmd.Context(), engine.Config[input.Record, []byte]{
		Source:      source,
		Evaluate:    run.evaluate,
		Write:       writeBodies(cmd.OutOrStdout()),
		Jobs:        flags.jobs,
		Unordered:   flags.unordered,
		StopOnError: flags.stopOnError,
		Abort:       runner.Aborting,
	})

	result, err = runner.FinishStream(cmd.ErrOrStderr(), source, stats, result, err)
	if err != nil {
		return err
	}

	// A request body carries no assertion, so no record can have failed one.
	return streamResult(result, 0, 0)
}

func newRawRun(cmd *cobra.Command, settings rootSettings, flags runConfig, stats *runner.Collector) (*rawRun, error) {
	// --print-request needs the resolution too, to print the model a body with none is sent with.
	chosen, err := settings.holder.resolved()
	if err != nil {
		return nil, err
	}

	run := &rawRun{
		flags: flags, stats: stats, provider: chosen.Provider,
		defaultModel: chosen.ModelFor("", settings.lookupEnv), long: flags.env.longContext,
	}

	// Built only when the run makes a request, so -i request --print-request needs no key.
	if requests(flags) {
		run.client, err = settings.newClient(cmd.Context(), chosen, runOptions(flags, stats)...)
		if err != nil {
			return nil, err
		}

		run.defaultModel = run.client.DefaultModel()
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
	stats        *runner.Collector
	provider     onesie.Provider
	defaultModel string
	long         bool
	client       *onesie.Client
}

func (r *rawRun) evaluate(ctx context.Context, rec input.Record) ([]byte, error) {
	if rec.Err != nil {
		// A value alongside the error, because the engine writes every outcome. A nil value
		// would print a blank line in place of the record.
		r.stats.RecordFailure(rec.Err, false, 0)

		return errorLine(rec.Err), rec.Err
	}

	body, err := r.provider.PrepareRaw(json.RawMessage(rec.Raw), r.defaultModel, longContextOptions(r.long)...)
	if err != nil {
		refused := &input.LineError{Line: rec.Line, Err: embed("", err)}
		r.stats.RecordFailure(refused, false, 0)

		return errorLine(refused), refused
	}

	// --print-request prints the body as it would be sent, with exit 0 and no network. The
	// same predicate decides this and the client above, so the two cannot disagree and leave
	// a nil client.
	if !requests(r.flags) {
		return body.Bytes(), nil
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
			r.stats.Spend(*unusable.Usage)
		}

		r.stats.RecordFailure(advised, true, body.QuestionCount())
		r.stats.TerminalAttempt(advised)

		return errorLine(advised), advised
	}

	r.stats.Record(result.Model, result.Usage, body.QuestionCount())

	return response, nil
}

func writeBodies(out io.Writer) func([]byte) error {
	return func(body []byte) error {
		_, err := fmt.Fprintln(out, string(body))

		return err
	}
}

func longContextOptions(on bool) []onesie.RequestOption {
	if !on {
		return nil
	}

	return []onesie.RequestOption{onesie.WithLongContext()}
}

func requests(flags runConfig) bool {
	return !flags.printQuestions && !flags.printRequest
}
