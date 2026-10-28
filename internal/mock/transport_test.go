package mock

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestTransport(t *testing.T) {
	t.Parallel()

	noul := []plan.Question{{ID: "u", Shape: plan.Noul}}

	tests := []struct {
		name      string
		file      string
		provider  onesie.Provider
		wantModel string
		wantErr   string
	}{
		{
			name: "should answer with the model on the context and bill nothing", file: `{"u":0.3}`,
			provider: onesie.TypeSafe(), wantModel: "m",
		},
		{
			name: "should answer as cloudflare's route returns a result", file: `{"u":0.3}`,
			provider: onesie.Cloudflare(), wantModel: "m",
		},
		{
			name: "should send a response entry as a 200 with no answers", file: `{"error":{"kind":"response"}}`,
			provider: onesie.TypeSafe(),
			wantErr: "onesie: 200 response could not be used: " +
				"the response is not a System One result, since it has no answers",
		},
		{
			name: "should fail a timeout entry as a transport timeout", file: `{"error":"timeout"}`,
			provider: onesie.TypeSafe(), wantErr: "onesie: request to mock.invalid:443 timed out",
		},
		{
			name: "should fail a connection entry as a dropped connection", file: `{"error":"connection"}`,
			provider: onesie.Cloudflare(), wantErr: "onesie: could not reach mock.invalid: mock connection error",
		},
		{
			name: "should send a status entry's body for the route to read", file: `{"error":{"kind":"http","status":400,"body":{"detail":"Unknown model: m"}}}`,
			provider: onesie.TypeSafe(), wantErr: "onesie: 400 Unknown model: m",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answers, err := Load(strings.NewReader(tc.file), noul, Options{})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			entry, _ := answers.Lookup(1, nil)

			result, err := sendThrough(t, tc.provider, entry)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error\n got: %v\nwant: %s", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("SystemOne: %v", err)
			}

			if result.Model != tc.wantModel || result.Usage != (onesie.Usage{}) {
				t.Errorf("model %q usage %+v, want %q and zero usage", result.Model, result.Usage, tc.wantModel)
			}

			if got, err := result.Noul("u"); err != nil || got.Noul != 0.3 {
				t.Errorf("answer %+v, %v, want 0.3", got, err)
			}
		})
	}

	t.Run("should refuse a request that carries no entry", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://mock.invalid", http.NoBody)

		_, err := NewTransport(onesie.TypeSafe()).RoundTrip(req)
		if err == nil || !strings.Contains(err.Error(), "no mock entry, which is a bug in onesie") {
			t.Errorf("error = %v, want the bug named", err)
		}
	})
}

func sendThrough(t *testing.T, provider onesie.Provider, entry Entry) (*onesie.Result, error) {
	t.Helper()

	policy := onesie.DefaultRetryPolicy()
	policy.MaxRetries = 0

	client, err := onesie.New(
		onesie.WithProvider(provider), onesie.WithAPIKey("k"), onesie.WithBaseURL("https://mock.invalid"),
		onesie.WithEnv(func(string) (string, bool) { return "", false }), onesie.WithRetry(policy),
		onesie.WithHTTPClient(&http.Client{Transport: NewTransport(provider)}),
	)
	if err != nil {
		t.Fatalf("onesie.New: %v", err)
	}

	req := onesie.Request{
		Model: "m", State: "s", Questions: onesie.Questions{{ID: "u", Question: onesie.Noul{Instructions: "q"}}},
	}

	result, err := client.SystemOne(WithEntry(t.Context(), entry, req.Model), req)
	if errors.Is(err, onesie.ErrValidation) {
		t.Fatalf("SystemOne refused the request: %v", err)
	}

	return result, err
}

func resultOf(entry Entry) *onesie.Result {
	return &onesie.Result{Answers: entry.answers}
}
