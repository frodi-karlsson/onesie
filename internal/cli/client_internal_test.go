package cli

import (
	"net/http"
	"testing"

	"github.com/frodi-karlsson/onesie/onesie"
)

func TestEndsInVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "should not warn for the typesafe default", baseURL: onesie.TypeSafe().BaseURL},
		{name: "should not warn for the openrouter default", baseURL: onesie.OpenRouter().BaseURL},
		{name: "should not warn for the berget default", baseURL: onesie.Berget().BaseURL},
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

func TestPooled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		jobs int
		want int
	}{
		{name: "should use one connection when jobs is zero", jobs: 0, want: 1},
		{name: "should keep one idle connection per worker", jobs: 8, want: 8},
		{name: "should raise the total idle limit when jobs exceeds it", jobs: 128, want: 128},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			transport, ok := pooled(tc.jobs).(*http.Transport)
			if !ok {
				t.Fatal("pooled did not return an HTTP transport")
			}

			if transport.MaxConnsPerHost != tc.want {
				t.Errorf("MaxConnsPerHost = %d, want %d", transport.MaxConnsPerHost, tc.want)
			}

			if transport.MaxIdleConnsPerHost != tc.want {
				t.Errorf("MaxIdleConnsPerHost = %d, want %d", transport.MaxIdleConnsPerHost, tc.want)
			}

			if transport.MaxIdleConns < tc.want {
				t.Errorf("MaxIdleConns = %d, want at least %d", transport.MaxIdleConns, tc.want)
			}
		})
	}
}
