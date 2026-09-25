package jev

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
			message:  "onesie: 400 bad question",
		},
		{
			name:     "should map 401 to ErrAuthentication",
			status:   401,
			body:     `{"message":"invalid key"}`,
			sentinel: ErrAuthentication,
			message:  "onesie: 401 invalid key",
		},
		{
			name:     "should map 403 to ErrPermissionDenied",
			status:   403,
			body:     `{"detail":"nope"}`,
			sentinel: ErrPermissionDenied,
			message:  "onesie: 403 nope",
		},
		{
			name:     "should map 404 to ErrNotFound",
			status:   404,
			body:     ``,
			sentinel: ErrNotFound,
			message:  "onesie: 404 status code, no body",
		},
		{
			name:     "should map 422 to ErrUnprocessableEntity",
			status:   422,
			body:     `{"detail":[{"loc":["body","questions"],"msg":"field required"}]}`,
			sentinel: ErrUnprocessableEntity,
			message:  "onesie: 422 questions: field required",
		},
		{
			name:     "should map 429 to ErrRateLimit",
			status:   429,
			body:     `{"error":{"message":"slow down"}}`,
			sentinel: ErrRateLimit,
			message:  "onesie: 429 slow down",
		},
		{
			name:     "should map 402 to ErrPaymentRequired",
			status:   402,
			body:     `{"error":{"message":"Insufficient credits","code":402}}`,
			sentinel: ErrPaymentRequired,
			message:  "onesie: 402 Insufficient credits",
		},
		{
			name:     "should keep a plain error message unchanged",
			status:   400,
			body:     `{"error":{"message":"HTTP is not JSON","code":400}}`,
			sentinel: ErrBadRequest,
			message:  "onesie: 400 HTTP is not JSON",
		},
		{
			name:     "should keep the whole text when an HTTP prefix holds no JSON",
			status:   400,
			body:     `{"error":{"message":"HTTP 400: upstream said no","code":400}}`,
			sentinel: ErrBadRequest,
			message:  "onesie: 400 HTTP 400: upstream said no",
		},
		{
			name:     "should map 529 to ErrServer",
			status:   529,
			body:     `overloaded`,
			sentinel: ErrServer,
			message:  "onesie: 529 overloaded",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := newAPIError(tc.status, http.Header{}, "X-TypeSafe-Request-Id", []byte(tc.body), time.Now())

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
	fixtures := []struct {
		name    string
		file    string
		message string
	}{
		{
			name:    "should report the first OpenRouter validation issue with its path",
			file:    "openrouter-validation.json",
			message: "onesie: 400 questions.q.criteria.false: Invalid input",
		},
		{
			name:    "should unwrap a TypeSafe detail forwarded by OpenRouter",
			file:    "openrouter-detail.json",
			message: "onesie: 400 Too many score levels. Must have at most 10 levels.",
		},
		{
			name:    "should unwrap a TypeSafe error type forwarded by OpenRouter",
			file:    "openrouter-tokens.json",
			message: "onesie: 400 max_tokens_exceeded",
		},
	}

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body, err := os.ReadFile(filepath.Join("testdata", tc.file))
			if err != nil {
				t.Fatalf("reading the fixture: %v", err)
			}

			got := newAPIError(400, http.Header{}, "X-Generation-Id", body, time.Now()).Error()
			if got != tc.message {
				t.Errorf("message\n got: %s\nwant: %s", got, tc.message)
			}
		})
	}

	t.Run("should build an error from a status and a message alone", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			status   int
			sentinel error
		}{
			{status: 401, sentinel: ErrAuthentication},
			{status: 503, sentinel: ErrServer},
		}

		for _, c := range cases {
			err := NewAPIError(c.status, "onesie: mock status")

			if err.Status != c.status {
				t.Errorf("status got %d, want %d", err.Status, c.status)
			}

			if err.Error() != "onesie: mock status" {
				t.Errorf("message got %q", err.Error())
			}

			if !errors.Is(err, c.sentinel) {
				t.Errorf("status %d did not match its sentinel", c.status)
			}
		}

		if errors.Is(NewAPIError(401, "x"), ErrServer) {
			t.Errorf("a 401 matched ErrServer")
		}
	})

	t.Run("should only match ErrPaymentRequired for 402", func(t *testing.T) {
		t.Parallel()

		for _, status := range []int{400, 401, 403} {
			err := newAPIError(status, http.Header{}, "X-Generation-Id", nil, time.Now())
			if errors.Is(err, ErrPaymentRequired) {
				t.Errorf("status %d matched ErrPaymentRequired", status)
			}
		}
	})

	t.Run("should fill the error's fields from the response", func(t *testing.T) {
		t.Parallel()

		t.Run("should read the request id from the header", func(t *testing.T) {
			t.Parallel()

			header := http.Header{"X-Typesafe-Request-Id": {"req_123"}}

			if got := newAPIError(500, header, "X-TypeSafe-Request-Id", nil, time.Now()).RequestID; got != "req_123" {
				t.Errorf("request id got %q, want %q", got, "req_123")
			}
		})

		t.Run("should read retry after seconds into a duration", func(t *testing.T) {
			t.Parallel()

			header := http.Header{"Retry-After": {"3"}}

			if got := newAPIError(429, header, "X-TypeSafe-Request-Id", nil, time.Now()).RetryAfter; got != 3*time.Second {
				t.Errorf("retry after got %v, want %v", got, 3*time.Second)
			}
		})

		t.Run("should leave retry after zero when the header is absent", func(t *testing.T) {
			t.Parallel()

			if got := newAPIError(429, http.Header{}, "X-TypeSafe-Request-Id", nil, time.Now()).RetryAfter; got != 0 {
				t.Errorf("retry after got %v, want 0", got)
			}
		})

		t.Run("should truncate a long plain text body", func(t *testing.T) {
			t.Parallel()

			body := []byte(fmt.Sprintf("%400s", "x"))

			got := newAPIError(500, http.Header{}, "X-TypeSafe-Request-Id", body, time.Now()).Error()

			if len(got) > maxBodyInError+30 {
				t.Errorf("message was not truncated, length %d", len(got))
			}
		})

		t.Run("should truncate a long extracted message", func(t *testing.T) {
			t.Parallel()

			body := []byte(fmt.Sprintf(`{"error":"%400s"}`, "x"))

			got := newAPIError(500, http.Header{}, "X-TypeSafe-Request-Id", body, time.Now()).Error()

			if len(got) > maxBodyInError+30 {
				t.Errorf("extracted message was not truncated, length %d", len(got))
			}
		})
	})
}

