package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
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
	// ExitAbstain means the assertion did not hold and the abstain expression did, from --abstain-if
	// or a file's abstain_if, so the gate could not decide.
	ExitAbstain = 7
	// ExitInterrupt means the run was interrupted by a signal.
	ExitInterrupt = 130
)

// Classify maps an error onto its exit code.
func Classify(err error) int {
	if err == nil {
		return ExitOK
	}

	// Order matters. RetryAfterError embeds APIError, so the typed check precedes the sentinels it
	// would otherwise match, and silentError comes last so a real error joined to one keeps its code.

	var records *recordsError
	if errors.As(err, &records) {
		return ExitRecords
	}

	var stored *storedFailure
	if errors.As(err, &stored) {
		return stored.freshRunCode()
	}

	var rejected *rejectedError
	if errors.As(err, &rejected) {
		return ExitRejected
	}

	var abstained *abstainError
	if errors.As(err, &abstained) {
		return ExitAbstain
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

	var abstained *abstainError
	if errors.As(err, &abstained) {
		// Carried by the exit code alone, like a rejection.
		return false
	}

	var silent *silentError
	if errors.As(err, &silent) {
		// The command already wrote the whole result to stdout, so a stderr line would repeat it.
		//
		// Every branch here returns false, so a silentError joined to a real error would hide the
		// real message. That is why the type is only ever returned on its own.
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

type storedFailure struct {
	failure output.Failure
	record  int
}

func (e *storedFailure) Error() string {
	return fmt.Sprintf("%s. The stored failure for record %d is followed by more lines, so a run without "+
		"--stop-on-error wrote it. Pass --id or drop --stop-on-error to carry on",
		strings.TrimSuffix(e.failure.Message, "."), e.record)
}

func (e *storedFailure) freshRunCode() int {
	switch e.failure.Kind {
	case "input":
		return ExitUsage
	case "response":
		return ExitUnavailable
	case "transport":
		return ExitTransport
	case "http":
		return storedStatus(e.failure.Status)
	default:
		return ExitRecords
	}
}

func storedStatus(status *int) int {
	if status == nil {
		return ExitRecords
	}

	switch *status {
	case http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden:
		return ExitAuth
	default:
		return classifyStatus(*status)
	}
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

type abstainError struct{}

func (*abstainError) Error() string {
	return "the gate could not decide"
}
