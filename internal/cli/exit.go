package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
)

// Exit codes. A driver script branches on these, so they are part of the interface.
const (
	// ExitOK means the question was answered.
	ExitOK = 0
	// ExitRejected means an assertion was false, or -q ran and the policy did not accept the answer.
	ExitRejected = 1
	// ExitUsage means a usage or validation error, including a server 422.
	ExitUsage = 2
	// ExitAuth means authentication, permission or payment failed.
	ExitAuth = 3
	// ExitUnavailable means the server did not answer after retries.
	ExitUnavailable = 4
	// ExitTransport means a transport error or a timeout.
	ExitTransport = 5
	// ExitRecords means a stream finished with one or more failed records, or --stop-on-error stopped
	// it at a record that never became a request.
	ExitRecords = 6
	// ExitAbstain means the assertion did not hold and the abstain expression did, from --abstain-if
	// or a file's abstain_if, so the gate could not decide.
	ExitAbstain = 7
	// ExitInterrupt means the run was interrupted by a signal.
	ExitInterrupt = 130
)

var rootExitCodes = []exitCodeLine{
	{ExitOK, "answered"},
	{ExitRejected, "a false --assert, or under -q alone the policy did not accept the answer"},
	{ExitUsage, "usage or validation error, including a missing key, or a plain http base URL to another " +
		"host without --allow-http"},
	{ExitAuth, "the key was refused, the account is out of credits, the keychain failed, or profiles.json or " +
		"credentials.json is exposed"},
	{ExitUnavailable, "the server did not answer after retries, or it answered 200 with a response onesie " +
		"could not use, which is never retried"},
	{ExitTransport, "transport error or timeout"},
	{ExitRecords, "a stream finished with one or more failed records, or --stop-on-error stopped it at " +
		"a record that never became a request"},
	{ExitAbstain, "the gate could not decide: the assertion failed and --abstain-if held"},
	{ExitInterrupt, "interrupted"},
}

var calibrateExitCodes = []exitCodeLine{
	{ExitOK, "every record was answered and every --require held"},
	{ExitRejected, "a --require did not hold"},
	{ExitUsage, "usage or validation error, including a bad label or record"},
	{ExitAuth, "the key was refused, the account is out of credits, the keychain failed, or profiles.json or " +
		"credentials.json is exposed. No report prints"},
	{ExitRecords, "the report holds one or more failed records, which wins over 1"},
	{ExitInterrupt, "interrupted. No report prints"},
}

func exitCodesSection(codes []exitCodeLine) string {
	var out strings.Builder

	out.WriteString("Exit codes:\n")

	for _, line := range codes {
		fmt.Fprintf(&out, "  %-5d%s\n", line.code, line.meaning)
	}

	return strings.TrimSuffix(out.String(), "\n")
}

type exitCodeLine struct {
	code    int
	meaning string
}

// Classify maps an error onto its exit code.
func Classify(err error) int {
	if err == nil {
		return ExitOK
	}

	return classify(err).code
}

func worthReporting(err error) bool {
	return !classify(err).quiet
}

func classify(err error) failure {
	matched := firstMatch(err)
	matched.quiet = matched.quiet || canceledOrBrokenPipe(err)

	return matched
}

func canceledOrBrokenPipe(err error) bool {
	return errors.Is(err, context.Canceled) || engine.BrokenPipe(err)
}

