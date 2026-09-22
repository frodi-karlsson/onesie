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
