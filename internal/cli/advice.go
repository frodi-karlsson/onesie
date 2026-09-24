package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

func advise(err error, model string, validated bool) error {
	var api *jev.APIError
	if !errors.As(err, &api) {
		return err
	}

	if api.Status == http.StatusPaymentRequired {
		return &advisedError{
			cause:   err,
			message: err.Error() + ". Add credits to the account this key belongs to",
		}
	}

	if unknownModel(api, model) {
		return &advisedError{
			cause:   err,
			message: fmt.Sprintf("onesie: model '%s' not found. Try --list-models", model),
		}
	}

	// Only where onesie ran its own bounds check. A body replayed through -i request is sent
	// unchecked, so a note about stale limits would blame a check that never ran.
	if validated && countRejected(api) {
		return &advisedError{cause: err, message: staleLimits(err.Error())}
	}

	return err
}

type advisedError struct {
	cause   error
	message string
}

func (e *advisedError) Error() string {
	return e.message
}

func (e *advisedError) Unwrap() error {
	return e.cause
}

func unknownModel(api *jev.APIError, model string) bool {
	// The id comes from what onesie sent rather than from a substring of the server's prose, whose
	// wording the server owns. A call carrying no model has nothing to name, so it keeps the
	// server's text.
	if model == "" || api.Status != http.StatusBadRequest {
		return false
	}

	// TypeSafe says Unknown model, and OpenRouter says the model does not exist.
	return strings.Contains(api.Error(), "Unknown model") || strings.Contains(api.Error(), "does not exist")
}

func countRejected(api *jev.APIError) bool {
	// Every content rejection seen live was a 400. A 422 is here because the API reference
	// documents one, not because one was observed.
	if api.Status != http.StatusBadRequest && api.Status != http.StatusUnprocessableEntity {
		return false
	}

	// A heuristic, since the server owns the wording. The one count rejection seen live reads "Too
	// many score levels. Must have at most 10 levels.", so the match pairs a word about a bound
	// with the thing counted rather than pinning that sentence. A phrasing this misses costs only
	// the note.
	message := strings.ToLower(api.Error())

	return boundWord(message) && countedSubject(message)
}

func boundWord(message string) bool {
	for _, phrase := range []string{"too many", "too few", "at most", "at least"} {
		if strings.Contains(message, phrase) {
			return true
		}
	}

	return false
}

func countedSubject(message string) bool {
	return strings.Contains(message, "level") || strings.Contains(message, "option")
}

func staleLimits(message string) string {
	const note = "onesie's own check passed, so its built in limits may be stale. Run onesie -V"

	// The server ends its own sentence, and a second full stop right before the note reads as a
	// typo rather than as a boundary.
	if strings.HasSuffix(message, ".") {
		return message + " " + note
	}

	return message + ". " + note
}
