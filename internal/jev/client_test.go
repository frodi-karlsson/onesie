package jev_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

// mockClock records every wait instead of performing it, so retry tests finish instantly. It is
// mutex guarded because the concurrency test drives one client from many goroutines.
type mockClock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *mockClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)

	return nil
}

func (c *mockClock) Slept() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]time.Duration(nil), c.slept...)
}

func newTestClient(t *testing.T, url string, opts ...jev.Option) (*jev.Client, *mockClock) {
	t.Helper()

	clock := &mockClock{now: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}

	base := []jev.Option{
		jev.WithEnv(func(string) (string, bool) { return "", false }),
		jev.WithAPIKey("sk-test"),
		jev.WithBaseURL(url),
		jev.WithClock(clock),
		jev.WithRandom(func() float64 { return 0 }),
	}

	client, err := jev.New(append(base, opts...)...)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	return client, clock
}

func oneNoul() jev.Questions {
	return jev.Questions{{ID: "q", Question: jev.Noul{Instructions: "Urgent?"}}}
}

const shortAnswer = `{"model":"m","answers":{"q":{"type":"noul","noul":0.1}},"usage":{}}`

func TestNew(t *testing.T) {
	t.Parallel()

	mockEnv := func(values map[string]string) func(string) (string, bool) {
		return func(name string) (string, bool) {
			value, ok := values[name]

			return value, ok
		}
	}

	t.Run("should fail without an api key", func(t *testing.T) {
		t.Parallel()

		_, err := jev.New(jev.WithEnv(mockEnv(nil)))
		if err == nil {
			t.Fatalf("expected an error, got none")
		}

		for _, want := range []string{"--api-key", jev.EnvAPIKey} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to name %q", err.Error(), want)
			}
		}
	})

	t.Run("should read the api key from the environment", func(t *testing.T) {
		t.Parallel()

		client, err := jev.New(jev.WithEnv(mockEnv(map[string]string{jev.EnvAPIKey: "sk-from-env"})))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if client.AttemptTimeout() != jev.DefaultAttemptTimeout {
			t.Errorf("attempt timeout got %v", client.AttemptTimeout())
		}

		if client.RetryPolicy().MaxRetries != 2 {
			t.Errorf("max retries got %d, want 2", client.RetryPolicy().MaxRetries)
		}
	})

	t.Run("should prefer an explicit option over the environment", func(t *testing.T) {
		t.Parallel()

		var seen atomic.Value

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Model string `json:"model"`
			}

			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			seen.Store(body.Model)

			_, _ = io.WriteString(w, shortAnswer)
		}))
		defer server.Close()

		client, err := jev.New(
			jev.WithEnv(mockEnv(map[string]string{
				jev.EnvAPIKey:       "sk-from-env",
				jev.EnvDefaultModel: "jev-from-env",
			})),
			jev.WithAPIKey("sk-explicit"),
			jev.WithBaseURL(server.URL),
			jev.WithDefaultModel("jev-explicit"),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if _, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if seen.Load() != "jev-explicit" {
			t.Errorf("model got %v, want %q", seen.Load(), "jev-explicit")
		}
	})

	t.Run("should ignore a blank environment value", func(t *testing.T) {
		t.Parallel()

		_, err := jev.New(jev.WithEnv(mockEnv(map[string]string{
			jev.EnvAPIKey:  "   ",
			jev.EnvBaseURL: "   ",
		})))

		if err == nil {
			t.Fatalf("a whitespace only key should be rejected")
		}
	})

	t.Run("should reject an invalid base url", func(t *testing.T) {
		t.Parallel()

		_, err := jev.New(
			jev.WithEnv(mockEnv(nil)),
			jev.WithAPIKey("sk"),
			jev.WithBaseURL("not a url"),
		)

		if !errors.Is(err, jev.ErrValidation) {
			t.Errorf("error got %v, want ErrValidation", err)
		}
	})

	t.Run("should reject an invalid retry policy", func(t *testing.T) {
		t.Parallel()

		policy := jev.DefaultRetryPolicy()
		policy.MaxRetries = -1

		if _, err := jev.New(jev.WithEnv(mockEnv(nil)), jev.WithAPIKey("sk"), jev.WithRetry(policy)); err == nil {
			t.Fatalf("expected an error, got none")
		}
	})

	t.Run("should reject a non positive attempt timeout", func(t *testing.T) {
		t.Parallel()

		if _, err := jev.New(jev.WithEnv(mockEnv(nil)), jev.WithAPIKey("sk"), jev.WithAttemptTimeout(0)); err == nil {
			t.Fatalf("expected an error, got none")
		}
	})
}

