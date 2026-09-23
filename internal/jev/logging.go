package jev

import (
	"log/slog"
	"net/http"
	"strings"
)

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

func maskKey(value string) string {
	scheme, secret, hasScheme := strings.Cut(value, " ")
	if !hasScheme {
		scheme, secret = "", value
	}

	// Only the last four characters are kept. The leading characters of an API key are the
	// structured, guessable part, so they are exactly what must not reach a log line.
	tail := ""
	if len(secret) > 8 {
		tail = secret[len(secret)-4:]
	}

	if scheme == "" {
		return "***" + tail
	}

	return scheme + " ***" + tail
}
