package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jev"
)

func streamRaw(
	cmd *cobra.Command,
	settings rootSettings,
	flags *runFlags,
	stats *collector,
	answers *outFile,
	resume resumePlan,
) error {
	var client *jev.Client

	// Built only when the run makes a request, so -i request --print-request needs no key.
	if requests(flags) {
		built, err := settings.newClient(cmd.Context(), observing(stats)...)
		if err != nil {
			return err
		}

		client = built
	}

	resume, readErr := resumedVerdicts(cmd.Context(), answers, nil, answersFormat{forwarded: true}, flags, resume)
	if readErr != nil {
		return readErr
	}

	source := &resumed{
		source: input.NewStream(settings.stdin, input.Request, flags.skipBlank),
		left:   resume.skip,
		stored: resume.stored,
		// --stop-on-assert is refused under -i request, whose bodies carry no assertion.
		haltOnError: flags.stopOnError,
	}
	out := cmd.OutOrStdout()

	result, err := engine.Run(cmd.Context(), engine.Config[input.Record, []byte]{
		Source: source,
		Evaluate: func(ctx context.Context, rec input.Record) ([]byte, error) {
			if rec.Err != nil {
				// A value alongside the error, because the engine writes every outcome. Returning
				// nil here would print a blank line rather than the record.
				stats.recordFailure(rec.Err, false, 0)

				return errorLine(rec.Err), rec.Err
			}

			// --print-request is the identity here: bodies pass through unchanged, with exit 0 and
			// no network. The same predicate decides this and the client above, so the two cannot
			// disagree and leave a nil client.
			if !requests(flags) {
				return []byte(rec.Raw), nil
			}

			// From the request rather than the response. A failed record has no answers but did
			// carry questions, and counting the response would report zero for exactly the records
			// a user turned --stats on to understand.
			asked := rawQuestions([]byte(rec.Raw))

			body, err := client.SystemOneRaw(ctx, json.RawMessage(rec.Raw))
			if err != nil {
				// The body reached the wire unchanged, so the model it named is the one onesie sent,
				// and nothing inside it was checked locally, so no local bound is implicated.
				advised := advise(err, rawModel([]byte(rec.Raw)), false)

				stats.recordFailure(advised, true, asked)
				stats.terminalAttempt(advised)

				return errorLine(advised), advised
			}

			model, usage := rawSummary(body)
			stats.record(model, usage, asked)

			return oneLine(body), nil
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

	skips := source.skipped()
	stats.skip(skips)

	// A stored failure a resume skipped fails the run as it failed the one that wrote it.
	result.Failed += skips.failed

	if freshAbort := source.freshRunAbort(); freshAbort != nil {
		return freshAbort
	}

	if err != nil {
		return &sourceError{cause: err, failed: result.Failed}
	}

	// A request body carries no assertion, so no record can have failed one.
	return streamResult(result, 0, 0)
}

func oneLine(body []byte) []byte {
	// A server that newline terminates or pretty prints its JSON would otherwise turn one record
	// into several output lines, where this mode promises one line per record. Every other
	// streaming path encodes its own record and never faces this.
	var flat bytes.Buffer

	if err := json.Compact(&flat, body); err == nil {
		return flat.Bytes()
	}

	// Not JSON, so there is nothing to compact. The newlines still have to go.
	return bytes.ReplaceAll(bytes.TrimRight(body, "\n"), []byte("\n"), []byte(" "))
}

func rawSummary(response []byte) (string, jev.Usage) {
	var probe struct {
		Model string    `json:"model"`
		Usage jev.Usage `json:"usage"`
	}

	// A body onesie cannot read still counts as a request. Only the model and the token numbers are
	// lost, and reporting nothing for them beats failing a record the server answered.
	if err := json.Unmarshal(response, &probe); err != nil {
		return "", jev.Usage{}
	}

	return probe.Model, probe.Usage
}

func rawModel(request []byte) string {
	var probe struct {
		Model string `json:"model"`
	}

	if err := json.Unmarshal(request, &probe); err != nil {
		return ""
	}

	return probe.Model
}

func rawQuestions(request []byte) int {
	var probe struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}

	if err := json.Unmarshal(request, &probe); err != nil {
		return 0
	}

	return len(probe.Questions)
}