func TestSystemOne(t *testing.T) {
	t.Parallel()

	t.Run("should send the state questions and model then decode the answers", func(t *testing.T) {
		t.Parallel()

		var (
			mu      sync.Mutex
			body    map[string]json.RawMessage
			headers http.Header
		)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			headers = r.Header.Clone()

			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &body)
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-TypeSafe-Request-Id", "req_abc")
			w.Header().Set("X-RateLimit-Remaining", "42")
			_, _ = io.WriteString(w, `{
				"model":"jev-1.13.0",
				"answers":{"q":{"type":"noul","noul":0.95}},
				"usage":{"input_tokens":10,"output_tokens":2}
			}`)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		result, err := client.SystemOne(t.Context(), jev.Request{State: "charged twice", Questions: oneNoul()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()

		for _, key := range []string{"state", "model", "questions"} {
			if _, ok := body[key]; !ok {
				t.Errorf("request body is missing %q", key)
			}
		}

		if got := headers.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("authorization got %q", got)
		}

		for header, want := range map[string]string{
			"Content-Type": "application/json",
			"Accept":       "application/json",
		} {
			if got := headers.Get(header); got != want {
				t.Errorf("%s got %q, want %q", header, got, want)
			}
		}

		for _, header := range []string{"User-Agent", "X-TypeSafe-SDK", "X-TypeSafe-Runtime"} {
			if headers.Get(header) == "" {
				t.Errorf("%s was not set", header)
			}
		}

		if result.RequestID != "req_abc" {
			t.Errorf("request id got %q", result.RequestID)
		}

		if got := result.Header.Get("X-RateLimit-Remaining"); got != "42" {
			t.Errorf("response header got %q, want the rate limit value", got)
		}

		answer, err := result.Noul("q")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if answer.Noul != 0.95 {
			t.Errorf("noul got %v, want 0.95", answer.Noul)
		}
	})

	t.Run("should validate before sending anything", func(t *testing.T) {
		t.Parallel()

		var called atomic.Bool

		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			called.Store(true)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		if _, err := client.SystemOne(t.Context(), jev.Request{State: "x"}); err == nil {
			t.Fatalf("expected an error, got none")
		}

		if called.Load() {
			t.Errorf("the server was called despite invalid questions")
		}
	})

	t.Run("should not let a caller header clobber authorization", func(t *testing.T) {
		t.Parallel()

		var auth atomic.Value

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth.Store(r.Header.Get("Authorization"))
			_, _ = io.WriteString(w, shortAnswer)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		_, err := client.SystemOne(
			t.Context(),
			jev.Request{State: "x", Questions: oneNoul()},
			jev.WithRequestHeader("Authorization", "Bearer stolen"),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if auth.Load() != "Bearer sk-test" {
			t.Errorf("authorization got %v, want the client key", auth.Load())
		}
	})

	t.Run("should send a client level default header", func(t *testing.T) {
		t.Parallel()

		var seen atomic.Value

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen.Store(r.Header.Get("X-Trace"))
			_, _ = io.WriteString(w, shortAnswer)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL, jev.WithHeader("X-Trace", "abc"))

		if _, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if seen.Load() != "abc" {
			t.Errorf("trace header got %v, want %q", seen.Load(), "abc")
		}
	})

	t.Run("should drop a caller supplied retry count header", func(t *testing.T) {
		t.Parallel()

		var seen atomic.Value

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen.Store(r.Header.Get("X-TypeSafe-Retry-Count"))
			_, _ = io.WriteString(w, shortAnswer)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		_, err := client.SystemOne(
			t.Context(),
			jev.Request{State: "x", Questions: oneNoul()},
			jev.WithRequestHeader("X-TypeSafe-Retry-Count", "99"),
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if seen.Load() != "" {
			t.Errorf("retry count got %v, want it dropped on the first attempt", seen.Load())
		}
	})

	t.Run("should reject a response missing an answer that was asked for", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"model":"m","answers":{},"usage":{}}`)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		_, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()})

		if !errors.Is(err, jev.ErrResponse) {
			t.Errorf("error got %v, want ErrResponse", err)
		}
	})

	t.Run("should reject an empty body on a 200", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		_, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()})

		if !errors.Is(err, jev.ErrResponse) {
			t.Errorf("error got %v, want ErrResponse", err)
		}
	})

	t.Run("should parse json sent without a json content type", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"q":{"type":"noul","noul":0.5}},"usage":{}}`)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		result, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.Model != "jev-1.13.0" {
			t.Errorf("model got %q", result.Model)
		}
	})

	t.Run("should cap an oversized response body", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			for range 200 {
				_, _ = io.WriteString(w, `{"padding":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
			}
		}))
		defer server.Close()

		policy := jev.DefaultRetryPolicy()
		policy.MaxRetries = 0

		client, _ := newTestClient(t, server.URL, jev.WithMaxResponseBytes(256), jev.WithRetry(policy))

		_, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()})

		if !errors.Is(err, jev.ErrConnection) {
			t.Errorf("error got %v, want ErrConnection", err)
		}
	})
}

func TestSystemOneRetry(t *testing.T) {
	t.Parallel()

	t.Run("should retry a 429 then succeed", func(t *testing.T) {
		t.Parallel()

		var (
			calls  atomic.Int32
			mu     sync.Mutex
			counts []string
		)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			counts = append(counts, r.Header.Get("X-TypeSafe-Retry-Count"))
			mu.Unlock()

			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)

				return
			}

			_, _ = io.WriteString(w, shortAnswer)
		}))
		defer server.Close()

		client, clock := newTestClient(t, server.URL)

		if _, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if calls.Load() != 2 {
			t.Errorf("call count got %d, want 2", calls.Load())
		}

		if slept := clock.Slept(); len(slept) != 1 || slept[0] != 500*time.Millisecond {
			t.Errorf("waits got %v, want one 500ms wait", slept)
		}

		mu.Lock()
		defer mu.Unlock()

		if len(counts) != 2 || counts[0] != "" || counts[1] != "1" {
			t.Errorf("retry count header got %v", counts)
		}
	})

	t.Run("should honour retry after on a 429", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)

				return
			}

			_, _ = io.WriteString(w, shortAnswer)
		}))
		defer server.Close()

		client, clock := newTestClient(t, server.URL)

		if _, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if slept := clock.Slept(); len(slept) != 1 || slept[0] != 2*time.Second {
			t.Errorf("waits got %v, want one 2s wait", slept)
		}
	})

	t.Run("should floor a zero retry after rather than retrying immediately", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusServiceUnavailable)

				return
			}

			_, _ = io.WriteString(w, shortAnswer)
		}))
		defer server.Close()

		client, clock := newTestClient(t, server.URL)

		if _, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if slept := clock.Slept(); len(slept) != 1 || slept[0] != 500*time.Millisecond {
			t.Errorf("waits got %v, want the 500ms floor", slept)
		}
	})

	t.Run("should not retry a 422", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"detail":[{"loc":["body","state"],"msg":"field required"}]}`)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		_, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()})

		if !errors.Is(err, jev.ErrUnprocessableEntity) {
			t.Fatalf("error got %v, want ErrUnprocessableEntity", err)
		}

		if calls.Load() != 1 {
			t.Errorf("call count got %d, want 1", calls.Load())
		}
	})

	t.Run("should give up after MaxRetries and return the last error", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(529)
		}))
		defer server.Close()

		client, clock := newTestClient(t, server.URL)

		_, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()})

		if !errors.Is(err, jev.ErrServer) {
			t.Fatalf("error got %v, want ErrServer", err)
		}

		if calls.Load() != 3 {
			t.Errorf("call count got %d, want 3, one attempt plus two retries", calls.Load())
		}

		want := []time.Duration{500 * time.Millisecond, time.Second}
		slept := clock.Slept()

		if len(slept) != len(want) {
			t.Fatalf("waits got %v, want %v", slept, want)
		}

		for i, d := range want {
			if slept[i] != d {
				t.Errorf("wait %d got %v, want %v", i, slept[i], d)
			}
		}
	})

	t.Run("should retry a connection failure when the policy allows it", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				// Hijack and close without a response, which the client sees as a connection error.
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					return
				}

				conn, _, hijackErr := hijacker.Hijack()
				if hijackErr != nil {
					return
				}

				_ = conn.Close()

				return
			}

			_, _ = io.WriteString(w, shortAnswer)
		}))
		defer server.Close()

		client, clock := newTestClient(t, server.URL)

		if _, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if calls.Load() != 2 {
			t.Errorf("call count got %d, want 2", calls.Load())
		}

		if len(clock.Slept()) != 1 {
			t.Errorf("waits got %v, want one", clock.Slept())
		}
	})

	t.Run("should not retry a connection failure when RetryConnection is off", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)

			hijacker, ok := w.(http.Hijacker)
			if !ok {
				return
			}

			conn, _, hijackErr := hijacker.Hijack()
			if hijackErr != nil {
				return
			}

			_ = conn.Close()
		}))
		defer server.Close()

		policy := jev.DefaultRetryPolicy()
		policy.RetryConnection = false

		client, _ := newTestClient(t, server.URL, jev.WithRetry(policy))

		_, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()})

		if !errors.Is(err, jev.ErrConnection) {
			t.Fatalf("error got %v, want ErrConnection", err)
		}

		if calls.Load() != 1 {
			t.Errorf("call count got %d, want 1", calls.Load())
		}
	})

	t.Run("should respect a per call retry override", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		policy := client.RetryPolicy()
		policy.MaxRetries = 0

		_, err := client.SystemOne(
			t.Context(),
			jev.Request{State: "x", Questions: oneNoul()},
			jev.WithRequestRetry(policy),
		)
		if err == nil {
			t.Fatalf("expected an error, got none")
		}

		if calls.Load() != 1 {
			t.Errorf("call count got %d, want 1", calls.Load())
		}
	})
}

