package cli_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/frodi-karlsson/jev-cli/internal/cli"
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
		{name: "should report success for no error", err: nil, want: cli.ExitOK},
		{
			name: "should report auth for an unauthorized status",
			err:  apiError(http.StatusUnauthorized), want: cli.ExitAuth,
		},
		{
			name: "should report auth for a forbidden status",
			err:  apiError(http.StatusForbidden), want: cli.ExitAuth,
		},
		{
			name: "should report usage for an unprocessable entity",
			err:  apiError(http.StatusUnprocessableEntity), want: cli.ExitUsage,
		},
		{
			name: "should report usage for a not found",
			err:  apiError(http.StatusNotFound), want: cli.ExitUsage,
		},
		{
			name: "should report usage for a bad request",
			err:  apiError(http.StatusBadRequest), want: cli.ExitUsage,
		},
		{
			name: "should report transport for a request timeout status",
			err:  apiError(http.StatusRequestTimeout), want: cli.ExitTransport,
		},
		{
			name: "should report unavailable for rate limiting",
			err:  apiError(http.StatusTooManyRequests), want: cli.ExitUnavailable,
		},
		{
			name: "should report unavailable for a server error",
			err:  apiError(http.StatusInternalServerError), want: cli.ExitUnavailable,
		},
		{
			name: "should report unavailable for an overloaded server",
			err:  apiError(529), want: cli.ExitUnavailable,
		},
		{
			name: "should report unavailable for an over cap retry after",
			err: &jev.RetryAfterError{
				APIError:   *apiError(http.StatusTooManyRequests),
				RetryAfter: 120 * time.Second,
				Cap:        60 * time.Second,
			},
			want: cli.ExitUnavailable,
		},
		{
			name: "should report transport for a connection failure",
			err:  &jev.ConnectionError{Err: errors.New("refused")}, want: cli.ExitTransport,
		},
		{
			name: "should report transport for a timeout",
			err: &jev.TimeoutError{
				ConnectionError: jev.ConnectionError{Err: errors.New("deadline")},
				Timeout:         10 * time.Second,
			},
			want: cli.ExitTransport,
		},
		{
			name: "should report interrupt for a cancelled context",
			err:  context.Canceled, want: cli.ExitInterrupt,
		},
		{
			name: "should report transport for a bare deadline",
			err:  context.DeadlineExceeded, want: cli.ExitTransport,
		},
		{
			name: "should report usage for a validation error",
			err:  &jev.ValidationError{Message: "bad"}, want: cli.ExitUsage,
		},
		{
			name: "should report usage for an unrecognised error",
			err:  errors.New("something else"), want: cli.ExitUsage,
		},
		{
			name: "should see through a wrapped error",
			err:  fmt.Errorf("while asking: %w", apiError(http.StatusUnauthorized)),
			want: cli.ExitAuth,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := cli.Classify(tc.err); got != tc.want {
				t.Errorf("Classify(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