func firstMatch(err error) failure {
	// Order matters. RetryAfterError embeds APIError, so the typed check precedes the sentinels it
	// would otherwise match, and silentError comes last so a real error joined to one keeps its code.

	var records *recordsError
	if errors.As(err, &records) {
		// The exit code already says a record failed, and the per record lines on stdout carry the
		// detail.
		return failure{kind: output.KindInput, code: ExitRecords, quiet: true}
	}

	var stored *storedFailure
	if errors.As(err, &stored) {
		return failure{kind: output.KindInput, code: stored.freshRunCode()}
	}

	var rejected *rejectedError
	if errors.As(err, &rejected) {
		// A rejection is carried by the exit code alone. Under -q nothing is printed at all, and a
		// false assertion has already printed the record.
		return failure{kind: output.KindInput, code: ExitRejected, quiet: true}
	}

	var abstained *abstainError
	if errors.As(err, &abstained) {
		return failure{kind: output.KindInput, code: ExitAbstain, quiet: true}
	}

	var stopped *stoppedError
	if errors.As(err, &stopped) {
		return failure{kind: output.KindInput, code: ExitRecords, quiet: true}
	}

	var bad *input.LineError
	if errors.As(err, &bad) {
		return failure{kind: output.KindInput, code: ExitUsage}
	}

	if errors.Is(err, context.Canceled) {
		return failure{kind: output.KindInput, code: ExitInterrupt}
	}

	// A deadline reaching here unwrapped is still a timeout, and a timeout is transport shaped.
	// Without this it would fall through to the usage default.
	if errors.Is(err, context.DeadlineExceeded) {
		return failure{kind: output.KindTransport, code: ExitTransport}
	}

	var overCap *onesie.RetryAfterError
	if errors.As(err, &overCap) {
		status := overCap.Status

		return failure{kind: output.KindHTTP, status: &status, code: ExitUnavailable}
	}

	// A 2xx body onesie cannot use is a server fault, not a usage error, so it exits 4 and is never
	// retried. Its kind is response, since http would name a status worth retrying and transport a
	// connection blip that never happened.
	var unusable *onesie.ResponseError
	if errors.As(err, &unusable) {
		status := unusable.Status

		return failure{kind: output.KindResponse, status: &status, code: ExitUnavailable}
	}

	var cannotUse *answer.UnusableError
	if errors.As(err, &cannotUse) {
		status := http.StatusOK

		return failure{kind: output.KindResponse, status: &status, code: ExitUnavailable}
	}

	var api *onesie.APIError

	answered := errors.As(err, &api)

	if answered && (errors.Is(err, onesie.ErrAuthentication) || errors.Is(err, onesie.ErrPermissionDenied) ||
		errors.Is(err, onesie.ErrPaymentRequired)) {
		status := api.Status

		return failure{kind: output.KindHTTP, status: &status, code: ExitAuth}
	}

	// A key file anyone else can reach is an authentication failure rather than a usage one.
	var readable *creds.ReadableError
	if errors.As(err, &readable) {
		return failure{kind: output.KindInput, code: ExitAuth}
	}

	var keychain *creds.KeychainError
	if errors.As(err, &keychain) {
		return failure{kind: output.KindInput, code: ExitAuth}
	}

	if answered {
		status := api.Status

		return failure{kind: output.KindHTTP, status: &status, code: classifyStatus(api.Status)}
	}

	// A connection that broke with EPIPE exits as the transport failure it is, and stays quiet as
	// every broken pipe does.
	if errors.Is(err, onesie.ErrConnection) || errors.Is(err, onesie.ErrTimeout) {
		return failure{kind: output.KindTransport, code: ExitTransport}
	}

	// After the transport checks, so a socket write that failed with EPIPE keeps the transport exit
	// code. What reaches here is onesie's own stdout, and no exit code means the consumer stopped
	// reading. A line about it would go to a stderr the same consumer is often reading.
	if engine.BrokenPipe(err) {
		return failure{kind: output.KindInput, code: ExitOK}
	}

	var silent *silentError
	if errors.As(err, &silent) {
		// The command already wrote the whole result to stdout, so a stderr line would repeat it.
		return failure{kind: output.KindInput, code: silent.code, quiet: true}
	}

	// No status arrived and the network did not fail, so the record never became a request, as with
	// a line onesie could not read. Calling it transport would blame the network, and exit 5 where a
	// fresh run exits 2.
	return failure{kind: output.KindInput, code: ExitUsage}
}

type failure struct {
	kind   output.Kind
	status *int
	code   int
	quiet  bool
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
	case output.KindInput:
		return ExitRecords
	case output.KindResponse:
		return ExitUnavailable
	case output.KindTransport:
		return ExitTransport
	case output.KindHTTP:
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

type stoppedError struct {
	cause error
}

func (e *stoppedError) Error() string {
	return e.cause.Error()
}

func (e *stoppedError) Unwrap() error {
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
