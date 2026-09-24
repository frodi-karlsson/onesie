package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func printQuestions(
	out, errOut io.Writer,
	questions []plan.Question,
	assertion, abstainIf string,
	loaded *qfile.File,
) error {
	if err := warnUncarried(errOut, loaded); err != nil {
		return err
	}

	file, err := qfile.Write(questions, assertion, abstainIf)
	if err != nil {
		return err
	}

	_, err = out.Write(file)

	return written(err)
}

func warnUncarried(w io.Writer, loaded *qfile.File) error {
	// A question file has no model key and no state key, so only a request body loses anything on
	// the way into one.
	if loaded == nil || !loaded.IsBody {
		return nil
	}

	if loaded.Model != "" {
		_, err := fmt.Fprintf(w,
			"warning: --print-questions does not carry the body's model '%s'. "+
				"Pass -m when you reload\n", loaded.Model)
		if err != nil {
			return err
		}
	}

	if !loaded.HasState {
		return nil
	}

	_, err := fmt.Fprintln(w,
		"warning: --print-questions does not carry the body's state. "+
			"Pass --state when you reload")

	return err
}

func printRequest(
	w io.Writer,
	questions []plan.Question,
	source input.Source,
	state any,
	model string,
) error {
	req := jev.Request{Model: model, Questions: wireAll(questions)}

	encode := jev.MarshalQuestionsBody
	if source != input.SourceNone {
		req.State = state
		encode = jev.MarshalBody
	}

	body, err := encode(req)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(w, string(body))

	return written(err)
}

func streamRequests(
	cmd *cobra.Command,
	settings rootSettings,
	built *plan.Plan,
	mapper *jq.Expr,
	inputMode input.Mode,
	flags *runFlags,
) error {
	questions := wireAll(built.Questions)

	model, err := resolveModel(settings, flags, built.Model)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	result, err := engine.Run(cmd.Context(), engine.Config[input.Record, []byte]{
		Source: engine.Skip[input.Record](input.NewStream(settings.stdin, inputMode, flags.skipBlank), flags.resumeSkip),
		Evaluate: func(ctx context.Context, rec input.Record) ([]byte, error) {
			if rec.Err != nil {
				// A value alongside the error, because the engine writes every outcome. Returning
				// nil here would print a blank line rather than the record.
				return errorLine(rec.Err), rec.Err
			}

			sent, mapErr := mapped(ctx, mapper, rec.State, rec.Wire)
			if interrupted(ctx) {
				return nil, ctx.Err()
			}

			if mapErr != nil {
				bad := &input.LineError{Line: rec.Line, Err: mapErr}

				return errorLine(bad), bad
			}

			return requestLine(rec.Line, jev.Request{State: sent, Model: model, Questions: questions})
		},
		Write: func(body []byte) error {
			_, writeErr := fmt.Fprintln(out, string(body))

			return writeErr
		},
		Jobs:        flags.jobs,
		Unordered:   flags.unordered,
		StopOnError: flags.stopOnError,
		Abort:       aborting,
	})
	if err != nil {
		return &sourceError{cause: err, failed: result.Failed}
	}

	// A dry run asks no question, so no record can have carried an assertion.
	return streamResult(result, 0, 0)
}

func requestLine(number int, req jev.Request) ([]byte, error) {
	body, err := jev.MarshalBody(req)
	if err != nil {
		bad := &input.LineError{Line: number, Err: fmt.Errorf("encoding the request: %w", err)}

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