func TestSystemOneCancellation(t *testing.T) {
	t.Parallel()

	blockingServer := func(t *testing.T) *httptest.Server {
		t.Helper()

		release := make(chan struct{})

		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			<-release
		}))

		t.Cleanup(func() {
			close(release)
			server.Close()
		})

		return server
	}

	t.Run("should return the context error when the caller cancels", func(t *testing.T) {
		t.Parallel()

		client, _ := newTestClient(t, blockingServer(t).URL)

		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		_, err := client.SystemOne(ctx, jev.Request{State: "x", Questions: oneNoul()})

		if !errors.Is(err, context.Canceled) {
			t.Errorf("error got %v, want context.Canceled", err)
		}
	})

	t.Run("should report a timeout as a connection error", func(t *testing.T) {
		t.Parallel()

		policy := jev.DefaultRetryPolicy()
		policy.MaxRetries = 0

		client, _ := newTestClient(t, blockingServer(t).URL, jev.WithRetry(policy))

		_, err := client.SystemOne(
			t.Context(),
			jev.Request{State: "x", Questions: oneNoul()},
			jev.WithRequestAttemptTimeout(30*time.Millisecond),
		)

		if !errors.Is(err, jev.ErrTimeout) {
			t.Fatalf("error got %v, want ErrTimeout", err)
		}

		if !errors.Is(err, jev.ErrConnection) {
			t.Errorf("a timeout should also match ErrConnection")
		}
	})

	t.Run("should give each attempt a fresh timeout rather than one budget", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		release := make(chan struct{})

		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			calls.Add(1)
			<-release
		}))

		defer func() {
			close(release)
			server.Close()
		}()

		client, _ := newTestClient(t, server.URL)

		_, err := client.SystemOne(
			t.Context(),
			jev.Request{State: "x", Questions: oneNoul()},
			jev.WithRequestAttemptTimeout(30*time.Millisecond),
		)

		if !errors.Is(err, jev.ErrTimeout) {
			t.Fatalf("error got %v, want ErrTimeout", err)
		}

		// Three attempts each got their own deadline, proving the budget is not shared.
		if calls.Load() != 3 {
			t.Errorf("call count got %d, want 3", calls.Load())
		}
	})

	t.Run("should abort while waiting to retry", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(t.Context())

		// The mock clock returns immediately, so cancel before the call to make the wait the
		// first thing that observes the cancellation.
		cancel()

		client, _ := newTestClient(t, server.URL)

		_, err := client.SystemOne(ctx, jev.Request{State: "x", Questions: oneNoul()})

		if !errors.Is(err, context.Canceled) {
			t.Errorf("error got %v, want context.Canceled", err)
		}
	})

	t.Run("should honour a total timeout across retries", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(20 * time.Millisecond)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL, jev.WithTotalTimeout(30*time.Millisecond))

		_, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()})

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("error got %v, want context.DeadlineExceeded", err)
		}
	})
}

