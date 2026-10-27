package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func streamRaw(
	cmd *cobra.Command,
	settings rootSettings,
	flags *runFlags,
	stats *collector,
	answers *outFile,
	resume resumePlan,
) error {
	// Resolved for --print-request too, which prints the model a body that names none is sent with.
	chosen, resolveErr := runResolution(settings)
	if resolveErr != nil {
		return resolveErr
	}

	provider := chosen.provider
	defaultModel := chosen.modelFor("", settings.lookupEnv)

	var client *onesie.Client

	// Built only when the run makes a request, so -i request --print-request needs no key.
	if requests(flags) {
		built, err := settings.newClient(cmd.Context(), observing(stats)...)
		if err != nil {
			return err
		}

		client = built
	}

	long := flags.env.longContext

	// Every body would be refused, so the run stops before it reads any.
	if long && requests(flags) {
		if refusal := refuseNeeds(provider, defaultModel, onesie.Needs{LongContext: true}); refusal != nil {
			return longContextCause(refusal, settings, flags)
		}
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

			body := withModel([]byte(rec.Raw), defaultModel)

			// --print-request prints the body as it would be sent, with exit 0 and no network. The
			// same predicate decides this and the client above, so the two cannot disagree and leave
			// a nil client.
			if !requests(flags) {
				if long {
					return withLongContext(body), nil
				}

				return body, nil
			}

			// From the request rather than the response. A failed record has no answers but did
			// carry questions, and counting the response would report zero for exactly the records
			// a user turned --stats on to understand.
			read := readRawRequest(body)
			asked := read.questions

			var opts []onesie.RequestOption
			if read.needsImages {
				stats.imaged()

				if !flags.timeoutSet {
					opts = append(opts, onesie.WithRequestAttemptTimeout(imageAttemptTimeout))
				}
			}

			if long {
				opts = append(opts, onesie.WithLongContext())
			}

			response, err := client.SystemOneRaw(ctx, json.RawMessage(body), opts...)
			// The client refuses what the route cannot take in its own words, which name too few of
			// the providers that can.
			if errors.Is(err, onesie.ErrValidation) {
				if refusal := refuseNeeds(provider, read.model, onesie.Needs{
					Images: read.needsImages, Box: read.boxed, LongContext: long || read.longContext,
				}); refusal != nil {
					err = refusal
				}
			}

			if err != nil {
				// The body's model reached the wire unchanged, so the model it named is the one onesie sent,
				// and nothing inside it was checked locally, so no local bound is implicated.
				advised := advise(adviseBox(adviseLongContext(err,
					longContextHelps(provider, read.model, long || read.longContext)), provider.Name, read.boxed), read.model, false)

				stats.recordFailure(advised, true, asked)
				stats.terminalAttempt(advised)

				return errorLine(advised), advised
			}

			if usage, checkErr := checkRawResponse(body, response); checkErr != nil {
				stats.spend(usage)
				stats.recordFailure(checkErr, true, asked)

				return errorLine(checkErr), checkErr
			}

			model, usage := rawSummary(response)
			stats.record(model, usage, asked)

			return oneLine(response), nil
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

func withModel(body []byte, model string) []byte {
	object := bytes.TrimSpace(body)
	if model == "" || !bytes.HasPrefix(object, []byte("{")) || !json.Valid(object) {
		return body
	}

	name, err := json.Marshal(model)
	if err != nil {
		return body
	}

	// The body is otherwise sent as written, so the model goes in place or in front rather than
	// through a decode and encode that would reorder the keys and reformat the numbers. Every empty
	// model key is filled, since a server may read the first or the last of them.
	spans := keyValues(object, "model")
	if len(spans) == 0 {
		filled := append([]byte(`{"model":`), name...)
		if rest := bytes.TrimSpace(object[1:]); bytes.HasPrefix(rest, []byte("}")) {
			return append(filled, rest...)
		}

		return slices.Concat(filled, []byte(","), object[1:])
	}

	filled, at := []byte{}, 0

	for _, span := range spans {
		if !blankModel(object[span[0]:span[1]]) {
			continue
		}

		filled = slices.Concat(filled, object[at:span[0]], name)
		at = span[1]
	}

	if at == 0 {
		return body
	}

	return append(filled, object[at:]...)
}

func withLongContext(body []byte) []byte {
	// A printed body carries long context as -f prints it, so a replay keeps what this run sends,
	// and a body's own false does not turn off the flag or the variable.
	object := bytes.TrimSpace(body)
	if !bytes.HasPrefix(object, []byte("{")) || !json.Valid(object) {
		return body
	}

	spans := keyValues(object, "long_context")
	if len(spans) == 0 {
		inner := bytes.TrimSpace(object[1 : len(object)-1])
		if len(inner) == 0 {
			return []byte(`{"long_context":true}`)
		}

		return slices.Concat(object[:len(object)-1], []byte(`,"long_context":true}`))
	}

	filled, at := []byte{}, 0

	for _, span := range spans {
		filled = slices.Concat(filled, object[at:span[0]], []byte("true"))
		at = span[1]
	}

	return append(filled, object[at:]...)
}

func keyValues(object []byte, name string) [][2]int {
	// A scan of a body json.Valid passed, rather than a decode, so the assets a body carries are
	// walked past and never copied. Only the exact key counts, since encoding/json would match
	// Model for model too and the API reads only the exact one.
	var spans [][2]int

	for i := 1; i < len(object); {
		i = skipSpace(object, i)
		if object[i] == ',' {
			i++

			continue
		}

		if object[i] == '}' {
			break
		}

		keyEnd := stringEnd(object, i)
		key := object[i:keyEnd]
		valueStart := skipSpace(object, skipSpace(object, keyEnd)+1)
		valueEnd := jsonValueEnd(object, valueStart)

		if isKey(key, name) {
			spans = append(spans, [2]int{valueStart, valueEnd})
		}

		i = valueEnd
	}

	return spans
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}

	return i
}

func stringEnd(b []byte, i int) int {
	for i++; b[i] != '"'; i++ {
		if b[i] == '\\' {
			i++
		}
	}

	return i + 1
}

func jsonValueEnd(b []byte, i int) int {
	switch b[i] {
	case '"':
		return stringEnd(b, i)
	case '{', '[':
		depth := 0

		for {
			switch b[i] {
			case '"':
				i = stringEnd(b, i)

				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}

			i++
		}
	default:
		for i < len(b) && !bytes.ContainsRune([]byte(",}] \t\r\n"), rune(b[i])) {
			i++
		}

		return i
	}
}

func isKey(key []byte, name string) bool {
	if !bytes.Contains(key, []byte(`\`)) {
		return string(key) == `"`+name+`"`
	}

	var decoded string

	return json.Unmarshal(key, &decoded) == nil && decoded == name
}

func blankModel(value []byte) bool {
	if string(value) == "null" {
		return true
	}

	// A model name is short, so a long value is no blank one and is not decoded.
	var name string

	return len(value) <= 256 && json.Unmarshal(value, &name) == nil && strings.TrimSpace(name) == ""
}

func checkRawResponse(request, response []byte) (onesie.Usage, error) {
	if !json.Valid(response) {
		return onesie.Usage{}, unusableResponse("the response is not JSON")
	}

	var probe struct {
		Answers json.RawMessage `json:"answers"`
		Usage   onesie.Usage    `json:"usage"`
	}

	if err := json.Unmarshal(response, &probe); err != nil {
		return onesie.Usage{}, readableResponse(&onesie.ResponseError{Status: http.StatusOK, Err: err})
	}

	if !bytes.HasPrefix(bytes.TrimSpace(probe.Answers), []byte("{")) {
		return probe.Usage, unusableResponse("the response is not a System One result, since it has no answers")
	}

	var result onesie.Result
	if err := json.Unmarshal(response, &result); err != nil {
		return probe.Usage, readableResponse(&onesie.ResponseError{Status: http.StatusOK, Err: err})
	}

	// Only a body whose questions onesie can read, as -f reads one, says what each answer must fit.
	for _, question := range requestQuestions(request) {
		if _, err := answer.Normalize(question, result.Answers[question.ID]); err != nil {
			return probe.Usage, err
		}
	}

	return probe.Usage, nil
}

func unusableResponse(reason string) error {
	return &onesie.ResponseError{
		Status: http.StatusOK, Message: "onesie: 200 response could not be used: " + reason,
	}
}

func requestQuestions(request []byte) []plan.Question {
	var probe struct {
		Questions json.RawMessage `json:"questions"`
	}

	if json.Unmarshal(request, &probe) != nil || probe.Questions == nil {
		return nil
	}

	loaded, err := qfile.Load(request)
	if err != nil {
		return nil
	}

	return loaded.Questions
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

func rawSummary(response []byte) (string, onesie.Usage) {
	var probe struct {
		Model string       `json:"model"`
		Usage onesie.Usage `json:"usage"`
	}

	// A body onesie cannot read still counts as a request. Only the model and the token numbers are
	// lost, and reporting nothing for them beats failing a record the server answered.
	if err := json.Unmarshal(response, &probe); err != nil {
		return "", onesie.Usage{}
	}

	return probe.Model, probe.Usage
}

func readRawRequest(request []byte) rawRequest {
	// Empty structs for the assets, so the base64 of every asset is scanned but never copied.
	var probe struct {
		Model       string                     `json:"model"`
		Assets      map[string]struct{}        `json:"assets"`
		Questions   map[string]json.RawMessage `json:"questions"`
		LongContext bool                       `json:"long_context"`
	}

	// A type error still fills every field it did not reach, so a body with one odd field keeps
	// its model and question count, as the separate probes this replaces did.
	var typeErr *json.UnmarshalTypeError
	if err := json.Unmarshal(request, &probe); err != nil && !errors.As(err, &typeErr) {
		return rawRequest{}
	}

	read := rawRequest{
		model: probe.Model, questions: len(probe.Questions), needsImages: len(probe.Assets) > 0,
		longContext: probe.LongContext,
	}

	for _, raw := range probe.Questions {
		var question struct {
			Type string `json:"type"`
		}

		if json.Unmarshal(raw, &question) == nil && question.Type == "box" {
			read.needsImages = true
			read.boxed = true
		}
	}

	return read
}

type rawRequest struct {
	model       string
	questions   int
	needsImages bool
	boxed       bool
	longContext bool
}
