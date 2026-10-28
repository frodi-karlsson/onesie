package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

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

func printRequest(
	w io.Writer,
	questions []plan.Question,
	source input.Source,
	state any,
	assets onesie.Assets,
	longContext bool,
	model string,
) error {
	req := onesie.Request{Model: model, Questions: wireAll(questions), LongContext: longContext}

	encode := marshalQuestionsBody
	if source != input.SourceNone {
		req.State = state
		req.Assets = assets
		encode = onesie.MarshalBody

		if validErr := onesie.ValidateRequest(req); validErr != nil {
			return validErr
		}
	}

	body, err := encode(req)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(w, string(body))

	return written(err)
}

func marshalQuestionsBody(req onesie.Request) ([]byte, error) {
	// MarshalBody writes a null state, which is a legitimate state, so a body without one leaves the key out.
	return json.Marshal(struct {
		Model       string           `json:"model"`
		Questions   onesie.Questions `json:"questions"`
		LongContext bool             `json:"long_context,omitempty"`
	}{Model: req.Model, Questions: req.Questions, LongContext: req.LongContext})
}

func streamRequests(
	cmd *cobra.Command,
	settings rootSettings,
	flags runConfig,
	inv *invocation,
	resume answersfile.Resume,
) error {
	model, err := resolveModel(settings, inv.plan.Model)
	if err != nil {
		return err
	}

	run := &requestRun{
		built: inv.plan, mapper: inv.mapper, images: inv.images, model: model, questions: wireAll(inv.plan.Questions),
	}
	source := records(cmd.Context(), settings, flags.inputMode, flags, inv.namer, nil, resume, output.JSON)

	result, err := engine.Run(cmd.Context(), engine.Config[namedRecord, []byte]{
		Source:      source,
		Evaluate:    run.evaluate,
		Write:       writeBodies(cmd.OutOrStdout()),
		Jobs:        flags.jobs,
		Unordered:   flags.unordered,
		StopOnError: flags.stopOnError,
		Abort:       aborting,
	})
	source.stop()

	if err != nil {
		return &sourceError{cause: err, failed: result.Failed}
	}

	// A dry run asks no question, so no record can have carried an assertion.
	return streamResult(result, 0, 0)
}

type requestRun struct {
	built     *plan.Plan
	mapper    *jq.Expr
	images    *imaging
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

	req := onesie.Request{
		State: sent, Model: r.model, Questions: r.questions, Assets: assets, LongContext: r.built.LongContext,
	}
	if validErr := onesie.ValidateRequest(req); validErr != nil {
		return badLine(rec, embed("", validErr))
	}

	return requestLine(rec.Line, req)
}

func badLine(rec namedRecord, cause error) ([]byte, error) {
	bad := &input.LineError{Line: rec.Line, Err: cause}

	return errorLine(bad), bad
}

func requestLine(number int, req onesie.Request) ([]byte, error) {
	body, err := onesie.MarshalBody(req)
	if err != nil {
		bad := &input.LineError{Line: number, Err: embed("encoding the request", err)}

		return errorLine(bad), bad
	}

	return body, nil
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
