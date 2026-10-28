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

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	apiError := func(status int) *onesie.APIError {
		return &onesie.APIError{Status: status}
	}

	stored := func(kind output.Kind, status *int) error {
		return &storedFailure{failure: output.Failure{Kind: kind, Status: status, Message: "onesie: stored"}}
	}

	statusOf := func(status int) *int {
		return &status
	}

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "should report success for no error", err: nil, want: ExitOK},
		{
			name: "should report usage for a record a mock file does not answer",
			err:  &abortError{cause: &uncoveredError{message: "onesie: --mock has no line 3"}}, want: ExitUsage,
		},
		{
			name: "should report records for a stored input failure a resume stopped at",
			err:  stored("input", nil), want: ExitRecords,
		},
		{
			name: "should report records for a record --stop-on-error stopped at",
			err:  &stoppedError{cause: &input.LineError{Line: 2, Err: errors.New("not json")}}, want: ExitRecords,
		},
		{
			name: "should report usage for a bad input header that ended the source",
			err:  &sourceError{cause: &input.LineError{Line: 1, Err: errors.New("the header is not valid csv")}},
			want: ExitUsage,
		},
		{
			name: "should report unavailable for a stored unusable response",
			err:  stored("response", statusOf(http.StatusOK)), want: ExitUnavailable,
		},
		{
			name: "should report transport for a stored transport failure",
			err:  stored("transport", nil), want: ExitTransport,
		},
		{
			name: "should report auth for a stored payment required status",
			err:  stored("http", statusOf(http.StatusPaymentRequired)), want: ExitAuth,
		},
		{
			name: "should report unavailable for a stored rate limit",
			err:  stored("http", statusOf(http.StatusTooManyRequests)), want: ExitUnavailable,
		},
		{
			name: "should report transport for a stored request timeout status",
			err:  stored("http", statusOf(http.StatusRequestTimeout)), want: ExitTransport,
		},
		{
			name: "should report auth for a stored unauthorized status",
			err:  stored("http", statusOf(http.StatusUnauthorized)), want: ExitAuth,
		},
		{
			name: "should report auth for a stored forbidden status",
			err:  stored("http", statusOf(http.StatusForbidden)), want: ExitAuth,
		},
		{
			name: "should report usage for a stored not found status",
			err:  stored("http", statusOf(http.StatusNotFound)), want: ExitUsage,
		},
		{
			name: "should report unavailable for a stored server error",
			err:  stored("http", statusOf(http.StatusBadGateway)), want: ExitUnavailable,
		},
		{
			name: "should report usage for a stored bad request",
			err:  stored("http", statusOf(http.StatusBadRequest)), want: ExitUsage,
		},
		{
			name: "should report failed records for a stored failure of no known kind",
			err:  stored("", nil), want: ExitRecords,
		},
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
			err:  &onesie.ResponseError{Status: http.StatusOK, Err: errors.New("wrong shape")},
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
			err: &onesie.RetryAfterError{
				APIError:   *apiError(http.StatusTooManyRequests),
				RetryAfter: 120 * time.Second,
				Cap:        60 * time.Second,
			},
			want: ExitUnavailable,
		},
		{
			name: "should report unavailable for an unusable 200 body",
			err: &onesie.ResponseError{
				Status: http.StatusOK, Err: errors.New("invalid character 'n'"),
			},
			want: ExitUnavailable,
		},
		{
			name: "should report transport for a connection failure",
			err:  &onesie.ConnectionError{Err: errors.New("refused")}, want: ExitTransport,
		},
		{
			name: "should report transport for a timeout",
			err: &onesie.TimeoutError{
				ConnectionError: onesie.ConnectionError{Err: errors.New("deadline")},
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
			err:  &onesie.ValidationError{Message: "bad"}, want: ExitUsage,
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
		{name: "should classify an abstain as exit seven", err: &abstainError{}, want: ExitAbstain},
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
			err:  &onesie.ConnectionError{Err: syscall.EPIPE},
			want: ExitTransport,
		},
		{
			name: "should report the code a silent error carries",
			err:  &silentError{code: ExitAuth},
			want: ExitAuth,
		},
		{
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

	t.Run("should give each error the kind a record is written with and whether it is quiet", func(t *testing.T) {
		t.Parallel()

		pins := []struct {
			err   error
			kind  output.Kind
			quiet bool
		}{
			{err: &recordsError{}, kind: output.KindInput, quiet: true},
			{err: &stoppedError{cause: &input.LineError{Line: 2, Err: errors.New("not json")}}, kind: output.KindInput, quiet: true},
			{err: &rejectedError{}, kind: output.KindInput, quiet: true},
			{err: &abstainError{}, kind: output.KindInput, quiet: true},
			{err: &input.LineError{Line: 1, Err: errors.New("not json")}, kind: output.KindInput},
			{err: context.Canceled, kind: output.KindInput, quiet: true},
			{err: context.DeadlineExceeded, kind: output.KindTransport},
			{err: apiError(http.StatusUnauthorized), kind: output.KindHTTP},
			{err: apiError(http.StatusBadRequest), kind: output.KindHTTP},
			{err: &onesie.ResponseError{Status: http.StatusOK}, kind: output.KindResponse},
			{err: &onesie.ConnectionError{Err: errors.New("refused")}, kind: output.KindTransport},
			{err: &onesie.ConnectionError{Err: syscall.EPIPE}, kind: output.KindTransport, quiet: true},
			{err: &fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}, kind: output.KindInput, quiet: true},
			{err: &silentError{code: ExitAuth}, kind: output.KindInput, quiet: true},
			{err: errors.New("boom"), kind: output.KindInput},
			{
				err:  errors.Join(&storedFailure{failure: output.Failure{Kind: "input", Message: "bad"}}, context.Canceled),
				kind: output.KindInput, quiet: true,
			},
			{
				err: errors.Join(&onesie.APIError{Status: http.StatusBadGateway},
					&fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}),
				kind: output.KindHTTP, quiet: true,
			},
		}

		for _, pin := range pins {
			if got := classify(pin.err); got.kind != pin.kind || got.quiet != pin.quiet {
				t.Errorf("classify(%v) = kind %s, quiet %t, want kind %s, quiet %t",
					pin.err, got.kind, got.quiet, pin.kind, pin.quiet)
			}
		}
	})
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
		{name: "should stay quiet for an abstain", err: &abstainError{}},
		{name: "should stay quiet for a stream that failed records", err: &recordsError{}},
		{
			name: "should stay quiet when the consumer stopped reading",
			err:  &fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE},
		},
		{name: "should stay quiet for a record --stop-on-error stopped at", err: &stoppedError{cause: errors.New("bad")}},
		{name: "should stay quiet for a command that printed its whole result", err: &silentError{code: ExitAuth}},
		{name: "should stay quiet for an interrupt inside a wrapped error", err: fmt.Errorf("waiting: %w", context.Canceled)},
		{
			name: "should stay quiet for failed records joined to a failure closing the answers file",
			err:  errors.Join(&recordsError{}, errors.New("closing the answers file")),
		},
		{
			name: "should report a stored failure a resume stopped at",
			err:  &storedFailure{failure: output.Failure{Kind: "input", Message: "bad"}, record: 2},
			want: true,
		},
		{name: "should report a line onesie could not read", err: &input.LineError{Line: 1, Err: errors.New("x")}, want: true},
		{name: "should report a server error", err: &onesie.APIError{Status: http.StatusBadGateway}, want: true},
		{name: "should report a connection error", err: &onesie.ConnectionError{Err: errors.New("refused")}, want: true},
		{name: "should stay quiet for a connection that broke with EPIPE", err: &onesie.ConnectionError{Err: syscall.EPIPE}},
		{
			name: "should stay quiet for a stored failure joined to an interrupt",
			err:  errors.Join(&storedFailure{failure: output.Failure{Kind: "input", Message: "bad"}}, context.Canceled),
		},
		{
			name: "should stay quiet for a server error joined to a broken stdout",
			err: errors.Join(&onesie.APIError{Status: http.StatusBadGateway},
				&fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}),
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

			if got := aborting(&onesie.APIError{Status: tc.status}); got != tc.want {
				t.Errorf("aborting(%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestAuthFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "should match a refused key", err: &onesie.APIError{Status: http.StatusUnauthorized}, want: true},
		{name: "should match a forbidden key", err: &onesie.APIError{Status: http.StatusForbidden}, want: true},
		{
			name: "should match an account out of credits",
			err:  &onesie.APIError{Status: http.StatusPaymentRequired}, want: true,
		},
		{
			name: "should match a refusal wrapped in advice",
			err:  &advisedError{cause: &onesie.APIError{Status: http.StatusUnauthorized}}, want: true,
		},
		{name: "should not match a bad request", err: &onesie.APIError{Status: http.StatusBadRequest}},
		{name: "should not match a missing key", err: onesie.ErrNoAPIKey},
		{name: "should not match a record the mock does not answer", err: &uncoveredError{message: "x"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := authFailure(tc.err); got != tc.want {
				t.Errorf("authFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
