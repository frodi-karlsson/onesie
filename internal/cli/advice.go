package cli

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

func explain(err error, facts requestFacts) error {
	helps := longContextHelps(facts.provider, facts.model, facts.longContext)

	boxed := adviseBox(adviseLongContext(err, helps), facts.boxed, facts.provider.BoxModel())

	return advise(adviseLimits(boxed), facts.model)
}

type requestFacts struct {
	provider    onesie.Provider
	model       string
	longContext bool
	boxed       bool
}

func advise(err error, model string) error {
	var api *onesie.APIError
	if !errors.As(err, &api) {
		return err
	}

	if api.Status == http.StatusPaymentRequired {
		return &advisedError{cause: err, note: "Add credits to the account this key belongs to"}
	}

	if model != "" && errors.Is(err, onesie.ErrUnknownModel) {
		return &advisedError{cause: err, message: fmt.Sprintf("model '%s' not found", model), note: "Try --list-models"}
	}

	var advised *advisedError
	if errors.As(err, &advised) {
		return err
	}

	// Only a missing path or method points at the base URL. An outage, a rate limit or a refused key
	// arrives from the right server too.
	if api.Body == nil && len(api.RawBody) > 0 &&
		(api.Status == http.StatusNotFound || api.Status == http.StatusMethodNotAllowed) {
		return &advisedError{cause: err, note: "Check that the base URL points at the System One API"}
	}

	return err
}

func adviseLongContext(err error, helps bool) error {
	if !helps || !errors.Is(err, onesie.ErrContextTooLong) {
		return err
	}

	return &advisedError{
		cause: err,
		note:  "Pass --long-context to raise the context limit, which is slower and less accurate",
	}
}

func longContextHelps(provider onesie.Provider, model string, on bool) bool {
	return !on && provider.Refuse(model, onesie.Needs{LongContext: true}) == nil
}

func adviseBox(err error, boxed bool, boxModel string) error {
	// Only the server's refusal, since a local one already names the providers that take a box.
	var api *onesie.APIError
	if !boxed || !errors.As(err, &api) || !errors.Is(err, onesie.ErrNotServed) {
		return err
	}

	note := "A box question needs a model that serves it"
	if boxModel != "" {
		note += ", such as -m " + boxModel
	}

	return &advisedError{cause: err, note: note}
}

func withKeyHint(err error, settings rootSettings, flags runConfig) error {
	// A mock run sends no key, so naming where one came from would send the reader to the wrong place.
	if !keyRefused(err) || flags.mockPath != "" {
		return err
	}

	chosen, resolveErr := connected(settings, flags)
	if resolveErr != nil {
		return err
	}

	return keyHint(err, chosen)
}

func keyHint(err error, chosen resolution) error {
	if !keyRefused(err) {
		return err
	}

	return &advisedError{cause: err, note: keyNote(chosen)}
}

func keyNote(chosen resolution) string {
	if chosen.keyAside {
		pass := "--base-url URL"
		if chosen.by == byDefault {
			pass = "--profile " + creds.QuotedName(chosen.profile) + " --base-url URL"
		}

		return "No key was sent, since the profile's key goes only with its own endpoint, and the base URL came " +
			"from " + chosen.endpointFrom + ". Put the URL in the profile's base_url with onesie profile update " +
			creds.QuotedName(chosen.profile) + " --base-url URL, or pass " + pass
	}

	// New accepts a missing key only on a custom base URL, so a refusal with none was sent there.
	if chosen.keyFrom == sourceNone && chosen.profile != "" {
		return "No key was sent, since none resolves and the base URL is custom. Store one with onesie profile " +
			"update " + creds.QuotedName(chosen.profile) + " --key"
	}

	if chosen.keyFrom == sourceNone {
		return "No key was sent, since none resolves and the base URL is custom. Set " + chosen.provider.EnvAPIKey()
	}

	if chosen.profile != "" && chosen.keyFrom != sourceEnv {
		return "Replace the key with onesie profile update " + creds.QuotedName(chosen.profile) + " --key"
	}

	return "The key came from " + chosen.provider.EnvAPIKey()
}

func keyRefused(err error) bool {
	return errors.Is(err, onesie.ErrAuthentication) || errors.Is(err, onesie.ErrPermissionDenied)
}

type advisedError struct {
	cause   error
	message string
	note    string
}

func (e *advisedError) Error() string {
	message := e.message
	if message == "" {
		message = text(e.cause)
	}

	if e.note == "" {
		return message
	}

	return withNote(message, e.note)
}

func (e *advisedError) Unwrap() error {
	return e.cause
}

func adviseLimits(err error) error {
	// Only the server's refusal. A local one means the check before sending refused, not passed.
	var api *onesie.APIError
	if !errors.As(err, &api) || !errors.Is(err, onesie.ErrQuestionLimit) {
		return err
	}

	return &advisedError{
		cause: err,
		note:  "onesie's own check passed, so its built in limits may be stale. Run onesie version",
	}
}

func withNote(message, note string) string {
	// The server may end its own sentence, and a second full stop right before the note reads as a
	// typo rather than as a boundary.
	if strings.HasSuffix(message, ".") {
		return message + " " + note
	}

	return message + ". " + note
}
