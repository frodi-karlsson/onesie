package jev

import (
	"log/slog"
	"net/http"
	"strings"
)

// redactedHeader copies a header for logging with credentials reduced. Bodies are not redacted,
// which matches the JavaScript SDK, so debug logging a request discloses its payload.
func redactedHeader(header http.Header) slog.Value {
	attrs := make([]slog.Attr, 0, len(header))

	for name, values := range header {
		attrs = append(attrs, slog.String(name, redactValue(name, strings.Join(values, ", "))))
	}

	return slog.GroupValue(attrs...)
}

func redactValue(name, value string) string {
	switch strings.ToLower(name) {
	case "authorization", "proxy-authorization", "x-api-key":
		return maskKey(value)
	case "cookie", "set-cookie":
		return "[redacted]"
	default:
		return value
	}
}

// maskKey keeps the scheme and the last four characters. The leading characters of an API key are
// the structured, guessable part, so they are exactly what must not reach a log line.
func maskKey(value string) string {
	scheme, secret, hasScheme := strings.Cut(value, " ")
	if !hasScheme {
		scheme, secret = "", value
	}

	tail := ""
	if len(secret) > 8 {
		tail = secret[len(secret)-4:]
	}

	if scheme == "" {
		return "***" + tail
	}

	return scheme + " ***" + tail
}
