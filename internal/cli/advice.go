package cli

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"unicode"

	"github.com/frodi-karlsson/onesie-go"
)

func advise(err error, model string, validated bool) error {
	var api *onesie.APIError
	if !errors.As(err, &api) {
		return err
	}

	if api.Status == http.StatusPaymentRequired {
		return &advisedError{
			cause:   err,
			message: withNote(err.Error(), "Add credits to the account this key belongs to"),
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

	var advised *advisedError
	if text, isText := api.Body.(string); isText && !errors.As(err, &advised) {
		return &advisedError{cause: err, message: textBody(api, text)}
	}

	return err
}

func textBody(api *onesie.APIError, body string) string {
	line := bodyLine(body)

	// onesie-go decodes a JSON string body and a body that is not JSON alike, so only the content
	// type tells a server's quoted message from a page a proxy sent.
	if jsonContent(api.Header.Get("Content-Type")) && !strings.HasPrefix(line, "<") {
		if line == "" {
			line = http.StatusText(api.Status)
		}

		return fmt.Sprintf("onesie: %d %s", api.Status, line)
	}

	message := fmt.Sprintf("onesie: %d %s, with a response body that is not JSON", api.Status, http.StatusText(api.Status))

	// An HTML page from a proxy or a web server says nothing a terminal line can show, while a one
	// line text body may name the cause.
	if line != "" && !strings.HasPrefix(line, "<") {
		message += ": " + line
	}

	// Only a missing path or method points at the base URL. An outage, a rate limit or a refused key
	// arrives from the right server too.
	if api.Status != http.StatusNotFound && api.Status != http.StatusMethodNotAllowed {
		return message
	}

	return withNote(message, "Check that the base URL points at the System One API")
}

func jsonContent(contentType string) bool {
	media, _, err := mime.ParseMediaType(contentType)

	return err == nil && (media == "application/json" || strings.HasSuffix(media, "+json"))
}

func bodyLine(body string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	line = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, line))

	const most = 120

	if runes := []rune(line); len(runes) > most {
		return string(runes[:most]) + "..."
	}

	return line
}

func adviseLongContext(err error, helps bool) error {
	var api *onesie.APIError
	if !helps || !errors.As(err, &api) || !strings.Contains(api.Error(), "max_tokens_exceeded") {
		return err
	}

	return &advisedError{
		cause:   err,
		message: withNote(err.Error(), "Pass --long-context to raise the context limit, which is slower and less accurate"),
	}
}

func longContextHelps(provider onesie.Provider, model string, on bool) bool {
	return !on && provider.Refuse(model, onesie.Needs{LongContext: true}) == nil
}

func adviseBox(err error, provider string, boxed bool) error {
	var api *onesie.APIError
	if !boxed || provider != onesie.TypeSafe().Name || !errors.As(err, &api) ||
		api.Status != http.StatusBadRequest || !strings.Contains(strings.ToLower(api.Error()), "invalid request") {
		return err
	}

	// A model that does not serve box questions answers one with a bare Invalid request, which
	// names neither the question nor the model.
	return &advisedError{
		cause:   err,
		message: withNote(err.Error(), "A box question needs a model that serves it, such as -m jev-preview"),
	}
}

func withKeyHint(err error, settings rootSettings) error {
	if !keyRefused(err) {
		return err
	}

	chosen, resolveErr := runResolution(settings)
	if resolveErr != nil {
		return err
	}

	return keyHint(err, chosen)
}

func keyHint(err error, chosen resolution) error {
	if !keyRefused(err) {
		return err
	}

	// Not a request to check the key, which the server's own message often already makes.
	note := "The key came from " + chosen.provider.EnvAPIKey
	if chosen.profile != "" {
		note = "Replace the key with onesie profile update " + quotedName(chosen.profile) + " --key"
	}

	return &advisedError{cause: err, message: withNote(err.Error(), note)}
}

func keyRefused(err error) bool {
	return errors.Is(err, onesie.ErrAuthentication) || errors.Is(err, onesie.ErrPermissionDenied)
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

func unknownModel(api *onesie.APIError, model string) bool {
	// The id comes from what onesie sent rather than from a substring of the server's prose, whose
	// wording the server owns. A call carrying no model has nothing to name, so it keeps the
	// server's text.
	if model == "" || (api.Status != http.StatusBadRequest && api.Status != http.StatusNotFound &&
		api.Status != http.StatusUnprocessableEntity) {
		return false
	}

	// TypeSafe says Unknown model, OpenRouter says the model does not exist, Berget answers 404 with
	// Model not found, Levanto answers 422 with unknown model, and Workers AI answers 400 with No such
	// model.
	return strings.Contains(api.Error(), "Unknown model") || strings.Contains(api.Error(), "does not exist") ||
		strings.Contains(api.Error(), "Model not found") || strings.Contains(api.Error(), "unknown model") ||
		strings.Contains(api.Error(), "No such model")
}

func countRejected(api *onesie.APIError) bool {
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
	for _, subject := range []string{"level", "option", "image", "pixel"} {
		if strings.Contains(message, subject) {
			return true
		}
	}

	return false
}

func staleLimits(message string) string {
	return withNote(message, "onesie's own check passed, so its built in limits may be stale. Run onesie -V")
}

func withNote(message, note string) string {
	// The server may end its own sentence, and a second full stop right before the note reads as a
	// typo rather than as a boundary.
	if strings.HasSuffix(message, ".") {
		return message + " " + note
	}

	return message + ". " + note
}
