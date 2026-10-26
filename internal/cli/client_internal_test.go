package cli

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
)

func TestClientBuilder(t *testing.T) {
	t.Parallel()

	refused := "onesie: " + refusedBaseURL

	tests := []struct {
		name     string
		profiles string
		args     []string
		env      map[string]string
	}{
		{
			name: "should refuse a --base-url whose password holds a question mark",
			args: []string{"is this urgent", "--state", "hello", "-o", "json", "--base-url", "https://u:1?SECRET-x@proxy.example"},
			env:  map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
		},
		{
			name: "should refuse a --base-url with a query before any request",
			args: []string{"--list-models", "--base-url", "{url}/?SECRET-x"},
			env:  map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
		},
		{
			name: "should refuse a base url variable with a fragment before any request",
			args: []string{"is this urgent", "--state", "hello", "-o", "json"},
			env:  map[string]string{onesie.EnvAPIKey: "SECRET-ENV", onesie.EnvBaseURL: "{url}/#SECRET-x"},
		},
		{
			name: "should refuse a profile's base url with a query before profile test sends a request",
			profiles: `{"default":"ts","profiles":{"ts":{"provider":"typesafe","api_key":"SECRET-TS",` +
				`"base_url":"{url}/?SECRET-x"}}}`,
			args: []string{"profile", "test"},
		},
	}

	for _, unclear := range unclearBaseURLs() {
		// The host is swapped for the local server's, so a request that goes out is counted.
		tests = append(tests, struct {
			name     string
			profiles string
			args     []string
			env      map[string]string
		}{
			name: "should refuse a --base-url " + unclear.name + " before any request",
			args: []string{"--list-models", "--base-url", strings.ReplaceAll(unclear.url, "proxy.example", "{host}")},
			env:  map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
		})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			}))
			defer srv.Close()

			withURL := strings.NewReplacer("{url}", srv.URL, "{host}", strings.TrimPrefix(srv.URL, "http://")).Replace

			env := map[string]string{}
			for name, value := range tc.env {
				env[name] = withURL(value)
			}

			args := make([]string, 0, len(tc.args))
			for _, arg := range tc.args {
				args = append(args, withURL(arg))
			}

			dir := t.TempDir()
			if tc.profiles != "" {
				dir = configWith(t, withURL(tc.profiles))
			}

			out, errOut, code := runInConfig(t, dir, args, "", env)

			assertNoSecret(t, out, errOut)

			if code != ExitUsage {
				t.Errorf("exit code = %d, want %d", code, ExitUsage)
			}

			if !strings.Contains(out+errOut, refused) {
				t.Errorf("stdout and stderr carry no %q", refused)
			}

			if got := requests.Load(); got != 0 {
				t.Errorf("the server saw %d requests, want none", got)
			}
		})
	}
}

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
		{name: "should not warn for the levanto default", baseURL: onesie.Levanto().BaseURL},
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

func TestWithoutCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "should refuse in place of a message that echoes credentials",
			err: &onesie.ValidationError{
				Question: "q", Message: "base URL must be an absolute http or https URL, got ftp://u:SECRET-pw@proxy.example",
			},
			want: refusedBaseURL,
		},
		{
			name: "should keep a message with no @",
			err:  &onesie.ValidationError{Question: "q", Message: "no API key"},
			want: "no API key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got *onesie.ValidationError
			if !errors.As(withoutCredentials(tc.err), &got) {
				t.Fatal("the result is not a ValidationError, so its exit code would change")
			}

			if got.Message != tc.want || got.Question != "q" {
				t.Errorf("message = %q, question = %q, want %q and q", got.Message, got.Question, tc.want)
			}
		})
	}
}
