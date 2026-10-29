package cli

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
)

func TestSentence(t *testing.T) {
	t.Parallel()

	refusal := func(p onesie.Provider, status int, body string) *onesie.APIError {
		return onesie.NewAPIError(p, "", onesie.Needs{}, status, []byte(body))
	}
	typesafe := onesie.TypeSafe()
	fromEnv := resolution{provider: typesafe}
	unreachable := &url.Error{Op: "Post", URL: "https://mock.invalid/v1/system-one", Err: errors.New("mock connection error")}
	timedOut := &url.Error{Op: "Post", URL: "https://mock.invalid/v1/system-one", Err: errors.New("i/o timeout")}

	tests := []struct {
		name  string
		cause error
		want  string
	}{
		{
			name:  "should say the key was rejected for a 401 with no body",
			cause: refusal(typesafe, http.StatusUnauthorized, ""),
			want:  "the key was rejected, status 401",
		},
		{
			name: "should keep the provider's words and name the key's variable for a refused key",
			cause: keyHint(refusal(typesafe, http.StatusUnauthorized,
				`{"error":{"message":"Cannot authenticate with the server. Please check your API key and try again."}}`), fromEnv),
			want: "the key was rejected, status 401. Cannot authenticate with the server. Please check your API key " +
				"and try again. The key came from TYPESAFE_API_KEY",
		},
		{
			name: "should name the profile command for a refused profile key",
			cause: keyHint(refusal(onesie.OpenRouter(), http.StatusUnauthorized,
				`{"error":{"message":"No auth credentials found","code":401}}`),
				resolution{provider: onesie.OpenRouter(), profile: "work"}),
			want: "the key was rejected, status 401. No auth credentials found. " +
				"Replace the key with onesie profile update 'work' --key",
		},
		{
			name:  "should keep a refused key's text body",
			cause: keyHint(refusal(typesafe, http.StatusUnauthorized, "bad key\n"), fromEnv),
			want:  "the key was rejected, status 401. bad key. The key came from TYPESAFE_API_KEY",
		},
		{
			name:  "should say the key was rejected for a 403 with no body",
			cause: refusal(typesafe, http.StatusForbidden, ""),
			want:  "the key was rejected, status 403",
		},
		{
			name: "should say the key was rejected for cloudflare's 400 that refuses the token",
			cause: refusal(onesie.Cloudflare(), http.StatusBadRequest,
				`{"success":false,"errors":[{"code":9106,"message":"Authentication failed (status: 400)"}]}`),
			want: "the key was rejected, status 400. Authentication failed (status: 400)",
		},
		{
			name:  "should say the account is out of credits for a 402",
			cause: advise(refusal(typesafe, http.StatusPaymentRequired, ""), ""),
			want:  "the account is out of credits, status 402. Add credits to the account this key belongs to",
		},
		{
			name:  "should say the server refused a 400 with no body",
			cause: refusal(typesafe, http.StatusBadRequest, ""),
			want:  "the server refused the request, status 400",
		},
		{
			name:  "should say the server refused a 404 with no body",
			cause: refusal(typesafe, http.StatusNotFound, ""),
			want:  "the server refused the request, status 404",
		},
		{
			name: "should lead with the advice that replaced an unknown model's message",
			cause: explain(refusal(typesafe, http.StatusBadRequest, `{"detail":"Unknown model: jev-latest"}`),
				requestFacts{provider: typesafe, model: "jev-latest"}),
			want: "model 'jev-latest' not found, status 400. Try --list-models",
		},
		{
			name:  "should leave out an html page and keep the advice",
			cause: advise(refusal(typesafe, http.StatusMethodNotAllowed, "<html>nope</html>"), ""),
			want:  "the server refused the request, status 405. Check that the base URL points at the System One API",
		},
		{
			name:  "should say the server timed out waiting for a 408",
			cause: refusal(typesafe, http.StatusRequestTimeout, ""),
			want:  "the server timed out waiting for the request, status 408",
		},
		{
			name:  "should say the server is limiting requests for a 429",
			cause: refusal(typesafe, http.StatusTooManyRequests, ""),
			want:  "the server is limiting requests, status 429",
		},
		{
			name: "should give the requested delay of a Retry-After over the cap",
			cause: &onesie.RetryAfterError{
				APIError: *refusal(typesafe, http.StatusTooManyRequests, ""), RetryAfter: 2 * time.Minute, Cap: 30 * time.Second,
			},
			want: "the server is limiting requests, status 429. The server asked to retry after 2m0s, above the 30s cap",
		},
		{
			name:  "should say the server failed for a 503 with no body",
			cause: refusal(typesafe, http.StatusServiceUnavailable, ""),
			want:  "the server failed, status 503",
		},
		{
			name:  "should leave out a 502's html page",
			cause: refusal(typesafe, http.StatusBadGateway, "<html><body>Bad Gateway</body></html>"),
			want:  "the server failed, status 502",
		},
		{
			name:  "should say an oversized 502 is over the limit",
			cause: served(t, http.StatusBadGateway, strings.Repeat("x", 1<<20+1)),
			want:  "the server failed, status 502. The response body is over the 1 MiB limit",
		},
		{
			name:  "should give the host of a timeout the connection reports",
			cause: &onesie.TimeoutError{ConnectionError: onesie.ConnectionError{Err: timedOut}},
			want:  "request to mock.invalid:443 timed out",
		},
		{
			name:  "should give the attempt deadline that fired",
			cause: &onesie.TimeoutError{Timeout: 10 * time.Second},
			want:  "request timed out after 10s",
		},
		{
			name:  "should give the host of a connection that failed",
			cause: &onesie.ConnectionError{Err: unreachable},
			want:  "could not reach mock.invalid: mock connection error",
		},
		{
			name:  "should say the answer could not be used for a 200 with no answers",
			cause: served(t, http.StatusOK, `{}`),
			want:  "the answer could not be used, status 200. The response is not a System One result, since it has no answers",
		},
		{
			name:  "should say an oversized 200 is over the limit",
			cause: served(t, http.StatusOK, strings.Repeat("x", 1<<20+1)),
			want:  "the answer could not be used, status 200. The response body is over the 1 MiB limit",
		},
		{
			name:  "should give an input failure's message as it is",
			cause: &onesie.ValidationError{Message: "question too long"},
			want:  "question too long",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := sentence(tc.cause); got != tc.want {
				t.Errorf("sentence = %q, want %q", got, tc.want)
			}
		})
	}
}

func served(t *testing.T, status int, body string) error {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)

		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client, err := onesie.New(
		onesie.WithAPIKey("SECRET-TEST"),
		onesie.WithBaseURL(srv.URL),
		onesie.WithMaxResponseBytes(1<<20),
		onesie.WithClock(instantClock{now: time.Now}),
	)
	if err != nil {
		t.Fatalf("onesie.New: %v", err)
	}

	_, err = client.SystemOne(t.Context(), onesie.Request{
		State:     "a ticket",
		Questions: onesie.Questions{{ID: "urgent", Question: onesie.Noul{Instructions: "is this urgent"}}},
	})
	if err == nil {
		t.Fatalf("SystemOne answered %d without an error", status)
	}

	return err
}