func TestClientConcurrency(t *testing.T) {
	t.Parallel()

	t.Run("should serve many goroutines from one client", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, shortAnswer)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		var wg sync.WaitGroup

		for range 16 {
			wg.Add(1)

			go func() {
				defer wg.Done()

				if _, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()}); err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}()
		}

		wg.Wait()
	})
}

func TestListModels(t *testing.T) {
	t.Parallel()

	t.Run("should unwrap the models list", func(t *testing.T) {
		t.Parallel()

		var (
			mu          sync.Mutex
			method      string
			path        string
			contentType string
		)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			method, path = r.Method, r.URL.Path
			contentType = r.Header.Get("Content-Type")
			mu.Unlock()

			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"models":[
				{"name":"jev-latest","description":"Flagship","release_date":"2026-08-01"}
			]}`)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		models, err := client.ListModels(t.Context())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()

		if method != http.MethodGet || path != "/v1/models" {
			t.Errorf("request got %s %s", method, path)
		}

		if contentType != "" {
			t.Errorf("content type got %q, want it unset on a bodyless request", contentType)
		}

		if len(models) != 1 || models[0].Name != "jev-latest" {
			t.Errorf("models got %+v", models)
		}
	})

	t.Run("should reject an unexpected response shape", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"data":[]}`)
		}))
		defer server.Close()

		client, _ := newTestClient(t, server.URL)

		_, err := client.ListModels(t.Context())

		if !errors.Is(err, jev.ErrResponse) {
			t.Errorf("error got %v, want ErrResponse", err)
		}
	})
}

func TestClientRetryAfterCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		retryAfter string
		cap        time.Duration
		wantCalls  int
		wantErr    bool
	}{
		{
			name:       "should stop immediately when the header exceeds the cap",
			retryAfter: "120",
			cap:        60 * time.Second,
			wantCalls:  1,
			wantErr:    true,
		},
		{
			name:       "should retry normally when the header is inside the cap",
			retryAfter: "1",
			cap:        60 * time.Second,
			wantCalls:  3,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int64

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", tc.retryAfter)
				w.WriteHeader(http.StatusTooManyRequests)
				if _, err := w.Write([]byte(`{"error":{"message":"slow down"}}`)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			policy := jev.DefaultRetryPolicy()
			policy.MaxRetryAfter = tc.cap

			client, _ := newTestClient(t, srv.URL, jev.WithRetry(policy))

			_, err := client.SystemOne(t.Context(), jev.Request{
				State:     "hello",
				Questions: oneNoul(),
			})

			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got none")
			}

			if got := int(calls.Load()); got != tc.wantCalls {
				t.Errorf("server calls = %d, want %d", got, tc.wantCalls)
			}

			if tc.wantCalls == 1 {
				var tooLong *jev.RetryAfterError
				if !errors.As(err, &tooLong) {
					t.Fatalf("expected a *RetryAfterError, got %T: %v", err, err)
				}

				if tooLong.RetryAfter != 120*time.Second {
					t.Errorf("RetryAfter = %s, want 2m0s", tooLong.RetryAfter)
				}

				if !errors.Is(err, jev.ErrRateLimit) {
					t.Error("expected errors.Is to reach ErrRateLimit through Unwrap")
				}
			}
		})
	}
}

