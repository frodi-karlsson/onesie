package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jev"
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
			name: "should report auth for a payment required status",
			err:  apiError(http.StatusPaymentRequired), want: ExitAuth,
		},
		{
			name: "should report unavailable for a 200 body it could not use",
			err:  &jev.ResponseError{Status: http.StatusOK, Message: "onesie: wrong shape"},
			want: ExitUnavailable,
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
		{name: "should classify a rejected policy as exit one", err: &rejectedError{}, want: ExitRejected},
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
		{
			name: "should report success when the consumer stopped reading",
			err:  &fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE},
			want: ExitOK,
		},
		{
			// The socket rather than stdout. A connection that broke under onesie is a transport
			// fault whatever errno the kernel chose for it.
			name: "should still report transport for a connection that broke with EPIPE",
			err:  &jev.ConnectionError{Err: syscall.EPIPE},
			want: ExitTransport,
		},
		{
			name: "should report the code a silent error carries",
			err:  &silentError{code: ExitAuth},
			want: ExitAuth,
		},
		{
			// A silent error means the command printed the whole result itself. Matched first it
			// would take the code from every error it was joined to, so it is matched last.
			name: "should keep the code of a real error joined to a silent one",
			err:  errors.Join(&silentError{code: ExitAuth}, &recordsError{}),
			want: ExitRecords,
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

func TestWorthReporting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "should report an ordinary failure", err: errors.New("boom"), want: true},
		{name: "should stay quiet for an interrupt", err: context.Canceled},
		{name: "should stay quiet for a policy rejection", err: &rejectedError{}},
		{name: "should stay quiet for a stream that failed records", err: &recordsError{}},
		{
			name: "should stay quiet when the consumer stopped reading",
			err:  &fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := worthReporting(tc.err); got != tc.want {
				t.Errorf("worthReporting(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

func TestAborting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "should abort on a refused key", status: http.StatusUnauthorized, want: true},
		{name: "should abort on a forbidden key", status: http.StatusForbidden, want: true},
		{name: "should abort when the account is out of credits", status: http.StatusPaymentRequired, want: true},
		{name: "should carry on after a bad request", status: http.StatusBadRequest},
		{name: "should carry on after a server error", status: http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := aborting(&jev.APIError{Status: tc.status}); got != tc.want {
				t.Errorf("aborting(%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