func TestRetryAfterError(t *testing.T) {
	t.Parallel()

	t.Run("should name the status, the requested delay and the cap", func(t *testing.T) {
		t.Parallel()

		err := &RetryAfterError{
			APIError:   APIError{Status: 429},
			RetryAfter: 2 * time.Minute,
			Cap:        time.Minute,
		}

		want := "onesie: status 429, the server asked to retry after 2m0s, above the 1m0s cap"
		if got := err.Error(); got != want {
			t.Errorf("message got %q, want %q", got, want)
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
			want: `onesie: no answer named "absent"`,
		},
		{
			name: "should report a type mismatch",
			err:  &AnswerError{Name: "department", Want: "noul", Got: "choice"},
			want: `onesie: answer "department" is a choice, not a noul`,
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

func TestTruncate(t *testing.T) {
	t.Parallel()

	t.Run("should never cut a character in half", func(t *testing.T) {
		t.Parallel()

		got := truncate(strings.Repeat("a", maxBodyInError-1) + "é and more")

		if !utf8.ValidString(got) {
			t.Errorf("truncate returned invalid UTF-8: %q", got)
		}

		if !strings.HasSuffix(got, "...") {
			t.Errorf("truncate = %q, want it to end in ...", got)
		}
	})

	t.Run("should leave a short message as it is", func(t *testing.T) {
		t.Parallel()

		if got := truncate("short"); got != "short" {
			t.Errorf("truncate = %q, want short", got)
		}
	})
}
