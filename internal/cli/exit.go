package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/frodi-karlsson/jev-cli/internal/creds"
	"github.com/frodi-karlsson/jev-cli/internal/engine"
	"github.com/frodi-karlsson/jev-cli/internal/input"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

// Exit codes. A driver script branches on these, so they are part of the interface.
const (
	// ExitOK means the question was answered.
	ExitOK = 0
	// ExitRejected means -q ran and the policy did not accept the answer.
	ExitRejected = 1
	// ExitUsage means a usage or validation error, including a server 422.
	ExitUsage = 2
	// ExitAuth means authentication or permission failed.
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

	// A 2xx body jev cannot use is a server fault, not a usage error, and retrying it is
	// reasonable, which is what exit 4 means.
	var unusable *jev.ResponseError
	if errors.As(err, &unusable) {
		return ExitUnavailable
	}

	if errors.Is(err, jev.ErrAuthentication) || errors.Is(err, jev.ErrPermissionDenied) {
		return ExitAuth
	}

	// A credential file anyone else can reach is an authentication failure rather than a usage
	// one. Section 16.2 is explicit that this is a refusal and not a warning.
	var readable *creds.ReadableError
	if errors.As(err, &readable) {
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
	// the transport failure it is. What reaches here is jev's own stdout, and section 12 has no
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
