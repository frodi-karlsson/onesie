package cli

import (
	"errors"
	"fmt"
	"net/http"
	"unicode"
	"unicode/utf8"

	"github.com/frodi-karlsson/onesie-go"
)

func sentence(cause error) string {
	var api *onesie.APIError
	if errors.As(cause, &api) {
		replacement, notes := adviceOf(cause)
		if replacement != "" {
			return joined(fmt.Sprintf("%s, status %d", replacement, api.Status), notes)
		}

		lead := fmt.Sprintf("%s, status %d", apiLead(cause, api), api.Status)

		return joined(lead, append([]string{apiDetail(cause, api)}, notes...))
	}

	var unusable *onesie.ResponseError
	if errors.As(cause, &unusable) {
		lead := fmt.Sprintf("the answer could not be used, status %d", unusable.Status)

		return joined(lead, []string{capitalised(text(unusable.Err))})
	}

	return text(cause)
}

func adviceOf(err error) (string, []string) {
	var advised *advisedError
	if !errors.As(err, &advised) {
		return "", nil
	}

	if advised.message != "" {
		return advised.message, []string{advised.note}
	}

	replacement, notes := adviceOf(advised.cause)

	return replacement, append(notes, advised.note)
}

func joined(lead string, parts []string) string {
	line := lead

	for _, part := range parts {
		if part != "" {
			line = withNote(line, part)
		}
	}

	return line
}

func apiLead(cause error, api *onesie.APIError) string {
	status := api.Status

	switch {
	case redirected(api):
		return "the server redirected the request"
	case status == http.StatusPaymentRequired:
		return "the account is out of credits"
	case authFailure(cause):
		return "the key was rejected"
	case status == http.StatusRequestTimeout:
		return "the server timed out waiting for the request"
	case status == http.StatusTooManyRequests:
		return "the server is limiting requests"
	case status >= http.StatusInternalServerError:
		return "the server failed"
	default:
		return "the server refused the request"
	}
}

func redirected(api *onesie.APIError) bool {
	return api.Status >= http.StatusMultipleChoices && api.Status < http.StatusBadRequest && api.Header.Get("Location") != ""
}

func apiDetail(cause error, api *onesie.APIError) string {
	var overCap *onesie.RetryAfterError
	if errors.As(cause, &overCap) {
		return fmt.Sprintf("The server asked to retry after %s, above the %s cap", overCap.RetryAfter, overCap.Cap)
	}

	return api.Detail()
}

func capitalised(text string) string {
	first, size := utf8.DecodeRuneInString(text)

	return string(unicode.ToUpper(first)) + text[size:]
}
