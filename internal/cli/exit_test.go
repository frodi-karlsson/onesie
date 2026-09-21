package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/frodi-karlsson/jev-cli/internal/input"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	apiError := func(status int) *jev.APIError {
		return &jev.APIError{Status: status}
	}

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "should report success for no error", err: nil, want: ExitOK},
		{
			name: "should report auth for an unauthorized status",
			err:  apiError(http.StatusUnauthorized), want: ExitAuth,
		},
		{
			name: "should report auth for a forbidden status",
			err:  apiError(http.StatusForbidden), want: ExitAuth,
		},
		{
			name: "should report usage for an unprocessable entity",
			err:  apiError(http.StatusUnprocessableEntity), want: ExitUsage,
		},
		{
			name: "should report usage for a not found",
			err:  apiError(http.StatusNotFound), want: ExitUsage,
		},
		{
			name: "should report usage for a bad request",
			err:  apiError(http.StatusBadRequest), want: ExitUsage,
		},
		{
			name: "should report transport for a request timeout status",
			err:  apiError(http.StatusRequestTimeout), want: ExitTransport,
		},
		{
			name: "should report unavailable for rate limiting",
			err:  apiError(http.StatusTooManyRequests), want: ExitUnavailable,
		},
		{
			name: "should report unavailable for a server error",
			err:  apiError(http.StatusInternalServerError), want: ExitUnavailable,
		},
		{
			name: "should report unavailable for an overloaded server",
			err:  apiError(529), want: ExitUnavailable,
		},
		{
			name: "should report unavailable for an over cap retry after",
			err: &jev.RetryAfterError{
				APIError:   *apiError(http.StatusTooManyRequests),
				RetryAfter: 120 * time.Second,
				Cap:        60 * time.Second,
			},
			want: ExitUnavailable,
		},
		{
			name: "should report unavailable for an unusable 200 body",
			err: &jev.ResponseError{
				Status: http.StatusOK, Err: errors.New("invalid character 'n'"),
			},
			want: ExitUnavailable,
		},
		{
			name: "should report transport for a connection failure",
			err:  &jev.ConnectionError{Err: errors.New("refused")}, want: ExitTransport,
		},
		{
			name: "should report transport for a timeout",
			err: &jev.TimeoutError{
				ConnectionError: jev.ConnectionError{Err: errors.New("deadline")},
				Timeout:         10 * time.Second,
			},
			want: ExitTransport,
		},
		{
			name: "should report interrupt for a cancelled context",
			err:  context.Canceled, want: ExitInterrupt,
		},
		{
			name: "should report transport for a bare deadline",
			err:  context.DeadlineExceeded, want: ExitTransport,
		},
		{
			name: "should report usage for a validation error",
			err:  &jev.ValidationError{Message: "bad"}, want: ExitUsage,
		},
		{
			name: "should report usage for an unrecognised error",
			err:  errors.New("something else"), want: ExitUsage,
		},
		{
			name: "should report records for a stream that finished with failures",
			err:  &recordsError{}, want: ExitRecords,
		},
		{
			name: "should report usage for an input error",
			err:  &input.LineError{Line: 3, Err: errors.New("line is not one complete JSON value")},
			want: ExitUsage,
		},
		{
			name: "should see through a wrapped error",
			err:  fmt.Errorf("while asking: %w", apiError(http.StatusUnauthorized)),
			want: ExitAuth,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := Classify(tc.err); got != tc.want {
				t.Errorf("Classify(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
