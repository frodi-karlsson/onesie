package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

func calibrateRequests(
	cmd *cobra.Command, settings rootSettings, flags runConfig, inputMode input.Mode, inv *invocation,
	scope calibrate.Scope,
) error {
	chosen, err := settings.holder.resolved()
	if err != nil {
		return err
	}

	set, err := calibrate.ReadLabelled(cmd.Context(), calibrateConfig(settings, flags, inputMode, inv), scope.Labels)
	if err != nil {
		return firstIDHint(err, inputMode)
	}

	ask := runner.Ask{
		Plan:      scope.Scored,
		Model:     chosen.ModelFor(scope.Scored.Model, settings.lookupEnv),
		Questions: plan.WireAll(scope.Scored.Questions),
	}

	for _, rec := range set.Records {
		if ctxErr := cmd.Context().Err(); ctxErr != nil {
			return ctxErr
		}

		sent, assets, imageErr := inv.images.Attach(rec.Sent())
		if imageErr != nil {
			return &input.LineError{Line: rec.Line(), Err: imageErr}
		}

		req := ask.Request(sent, assets)
		if checkErr := chosen.Provider.Check(req); checkErr != nil {
			return &input.LineError{Line: rec.Line(), Err: embed("", checkErr)}
		}

		body, bodyErr := requestLine(rec.Line(), req)
		if bodyErr != nil {
			return bodyErr
		}

		if _, printErr := fmt.Fprintln(cmd.OutOrStdout(), string(body)); printErr != nil {
			return written(printErr)
		}
	}

	return nil
}

func firstIDHint(err error, inputMode input.Mode) error {
	var unread *calibrate.ReadError
	if !errors.As(err, &unread) {
		return err
	}

	// The cause alone, since text reads a Reason only from the top level error.
	cause := unread.Err

	// calibrate stops at the first record it cannot read, so a null id on the first record is every
	// record read.
	if unread.Read != 1 || !runner.MisreadDelimiter(inputMode, unread.Header) || !errors.Is(cause, jq.ErrNullID) {
		return cause
	}

	return &advisedError{cause: cause, message: text(cause) + ". " + runner.DelimiterHint(inputMode, unread.Comma, unread.Header, plan.Flags)}
}
