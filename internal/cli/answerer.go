package cli

import (
	"context"
	"errors"

	"github.com/frodi-karlsson/onesie-go"
)

// AnswererFactory builds the answerer a run asks through, once the run's statistics exist.
type AnswererFactory func(ctx context.Context, stats *collector) (Answerer, error)

// Answerer answers the request of one record. Salt keys the answer a record may share with an
// identical one, and false means the record shares with none.
type Answerer interface {
	Answer(ctx context.Context, key RecordKey, req onesie.Request) (Reply, error)
	Salt(key RecordKey) (string, bool)
}

// Reply is an answer, and whether the cache gave it.
type Reply struct {
	Result *onesie.Result
	Cached bool
}

// RecordKey locates a record by its position, its input line and its id.
type RecordKey struct {
	Position int
	Line     int
	ID       any
}

// NewLiveAnswerer asks the API through client, and passes each error with its request through
// explain, which returns nil for a nil error.
func NewLiveAnswerer(client *onesie.Client, explain func(error, onesie.Request) error) LiveAnswerer {
	return LiveAnswerer{client: client, explain: explain}
}

// Answer asks the API.
func (a LiveAnswerer) Answer(ctx context.Context, _ RecordKey, req onesie.Request) (Reply, error) {
	result, err := a.client.SystemOne(ctx, req)

	return Reply{Result: result}, a.explain(err, req)
}

// Salt lets every record share its answer with an identical one.
func (LiveAnswerer) Salt(RecordKey) (string, bool) {
	return "", true
}

// LiveAnswerer asks the API for every record.
type LiveAnswerer struct {
	client  *onesie.Client
	explain func(error, onesie.Request) error
}

// Halting reports whether err ends the run before any further request.
func Halting(err error) bool {
	var halt *HaltError

	return errors.As(err, &halt)
}

// Error returns the text of the cause.
func (e *HaltError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the cause.
func (e *HaltError) Unwrap() error {
	return e.Err
}

// HaltError ends a run before any further request, rather than failing one record after another,
// for a record the mock file does not answer or a cache the run cannot use.
type HaltError struct {
	Err error
}