func TestWithAttemptObserver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statuses    []int
		wantReports []int
	}{
		{
			name:        "should report one attempt for a clean request",
			statuses:    []int{http.StatusOK},
			wantReports: []int{200},
		},
		{
			name:        "should report every attempt including the retries",
			statuses:    []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusOK},
			wantReports: []int{429, 500, 200},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var index atomic.Int64

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				status := tc.statuses[int(index.Add(1))-1]
				w.WriteHeader(status)

				body := `{"error":{"message":"nope"}}`
				if status == http.StatusOK {
					body = `{"model":"jev-1.0.0","answers":{"q":{"type":"noul","noul":0.5}},` +
						`"usage":{"input_tokens":1,"output_tokens":1}}`
				}

				if _, err := w.Write([]byte(body)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			var (
				mu      sync.Mutex
				reports []int
			)

			client, _ := newTestClient(t, srv.URL, jev.WithAttemptObserver(func(a jev.Attempt) {
				mu.Lock()
				defer mu.Unlock()

				reports = append(reports, a.Status)
			}))

			if _, err := client.SystemOne(t.Context(), jev.Request{State: "x", Questions: oneNoul()}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()

			if !slices.Equal(reports, tc.wantReports) {
				t.Errorf("observed statuses = %v, want %v", reports, tc.wantReports)
			}
		})
	}
}

