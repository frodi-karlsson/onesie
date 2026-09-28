package cli

import (
	"testing"

	"github.com/frodi-karlsson/onesie/jev"
)

func TestEndsInVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "should not warn for the typesafe default", baseURL: jev.TypeSafe().BaseURL},
		{name: "should not warn for the openrouter default", baseURL: jev.OpenRouter().BaseURL},
		{name: "should not warn for the berget default", baseURL: jev.Berget().BaseURL},
		{name: "should not warn for a /v1 that is not the last segment", baseURL: "https://proxy.example/v1/proxy"},
		{name: "should not warn for a segment that only ends in v1", baseURL: "https://proxy.example/apiv1"},
		{name: "should warn for a path ending in /v1", baseURL: "https://proxy.example/v1", want: true},
		{name: "should warn for a path ending in /v1/", baseURL: "https://proxy.example/v1/", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := endsInVersion(tc.baseURL); got != tc.want {
				t.Errorf("endsInVersion(%q) = %v, want %v", tc.baseURL, got, tc.want)
			}
		})
	}
}
