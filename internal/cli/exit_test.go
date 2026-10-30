package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	apiError := func(status int) *onesie.APIError {
		return &onesie.APIError{Status: status}
	}

	stored := func(kind output.Kind, status *int) error {
		return &runner.StoredFailure{Failure: output.Failure{Kind: kind, Status: status, Message: "onesie: stored"}}
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
			err:  &abortError{cause: &runner.HaltError{Err: errors.New("onesie: --mock has no line 3")}}, want: ExitUsage,
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
			err:  &runner.SourceError{Err: &input.LineError{Line: 1, Err: errors.New("the header is not valid csv")}},
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
			name: "should report auth for a stored auth failure whatever its status",
			err:  stored("auth", statusOf(http.StatusBadRequest)), want: ExitAuth,
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
			{err: apiError(http.StatusUnauthorized), kind: output.KindAuth},
			{err: apiError(http.StatusBadRequest), kind: output.KindHTTP},
			{err: &onesie.ResponseError{Status: http.StatusOK}, kind: output.KindResponse},
			{err: &onesie.ConnectionError{Err: errors.New("refused")}, kind: output.KindTransport},
			{err: &onesie.ConnectionError{Err: syscall.EPIPE}, kind: output.KindTransport, quiet: true},
			{err: &fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}, kind: output.KindInput, quiet: true},
			{err: &silentError{code: ExitAuth}, kind: output.KindInput, quiet: true},
			{err: &shownError{cause: apiError(http.StatusUnauthorized)}, kind: output.KindAuth, quiet: true},
			{err: &advisedError{cause: apiError(http.StatusPaymentRequired)}, kind: output.KindAuth},
			{err: errors.New("boom"), kind: output.KindInput},
			{err: &runner.StoredFailure{Failure: output.Failure{Kind: "input", Message: "bad"}}, kind: output.KindInput},
		}

		for _, pin := range pins {
			if got := classify(pin.err); got.kind != pin.kind || got.quiet != pin.quiet {
				t.Errorf("classify(%v) = kind %s, quiet %t, want kind %s, quiet %t",
					pin.err, got.kind, got.quiet, pin.kind, pin.quiet)
			}
		}
	})

	t.Run("should classify a halt as input with exit 2", func(t *testing.T) {
		t.Parallel()

		halt := &runner.HaltError{Err: errors.New("onesie: --mock has no line 3")}

		if got := classify(halt); got.kind != output.KindInput || got.code != ExitUsage || got.quiet {
			t.Errorf("classify(%v) = %+v, want kind input, exit %d and not quiet", halt, got, ExitUsage)
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
			name: "should report a stored failure a resume stopped at",
			err:  &runner.StoredFailure{Failure: output.Failure{Kind: "input", Message: "bad"}, Record: 2},
			want: true,
		},
		{name: "should report a line onesie could not read", err: &input.LineError{Line: 1, Err: errors.New("x")}, want: true},
		{name: "should report a server error", err: &onesie.APIError{Status: http.StatusBadGateway}, want: true},
		{name: "should report a connection error", err: &onesie.ConnectionError{Err: errors.New("refused")}, want: true},
		{name: "should stay quiet for a connection that broke with EPIPE", err: &onesie.ConnectionError{Err: syscall.EPIPE}},
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

func TestStoredRunCode(t *testing.T) {
	t.Parallel()

	apiError := func(status int) error {
		return &onesie.APIError{Status: status}
	}

	tests := []struct {
		name  string
		cause error
	}{
		{name: "should agree with a fresh run on a line onesie could not read", cause: &input.LineError{
			Line: 1, Err: errors.New("not valid JSON"),
		}},
		{name: "should agree with a fresh run on an unusable response", cause: &onesie.ResponseError{
			Status: http.StatusOK,
		}},
		{name: "should agree with a fresh run on a bad request", cause: apiError(http.StatusBadRequest)},
		{name: "should agree with a fresh run on an unauthorized status", cause: apiError(http.StatusUnauthorized)},
		{name: "should agree with a fresh run on a payment required status", cause: &advisedError{
			cause: apiError(http.StatusPaymentRequired), message: "402. Add credits",
		}},
		{name: "should agree with a fresh run on a forbidden status", cause: apiError(http.StatusForbidden)},
		{
			name: "should agree with a fresh run on a cloudflare 400 that refuses the token",
			cause: onesie.NewAPIError(onesie.Cloudflare(), "", onesie.Needs{}, http.StatusBadRequest,
				[]byte(`{"success":false,"errors":[{"code":9106,"message":"Authentication failed (status: 400)"}]}`)),
		},
		{name: "should agree with a fresh run on a not found status", cause: apiError(http.StatusNotFound)},
		{name: "should agree with a fresh run on a request timeout status", cause: apiError(http.StatusRequestTimeout)},
		{name: "should agree with a fresh run on a rate limit", cause: apiError(http.StatusTooManyRequests)},
		{name: "should agree with a fresh run on a server error", cause: apiError(http.StatusBadGateway)},
		{name: "should agree with a fresh run on a retry after above the cap", cause: &onesie.RetryAfterError{
			APIError: onesie.APIError{Status: http.StatusServiceUnavailable},
		}},
		{name: "should agree with a fresh run on a connection error", cause: &onesie.ConnectionError{
			Err: errors.New("connection refused"),
		}},
		{name: "should agree with a fresh run on a request timeout", cause: &onesie.TimeoutError{}},
		{name: "should agree with a fresh run on a deadline", cause: context.DeadlineExceeded},
		{name: "should agree with a fresh run on a request onesie refused to send", cause: &onesie.ValidationError{
			Message: "question too long",
		}},
		{name: "should agree with a fresh run on an unrecognised plain error", cause: errors.New("boom")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stored := &runner.StoredFailure{Failure: *runner.Describe(tc.cause, text)}
			stopped := streamResult(engine.Result{Aborted: true, Cause: tc.cause}, 0, 0)

			if got, want := storedRunCode(stored), Classify(stopped); got != want {
				t.Errorf("stored %+v exits %d, want %d as a fresh run under --stop-on-error does", stored.Failure, got, want)
			}

			if strings.HasPrefix(stored.Failure.Message, "onesie: ") {
				t.Errorf("message = %q, want no onesie prefix, as a line error has none", stored.Failure.Message)
			}
		})
	}
}
