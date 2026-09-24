package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jev"
)

// Exit codes. A driver script branches on these, so they are part of the interface.
const (
	// ExitOK means the question was answered.
	ExitOK = 0
	// ExitRejected means -q ran and the policy did not accept the answer.
	ExitRejected = 1
	// ExitUsage means a usage or validation error, including a server 422.
	ExitUsage = 2
	// ExitAuth means authentication, permission or payment failed.
	ExitAuth = 3
	// ExitUnavailable means the server did not answer after retries.
	ExitUnavailable = 4
	// ExitTransport means a transport error or a timeout.
	ExitTransport = 5
	// ExitRecords means a stream finished with one or more failed records.
	ExitRecords = 6
	// ExitInterrupt means the run was interrupted by a signal.
	ExitInterrupt = 130
)

// Classify maps an error onto its exit code. The order of the checks matters: RetryAfterError
// embeds APIError, so the typed check has to precede the sentinels it would otherwise match, and
// silentError comes last so a real error joined to one still reports its own code.
func Classify(err error) int {
	if err == nil {
		return ExitOK
	}

	var records *recordsError
	if errors.As(err, &records) {
		return ExitRecords
	}

	var rejected *rejectedError
	if errors.As(err, &rejected) {
		return ExitRejected
	}

	var bad *input.LineError
	if errors.As(err, &bad) {
		return ExitUsage
	}

	if errors.Is(err, context.Canceled) {
		return ExitInterrupt
	}

	// A deadline reaching here unwrapped is still a timeout, and a timeout is transport shaped.
	// Without this it would fall through to the usage default.
	if errors.Is(err, context.DeadlineExceeded) {
		return ExitTransport
	}

	var overCap *jev.RetryAfterError
	if errors.As(err, &overCap) {
		return ExitUnavailable
	}

	// A 2xx body onesie cannot use is a server fault, not a usage error, and retrying it is
	// reasonable, which is what exit 4 means.
	var unusable *jev.ResponseError
	if errors.As(err, &unusable) {
		return ExitUnavailable
	}

	if errors.Is(err, jev.ErrAuthentication) || errors.Is(err, jev.ErrPermissionDenied) ||
		errors.Is(err, jev.ErrPaymentRequired) {
		return ExitAuth
	}

	// A credential file anyone else can reach is an authentication failure rather than a usage
	// one. Section 16.2 is explicit that this is a refusal and not a warning.
	var readable *creds.ReadableError
	if errors.As(err, &readable) {
		return ExitAuth
	}

	var keychain *creds.KeychainError
	if errors.As(err, &keychain) {
		return ExitAuth
	}

	var api *jev.APIError
	if errors.As(err, &api) {
		return classifyStatus(api.Status)
	}

	if errors.Is(err, jev.ErrConnection) || errors.Is(err, jev.ErrTimeout) {
		return ExitTransport
	}

	// After the transport checks, so a socket write that failed with EPIPE is still reported as
	// the transport failure it is. What reaches here is onesie's own stdout, and section 12 has no
	// code meaning the consumer stopped reading.
	if engine.BrokenPipe(err) {
		return ExitOK
	}

	var silent *silentError
	if errors.As(err, &silent) {
		return silent.code
	}

	return ExitUsage
}

func worthReporting(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}

	if engine.BrokenPipe(err) {
		// The consumer closed the pipe onesie was writing to, and a line about it would go to a
		// stderr the same consumer is often reading.
		return false
	}

	var rejected *rejectedError
	if errors.As(err, &rejected) {
		// A rejection is carried by the exit code alone. Under -q nothing is printed at all, and
		// a false assertion has already printed the record §17.4 asks for.
		return false
	}

	var silent *silentError
	if errors.As(err, &silent) {
		// The command already wrote the whole result to stdout, so a stderr line would repeat it
		// with nothing added. auth status printing source: none and exiting 3 is the case.
		//
		// Unlike Classify, this cannot be reordered to spare a joined error: every branch here
		// returns false, so there is no positive one to put first. A silentError joined to a real
		// error would therefore lose the real message, which is why the type is only ever
		// returned on its own.
		return false
	}

	var records *recordsError

	// The exit code already says a record failed, and the per record lines on stdout carry the
	// detail.
	return !errors.As(err, &records)
}

type silentError struct {
	code int
}

func (e *silentError) Error() string {
	return fmt.Sprintf("exit %d with nothing left to report", e.code)
}

func classifyStatus(status int) int {
	switch {
	case status == http.StatusRequestTimeout:
		return ExitTransport
	case status == http.StatusTooManyRequests, status >= 500:
		return ExitUnavailable
	default:
		return ExitUsage
	}
}

type sourceError struct {
	cause  error
	failed int
}

func (e *sourceError) Error() string {
	if e.failed == 0 {
		return e.cause.Error()
	}

	return fmt.Sprintf("%v, after %s failed", e.cause, plural(e.failed, "record"))
}

func (e *sourceError) Unwrap() error {
	return e.cause
}

type abortError struct {
	cause error
}

func (e *abortError) Error() string {
	return e.cause.Error()
}

func (e *abortError) Unwrap() error {
	return e.cause
}

type recordsError struct{}

func (*recordsError) Error() string {
	return "one or more records failed"
}

type rejectedError struct{}

func (*rejectedError) Error() string {
	return "policy did not accept the answer"
}
