package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	setting "github.com/frodi-karlsson/onesie/internal/settings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func printSchema(w io.Writer) error {
	_, err := w.Write(qfile.Schema())

	return written(err)
}

func refuseBesideSchema(set *pflag.FlagSet, args []string) error {
	if len(args) > 0 {
		return errors.New("--print-schema asks no question. Drop the question argument")
	}

	var other string

	set.Visit(func(flag *pflag.Flag) {
		if other == "" && flag.Name != "print-schema" {
			other = flag.Name
		}
	})

	if other != "" {
		return fmt.Errorf("--print-schema takes no other flag. Drop --%s", other)
	}

	return nil
}

func printQuestions(
	out, errOut io.Writer,
	questions []plan.Question,
	assertion, abstainIf string,
	loaded *qfile.File,
	schemaURL string,
) error {
	if err := warnUncarried(errOut, loaded); err != nil {
		return err
	}

	file, err := qfile.Write(questions, assertion, abstainIf)
	if err != nil {
		return err
	}

	// A comment, so the loader never reads it, and yaml-language-server checks the file against the
	// schema it names.
	header := "# yaml-language-server: $schema=" + schemaURL + "\n"

	_, err = out.Write(append([]byte(header), file...))

	return written(err)
}

func warnUncarried(w io.Writer, loaded *qfile.File) error {
	// A question file has no model key and no state key, so only a request body loses anything on
	// the way into one.
	if loaded == nil || !loaded.IsBody {
		return nil
	}

	if loaded.Model != "" {
		err := output.Warning(w, fmt.Sprintf(
			"--print-questions does not carry the body's model '%s'. Pass -m when you reload", loaded.Model))
		if err != nil {
			return err
		}
	}

	if loaded.LongContext {
		if err := output.Warning(w,
			"--print-questions does not carry the body's long_context. "+
				"Pass --long-context when you reload"); err != nil {
			return err
		}
	}

	if !loaded.HasState {
		return nil
	}

	return output.Warning(w, "--print-questions does not carry the body's state. Pass --state when you reload")
}

func printRequest(w io.Writer, provider onesie.Provider, req onesie.Request, stated bool) error {
	body, err := requestBody(provider, req, stated)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(w, string(body))

	return written(err)
}

func requestBody(provider onesie.Provider, req onesie.Request, stated bool) ([]byte, error) {
	if !stated {
		return onesie.MarshalQuestionsBody(req)
	}

	prepared, err := provider.Prepare(req)
	if err != nil {
		return nil, err
	}

	return prepared.Bytes(), nil
}

func streamRequests(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	inv *invocation,
	resume answersfile.Resume,
) error {
	chosen, err := connected(settings, flags)
	if err != nil {
		return err
	}

	run := &requestRun{
		built: inv.plan, mapper: inv.mapper, images: inv.images, provider: chosen.provider,
		model: chosen.modelFor(inv.plan.Model), questions: plan.WireAll(inv.plan.Questions),
	}
	stops := stopsOf(flags)
	source := records(cmd.Context(), settings, flags.inputMode, flags, inv.namer, nil, resume, output.JSON, stops)

	result, err := engine.Run(cmd.Context(), engine.Config[namedRecord, []byte]{
		Source:      source,
		Evaluate:    run.evaluate,
		Write:       writeBodies(cmd.OutOrStdout()),
		Jobs:        setting.Get(flags.merged, setting.Jobs),
		Unordered:   setting.Get(flags.merged, setting.Unordered),
		StopOnError: stops.has(output.Error),
		Abort:       aborting,
	})
	source.stop()

	if err != nil {
		return &sourceError{cause: err, failed: result.Failed}
	}

	// A dry run asks no question, so no record can have carried an assertion.
	return streamResult(result, output.Tally{})
}

type requestRun struct {
	built     *plan.Plan
	mapper    *jq.Expr
	images    *imaging
	provider  onesie.Provider
	model     string
	questions onesie.Questions
}

func (r *requestRun) evaluate(ctx context.Context, rec namedRecord) ([]byte, error) {
	if rec.Err != nil {
		// A value alongside the error, because the engine writes every outcome. Returning
		// nil here would print a blank line rather than the record.
		return errorLine(rec.Err), rec.Err
	}

	if interrupted(ctx) {
		return nil, ctx.Err()
	}

	if rec.idErr != nil {
		return badLine(rec, rec.idErr)
	}

	state, wire := r.images.wrapLine(rec.State, rec.Wire)

	sent, mapErr := mapped(ctx, r.mapper, state, wire)
	if interrupted(ctx) {
		return nil, ctx.Err()
	}

	if mapErr != nil {
		return badLine(rec, mapErr)
	}

	sent, assets, imageErr := r.images.attach(sent)
	if imageErr != nil {
		return badLine(rec, imageErr)
	}

	prepared, prepareErr := r.provider.Prepare(requestFor(r.built, r.model, r.questions, sent, assets))
	if prepareErr != nil {
		return badLine(rec, embed("", prepareErr))
	}

	return prepared.Bytes(), nil
}

func badLine(rec namedRecord, cause error) ([]byte, error) {
	bad := &input.LineError{Line: rec.Line, Err: cause}

	return errorLine(bad), bad
}

func errorLine(cause error) []byte {
	return output.EncodeFailure(describe(cause))
}

func written(err error) error {
	// The consumer stopped reading, which is its right. The engine already ends a stream this way,
	// and a listing or a dry run piped into head must not exit differently for it.
	if engine.BrokenPipe(err) {
		return nil
	}

	return err
}