func TestClientSystemOneRaw(t *testing.T) {
	t.Parallel()

	compact := json.RawMessage(`{"state":"hi","model":"jev-latest","questions":{}}`)

	tests := []struct {
		name     string
		status   int
		response string
		send     json.RawMessage
		wantSent string
		wantErr  bool
	}{
		{
			name:     "should return the response body unchanged",
			status:   http.StatusOK,
			response: `{"model":"jev-1.0.0","answers":{"q":{"type":"noul","noul":0.25}}}`,
		},
		{
			name:     "should return an api error for a non 2xx",
			status:   http.StatusUnprocessableEntity,
			response: `{"error":{"message":"state too large"}}`,
			wantErr:  true,
		},
		{
			name:     "should compact a pretty printed body without reordering it",
			status:   http.StatusOK,
			response: `{"model":"jev-1.0.0","answers":{}}`,
			send:     json.RawMessage("{\n  \"state\": \"hi\",\n  \"model\": \"jev-latest\"\n}"),
			wantSent: `{"state":"hi","model":"jev-latest"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu   sync.Mutex
				sent []byte
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("reading request body: %v", err)
				}

				mu.Lock()
				sent = body
				mu.Unlock()

				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.response)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}
			}))
			defer srv.Close()

			client, _ := newTestClient(t, srv.URL)

			body := tc.send
			if body == nil {
				body = compact
			}

			got, err := client.SystemOneRaw(t.Context(), body)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got none")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			wantSent := tc.wantSent
			if wantSent == "" {
				wantSent = string(body)
			}

			mu.Lock()
			gotSent := string(sent)
			mu.Unlock()

			if gotSent != wantSent {
				t.Errorf("sent body = %s, want %s", gotSent, wantSent)
			}

			if string(got) != tc.response {
				t.Errorf("returned body = %s, want %s", got, tc.response)
			}
		})
	}
}

func TestMarshalBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  jev.Request
		want string
	}{
		{
			name: "should encode state model and questions in that order",
			req: jev.Request{
				State: "the server is down",
				Model: "jev-1.13.0",
				Questions: jev.Questions{
					{ID: "urgent", Question: jev.Noul{Instructions: "q"}},
				},
			},
			want: `{"state":"the server is down","model":"jev-1.13.0","questions":` +
				`{"urgent":{"type":"noul","instructions":"q"}}}`,
		},
		{
			name: "should keep a raw state's key order and its digits",
			req: jev.Request{
				State: json.RawMessage(`{"ticket_id":12345678901234567890,"zebra":1}`),
				Model: "jev-1.13.0",
				Questions: jev.Questions{
					{ID: "a", Question: jev.Noul{Instructions: "q"}},
				},
			},
			want: `{"state":{"ticket_id":12345678901234567890,"zebra":1},` +
				`"model":"jev-1.13.0","questions":` +
				`{"a":{"type":"noul","instructions":"q"}}}`,
		},
		{
			name: "should keep the questions in slice order",
			req: jev.Request{
				State: "s",
				Model: "m",
				Questions: jev.Questions{
					{ID: "zebra", Question: jev.Noul{Instructions: "z"}},
					{ID: "alpha", Question: jev.Noul{Instructions: "a"}},
				},
			},
			want: `{"state":"s","model":"m","questions":` +
				`{"zebra":{"type":"noul","instructions":"z"},` +
				`"alpha":{"type":"noul","instructions":"a"}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := jev.MarshalBody(tc.req)
			if err != nil {
				t.Fatalf("MarshalBody: %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("MarshalBody = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestMarshalQuestionsBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  jev.Request
		want string
	}{
		{
			name: "should omit the state entirely",
			req: jev.Request{
				State: "ignored",
				Model: "jev-1.13.0",
				Questions: jev.Questions{
					{ID: "urgent", Question: jev.Noul{Instructions: "q"}},
				},
			},
			want: `{"model":"jev-1.13.0","questions":` +
				`{"urgent":{"type":"noul","instructions":"q"}}}`,
		},
		{
			name: "should keep the questions in slice order",
			req: jev.Request{
				Model: "m",
				Questions: jev.Questions{
					{ID: "zebra", Question: jev.Noul{Instructions: "z"}},
					{ID: "alpha", Question: jev.Noul{Instructions: "a"}},
				},
			},
			want: `{"model":"m","questions":` +
				`{"zebra":{"type":"noul","instructions":"z"},` +
				`"alpha":{"type":"noul","instructions":"a"}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := jev.MarshalQuestionsBody(tc.req)
			if err != nil {
				t.Fatalf("MarshalQuestionsBody: %v", err)
			}

			if string(got) != tc.want {
				t.Errorf("MarshalQuestionsBody = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestResolveModel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		model string
		env   map[string]string
		want  string
	}{
		{
			name:  "should prefer an explicit model",
			model: "jev-1.9.9",
			env:   map[string]string{jev.EnvDefaultModel: "jev-1.2.0"},
			want:  "jev-1.9.9",
		},
		{
			name: "should fall back to the environment",
			env:  map[string]string{jev.EnvDefaultModel: "jev-1.2.0"},
			want: "jev-1.2.0",
		},
		{
			name: "should fall back to the built in default",
			want: jev.DefaultModel,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := jev.ResolveModel(tc.model, lookupFrom(tc.env)); got != tc.want {
				t.Errorf("ResolveModel = %s, want %s", got, tc.want)
			}
		})
	}

	// A client resolving the model its own way is the drift this guards against, so every case is
	// asserted twice: once against ResolveModel and once against the model a client really sends.
	for _, tc := range tests {
		t.Run(tc.name+" in a sent body", func(t *testing.T) {
			t.Parallel()

			var (
				mu   sync.Mutex
				body struct {
					Model string `json:"model"`
				}
			)

			server := httptest.NewServer(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					raw, readErr := io.ReadAll(r.Body)
					if readErr != nil {
						t.Errorf("reading the request body: %v", readErr)
					}

					mu.Lock()
					if err := json.Unmarshal(raw, &body); err != nil {
						t.Errorf("decoding the request body: %v", err)
					}
					mu.Unlock()

					w.Header().Set("Content-Type", "application/json")

					if _, err := io.WriteString(w, shortAnswer); err != nil {
						t.Errorf("writing the response: %v", err)
					}
				}))
			defer server.Close()

			client, err := jev.New(jev.WithEnv(lookupFrom(tc.env)),
				jev.WithAPIKey("sk-test"), jev.WithBaseURL(server.URL))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if _, err := client.SystemOne(t.Context(), jev.Request{
				State: "s", Model: tc.model, Questions: oneNoul(),
			}); err != nil {
				t.Fatalf("SystemOne: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()

			if body.Model != tc.want {
				t.Errorf("sent model = %s, want %s", body.Model, tc.want)
			}
		})
	}
}

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := env[name]

		return value, ok
	}
}
