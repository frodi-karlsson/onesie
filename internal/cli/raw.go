package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/jev-cli/internal/engine"
	"github.com/frodi-karlsson/jev-cli/internal/input"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func streamRaw(cmd *cobra.Command, settings rootSettings, flags *runFlags) error {
	var client *jev.Client

	// Built only when the run makes a request, so -i request --print-request needs no key.
	if requests(flags) {
		built, err := settings.newClient(cmd.Context())
		if err != nil {
			return err
		}

		client = built
	}

	out := cmd.OutOrStdout()

	result, err := engine.Run(cmd.Context(), engine.Config[[]byte]{
		Source: input.NewStream(settings.stdin, input.Request, flags.skipBlank),
		Evaluate: func(ctx context.Context, rec input.Record) ([]byte, error) {
			if rec.Err != nil {
				// A value alongside the error, because the engine writes every outcome. Returning
				// nil here would print a blank line rather than the record.
				return errorLine(rec.Err), rec.Err
			}

			// Section 10 makes --print-request the identity here: bodies pass through unchanged,
			// exit 0, no network. That is what lets it be dropped into any pipeline as a dry run
			// switch without the pipeline changing shape. The same predicate decides this and the
			// client above, so the two cannot disagree and leave a nil client to dereference.
			if !requests(flags) {
				return []byte(rec.Raw), nil
			}

			body, err := client.SystemOneRaw(ctx, json.RawMessage(rec.Raw))
			if err != nil {
				return errorLine(err), err
			}

			return body, nil
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

	return streamResult(result)
}
