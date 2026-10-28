package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
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

	return r.send(ctx, body.Bytes())
}

func (r *rawRun) send(ctx context.Context, body json.RawMessage) ([]byte, error) {
	// From the request rather than the response. A failed record has no answers but did
	// carry questions, and counting the response would report zero for exactly the records
	// a user turned --stats on to understand.
	read := readRawRequest(body)

	response, err := r.client.SystemOneRaw(ctx, body)
	if err != nil {
		// The prepared body names the model onesie sent.
		advised := explain(err, requestFacts{
			provider: r.provider, model: read.model, longContext: read.longContext, boxed: read.boxed,
		})

		r.stats.recordFailure(advised, true, read.questions)
		r.stats.terminalAttempt(advised)

		return errorLine(advised), advised
	}

	result, checkErr := checkRawResponse(read.asked, response)
	if checkErr != nil {
		r.stats.spend(result.Usage)
		r.stats.recordFailure(checkErr, true, read.questions)

		return errorLine(checkErr), checkErr
	}

	r.stats.record(result.Model, result.Usage, read.questions)

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

func checkRawResponse(asked []plan.Question, response []byte) (onesie.Result, error) {
	// One decode reads the model, the usage and the answers. A 200 whose answers onesie cannot read
	// leaves the usage unset, so the usage is read again alone to report what the request cost.
	var result onesie.Result

	err := json.Unmarshal(response, &result)

	var syntax *json.SyntaxError

	switch {
	case errors.As(err, &syntax):
		return result, answer.Unusable("the response is not JSON")
	case err != nil:
		result.Usage = billedUsage(response)

		return result, &onesie.ResponseError{Status: http.StatusOK, Err: err}
	case len(result.Answers) == 0 && !holdsAnswers(response):
		return result, answer.Unusable("the response is not a System One result, since it has no answers")
	}

	return result, answer.CheckAll(asked, &result)
}

func billedUsage(response []byte) onesie.Usage {
	var billed struct {
		Usage onesie.Usage `json:"usage"`
	}

	if json.Unmarshal(response, &billed) != nil {
		return onesie.Usage{}
	}

	return billed.Usage
}

func holdsAnswers(response []byte) bool {
	// Result reads a missing answers object as an empty one, which is also the answer to a body
	// that asks nothing, so only an empty result is read again to tell the two apart.
	var members map[string]json.RawMessage

	return json.Unmarshal(response, &members) == nil &&
		bytes.HasPrefix(bytes.TrimSpace(members["answers"]), []byte("{"))
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

func readRawRequest(prepared []byte) rawRequest {
	// The prepared body holds one member per key, so a decode into members reads exactly what is
	// sent, by the exact key.
	var members map[string]borrowed
	if json.Unmarshal(prepared, &members) != nil {
		return rawRequest{}
	}

	read := rawRequest{model: member[string](members, "model"), longContext: member[bool](members, "long_context")}

	questions, found := members["questions"]
	if !found {
		return read
	}

	if asked := requestQuestions(questions); asked != nil {
		read.asked, read.questions = asked, len(asked)
		read.boxed = slices.ContainsFunc(asked, func(q plan.Question) bool { return q.Shape == plan.Box })

		return read
	}

	// The questions do not load, so they are counted and their types read one by one.
	var each map[string]borrowed
	if json.Unmarshal(questions, &each) != nil {
		return read
	}

	read.questions = len(each)

	for _, raw := range each {
		var question map[string]borrowed
		if json.Unmarshal(raw, &question) == nil && member[string](question, "type") == "box" {
			read.boxed = true
		}
	}

	return read
}

type rawRequest struct {
	model       string
	questions   int
	boxed       bool
	longContext bool
	asked       []plan.Question
}

func member[T any](members map[string]borrowed, name string) T {
	var value T

	raw, found := members[name]
	if !found || json.Unmarshal(raw, &value) != nil {
		var zero T

		return zero
	}

	return value
}

func requestQuestions(questions borrowed) []plan.Question {
	// qfile.Load reads a mapping with no questions key as a question file, so the member goes back
	// under its key to be read as a body's questions.
	body := make([]byte, 0, len(questions)+len(`{"questions":}`))
	body = append(append(append(body, `{"questions":`...), questions...), '}')

	loaded, err := qfile.Load(body)
	if err != nil {
		return nil
	}

	return loaded.Questions
}

type borrowed []byte

func (b *borrowed) UnmarshalJSON(data []byte) error {
	// json.Unmarshal hands each member a slice of its own input, so keeping it copies nothing. The
	// input is a body this package holds and never writes, so the slice stays valid while it is read.
	*b = data

	return nil
}
