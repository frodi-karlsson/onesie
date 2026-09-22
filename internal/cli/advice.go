package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func advise(err error, model string) error {
	var api *jev.APIError
	if !errors.As(err, &api) {
		return err
	}

	if unknownModel(api, model) {
		return &advisedError{
			cause:   err,
			message: fmt.Sprintf("jev: model '%s' not found. Try --list-models", model),
		}
	}

	if countRejected(api) {
		return &advisedError{
			cause: err,
			message: err.Error() +
				". jev's own check passed, so its built in limits may be stale. Run jev -V",
		}
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
	// The id comes from what jev sent rather than from a substring of the server's prose, whose
	// wording the server owns. A call carrying no model has nothing to name, so it keeps the
	// server's text.
	if model == "" || api.Status != http.StatusBadRequest {
		return false
	}

	return strings.Contains(api.Error(), "Unknown model")
}

func countRejected(api *jev.APIError) bool {
	if api.Status != http.StatusUnprocessableEntity {
		return false
	}

	// A heuristic. The server owns the wording, so jev cannot know every phrasing, and it matches
	// the flattened "path: msg" form instead: an option or level count names criteria as the last
	// path segment, or as the parent of an index. It is acceptable here because the advice is
	// additive. A phrasing this misses leaves the server's text exactly as it stands, and a 422
	// naming criteria for another reason still points at the limits that built the request.
	message := api.Error()

	return strings.Contains(message, "criteria:") || strings.Contains(message, "criteria.")
}
