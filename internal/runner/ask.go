package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Record asks one record and counts the outcome in stats.
func (a Ask) Record(
	ctx context.Context, answerer Answerer, key RecordKey, state any, assets onesie.Assets, stats *Collector,
) (output.Record, error) {
	record, usage, cached, err := a.answered(ctx, answerer, key, state, assets)
	if Halting(err) {
		// No request stood behind it, so it is a record and not a request, and no attempt ended.
		stats.RecordFailure(err, false, 0)

		return record, err
	}

	if err != nil {
		// The request was made whatever went wrong afterwards, and the questions went with it, so a
		// failed record still carries them into the count --stats reports.
		stats.RecordFailure(err, true, len(a.Questions))
		stats.TerminalAttempt(err)
		stats.Spend(usage)

		return record, err
	}

	if cached {
		stats.cachedRecord(record.Model)

		return record, nil
	}

	stats.Record(record.Model, usage, len(a.Questions))

	return record, nil
}

func (a Ask) answered(
	ctx context.Context, answerer Answerer, key RecordKey, state any, assets onesie.Assets,
) (output.Record, onesie.Usage, bool, error) {
	got, err := answerer.Answer(ctx, key, a.Request(state, assets))
	if Halting(err) {
		return a.Failure(err), onesie.Usage{}, false, err
	}

	if err != nil {
		failed := a.Failure(err)

		var unusable *onesie.ResponseError
		if !errors.As(err, &unusable) || unusable.Usage == nil {
			return failed, onesie.Usage{}, false, err
		}

		if a.Usage {
			failed = spent(failed, unusable.Usage)
		}

		return failed, *unusable.Usage, false, err
	}

	result := got.Result
	record := output.Record{Model: result.Model}
	if a.Usage {
		// A copy, since a pointer into the result would keep the whole response alive for as long
		// as the record is held, and a stream holds one per distinct request.
		usage := result.Usage
		record.Usage = &usage
	}

	normalized := answer.NormalizeAll(a.Plan.Questions, result)

	for i, question := range a.Plan.Questions {
		answer.Apply(question, normalized[i])

		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: normalized[i], Box: question.Shape == plan.Box})
	}

	return record, result.Usage, got.Cached, nil
}

// Request is the request that asks state.
func (a Ask) Request(state any, assets onesie.Assets) onesie.Request {
	return onesie.Request{
		State: state, Model: a.Model, Questions: a.Questions, Assets: assets, LongContext: a.Plan.LongContext,
	}
}

func spent(failed output.Record, usage *onesie.Usage) output.Record {
	// A 200 onesie could not use still cost its tokens, so --usage reports them on the error line.
	failed.Usage = usage

	return failed
}

// Failure is the record of a request that failed with cause, every question failed beside it.
func (a Ask) Failure(cause error) output.Record {
	record := output.Record{Failure: Describe(cause, a.Message)}

	for _, question := range a.Plan.Questions {
		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: answer.Failed(question), Box: question.Shape == plan.Box})
	}

	return record
}

// Ask is what every record of a run is asked with. Message words the failure a record carries.
type Ask struct {
	Plan      *plan.Plan
	Model     string
	Questions onesie.Questions
	Usage     bool
	Message   func(error) string
}

// Describe is the failure a record carries for cause, worded by message.
func Describe(cause error, message func(error) string) *output.Failure {
	kind, status := Kind(cause)

	return &output.Failure{Kind: kind, Status: status, Message: message(cause)}
}

// Kind is the kind, and the status when one arrived, of a record that failed with err. Both go into
// the answers file, and a resume reads them back.
func Kind(err error) (output.Kind, *int) {
	var bad *input.LineError

	switch {
	case errors.As(err, &bad), errors.Is(err, context.Canceled):
		return output.KindInput, nil
	case errors.Is(err, context.DeadlineExceeded):
		// A deadline reaching here unwrapped is still a timeout, and a timeout is transport shaped.
		return output.KindTransport, nil
	default:
		return requestKind(err)
	}
}

func requestKind(err error) (output.Kind, *int) {
	// Order matters. RetryAfterError embeds APIError, so the typed check precedes the sentinels it
	// would otherwise match.
	var overCap *onesie.RetryAfterError
	if errors.As(err, &overCap) {
		status := overCap.Status

		return output.KindHTTP, &status
	}

	// A 2xx body onesie cannot use is a server fault. Its kind is response, since http would name a
	// status worth retrying and transport a connection blip that never happened.
	var unusable *onesie.ResponseError
	if errors.As(err, &unusable) {
		status := unusable.Status

		return output.KindResponse, &status
	}

	var api *onesie.APIError
	if errors.As(err, &api) {
		status := api.Status

		if AuthFailure(err) {
			return output.KindAuth, &status
		}

		return output.KindHTTP, &status
	}

	if errors.Is(err, onesie.ErrConnection) || errors.Is(err, onesie.ErrTimeout) {
		return output.KindTransport, nil
	}

	// No status arrived and the network did not fail, so the record never became a request, as with
	// a line onesie could not read. Calling it transport would blame the network.
	return output.KindInput, nil
}

// Judge evaluates the gate on a record that answered, and the abstain expression when the gate
// does not hold.
func Judge(gate, abstain *assert.Expr, record output.Record, stats *Collector) output.Record {
	if assert.Eval(gate, record) {
		return record
	}

	// Eval holds for a nil expression, which is right for a missing gate and wrong here, where a
	// missing abstain expression leaves every false assertion a no.
	if abstain != nil && assert.Eval(abstain, record) {
		record.Abstained = true
		stats.abstained()

		return record
	}

	record.AssertFailed = true
	stats.assertFailed()

	return record
}

// Mapped is the state mapper sends, or wire when there is no mapper.
func Mapped(ctx context.Context, mapper *jq.Expr, state, wire any, speller plan.Speller) (any, error) {
	if mapper == nil {
		return wire, nil
	}

	flag := speller.Spell("--map")

	value, err := mapper.One(ctx, JQValue(state, wire))
	if err != nil {
		return nil, fmt.Errorf("%s %w", flag, err)
	}

	if checkErr := input.CheckState(value); checkErr != nil {
		return nil, fmt.Errorf("%s: %w", flag, checkErr)
	}

	encoded, err := jq.Marshal(value, limits.MaxLineBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flag, err)
	}

	return json.RawMessage(encoded), nil
}

// JQValue is the value a jq expression reads for a record, with the numbers of a JSON line kept as
// written.
func JQValue(state, wire any) any {
	raw, isJSON := wire.(json.RawMessage)
	if !isJSON {
		return state
	}

	// Decoded again with numbers kept as written, since the parsed state holds float64 and a
	// large id would lose digits on its way through the expression.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return state
	}

	return value
}

// HasKey reports whether state is an object holding key.
func HasKey(state any, key string) bool {
	object, ok := state.(map[string]any)
	if !ok {
		return false
	}

	_, exists := object[key]

	return exists
}
