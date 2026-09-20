package jev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestNewAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   int
		body     string
		sentinel error
		message  string
	}{
		{
			name:     "should map 400 to ErrBadRequest",
			status:   400,
			body:     `{"error":"bad question"}`,
			sentinel: ErrBadRequest,
			message:  "jev: 400 bad question",
		},
		{
			name:     "should map 401 to ErrAuthentication",
			status:   401,
			body:     `{"message":"invalid key"}`,
			sentinel: ErrAuthentication,
			message:  "jev: 401 invalid key",
		},
		{
			name:     "should map 403 to ErrPermissionDenied",
			status:   403,
			body:     `{"detail":"nope"}`,
			sentinel: ErrPermissionDenied,
			message:  "jev: 403 nope",
		},
		{
			name:     "should map 404 to ErrNotFound",
			status:   404,
			body:     ``,
			sentinel: ErrNotFound,
			message:  "jev: 404 status code, no body",
		},
		{
			name:     "should map 422 to ErrUnprocessableEntity",
			status:   422,
			body:     `{"detail":[{"loc":["body","questions"],"msg":"field required"}]}`,
			sentinel: ErrUnprocessableEntity,
			message:  "jev: 422 questions: field required",
		},
		{
			name:     "should map 429 to ErrRateLimit",
			status:   429,
			body:     `{"error":{"message":"slow down"}}`,
			sentinel: ErrRateLimit,
			message:  "jev: 429 slow down",
		},
		{
			name:     "should map 529 to ErrServer",
			status:   529,
			body:     `overloaded`,
			sentinel: ErrServer,
			message:  "jev: 529 overloaded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := newAPIError(tc.status, http.Header{}, []byte(tc.body), time.Now())

			if !errors.Is(err, tc.sentinel) {
				t.Errorf("errors.Is did not match the expected sentinel")
			}

			if err.Error() != tc.message {
				t.Errorf("message\n got: %s\nwant: %s", err.Error(), tc.message)
			}

			var api *APIError
			if !errors.As(err, &api) {
				t.Fatalf("errors.As failed to recover an *APIError")
			}

			if api.Status != tc.status {
				t.Errorf("status got %d, want %d", api.Status, tc.status)
			}
		})
	}
}

func TestAPIErrorFields(t *testing.T) {
	t.Parallel()

	t.Run("should read the request id from the header", func(t *testing.T) {
		t.Parallel()

		header := http.Header{"X-Typesafe-Request-Id": {"req_123"}}

		if got := newAPIError(500, header, nil, time.Now()).RequestID; got != "req_123" {
			t.Errorf("request id got %q, want %q", got, "req_123")
		}
	})

	t.Run("should read retry after seconds into a duration", func(t *testing.T) {
		t.Parallel()

		header := http.Header{"Retry-After": {"3"}}

		if got := newAPIError(429, header, nil, time.Now()).RetryAfter; got != 3*time.Second {
			t.Errorf("retry after got %v, want %v", got, 3*time.Second)
		}
	})

	t.Run("should leave retry after zero when the header is absent", func(t *testing.T) {
		t.Parallel()

		if got := newAPIError(429, http.Header{}, nil, time.Now()).RetryAfter; got != 0 {
			t.Errorf("retry after got %v, want 0", got)
		}
	})

	t.Run("should truncate a long plain text body", func(t *testing.T) {
		t.Parallel()

		body := []byte(fmt.Sprintf("%400s", "x"))

		got := newAPIError(500, http.Header{}, body, time.Now()).Error()

		if len(got) > maxBodyInError+30 {
			t.Errorf("message was not truncated, length %d", len(got))
		}
	})

	t.Run("should truncate a long extracted message", func(t *testing.T) {
		t.Parallel()

		body := []byte(fmt.Sprintf(`{"error":"%400s"}`, "x"))

		got := newAPIError(500, http.Header{}, body, time.Now()).Error()

		if len(got) > maxBodyInError+30 {
			t.Errorf("extracted message was not truncated, length %d", len(got))
		}
	})
}

func TestTimeoutError(t *testing.T) {
	t.Parallel()

	t.Run("should match both ErrTimeout and ErrConnection", func(t *testing.T) {
		t.Parallel()

		err := error(&TimeoutError{Timeout: time.Second})

		if !errors.Is(err, ErrConnection) {
			t.Errorf("a timeout should match ErrConnection")
		}

		if !errors.Is(err, ErrTimeout) {
			t.Errorf("a timeout should match ErrTimeout")
		}
	})

	t.Run("should be recoverable as a ConnectionError", func(t *testing.T) {
		t.Parallel()

		err := error(&TimeoutError{
			ConnectionError: ConnectionError{Err: context.DeadlineExceeded},
			Timeout:         time.Second,
		})

		var connection *ConnectionError
		if !errors.As(err, &connection) {
			t.Fatalf("errors.As should reach the embedded ConnectionError")
		}

		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("the unwrap chain should reach the transport error")
		}
	})

	t.Run("should not report a connection error as a timeout", func(t *testing.T) {
		t.Parallel()

		err := error(&ConnectionError{Err: errors.New("dial failed")})

		if errors.Is(err, ErrTimeout) {
			t.Errorf("a connection error should not match ErrTimeout")
		}
	})

	t.Run("should survive a nil cause", func(t *testing.T) {
		t.Parallel()

		if got := (&ConnectionError{}).Error(); got == "" {
			t.Errorf("a nil cause produced an empty message")
		}
	})
}

func TestAnswerError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  *AnswerError
		want string
	}{
		{
			name: "should report a missing answer",
			err:  &AnswerError{Name: "absent", Missing: true},
			want: `jev: no answer named "absent"`,
		},
		{
			name: "should report a type mismatch",
			err:  &AnswerError{Name: "department", Want: "noul", Got: "choice"},
			want: `jev: answer "department" is a choice, not a noul`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.err.Error(); got != tc.want {
				t.Errorf("\n got: %s\nwant: %s", got, tc.want)
			}
		})
	}
}
