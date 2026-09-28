package jev

import (
	"net/http"
	"strings"
	"testing"
)

func TestMaskKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "should keep the scheme and the last four characters",
			value: "Bearer sk-live-abcdefghijkl",
			want:  "Bearer ***ijkl",
		},
		{
			name:  "should mask a bare secret with no scheme",
			value: "sk-live-abcdefghijkl",
			want:  "***ijkl",
		},
		{
			name:  "should reveal nothing from a short secret",
			value: "Bearer short",
			want:  "Bearer ***",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := maskKey(tc.value)

			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}

			if strings.Contains(got, "sk-live") {
				t.Errorf("the credential prefix leaked into %q", got)
			}
		})
	}
}

func TestRedactedHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		value  string
		want   string
	}{
		{name: "should redact authorization", header: "Authorization", value: "Bearer sk-live-abcdefghijkl", want: "Bearer ***ijkl"},
		{name: "should redact proxy authorization", header: "Proxy-Authorization", value: "Basic abcdefghijkl", want: "Basic ***ijkl"},
		{name: "should redact an api key header", header: "X-Api-Key", value: "sk-live-abcdefghijkl", want: "***ijkl"},
		{name: "should replace a cookie wholesale", header: "Cookie", value: "session=abcdef", want: "[redacted]"},
		{name: "should replace a set cookie wholesale", header: "Set-Cookie", value: "session=abcdef", want: "[redacted]"},
		{name: "should leave an ordinary header alone", header: "Accept", value: "application/json", want: "application/json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rendered := redactedHeader(http.Header{tc.header: {tc.value}}).String()

			if !strings.Contains(rendered, tc.want) {
				t.Errorf("rendered %q, want it to contain %q", rendered, tc.want)
			}
		})
	}
}
