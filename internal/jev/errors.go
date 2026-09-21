package jev

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	requestIDHeader = "X-TypeSafe-Request-Id"
	maxBodyInError  = 200
)

// Sentinels for errors.Is. An APIError reports itself as the one matching its status.
var (
	// ErrBadRequest matches an APIError with status 400.
	ErrBadRequest = errors.New("bad request")
	// ErrAuthentication matches an APIError with status 401.
	ErrAuthentication = errors.New("authentication failed")
	// ErrPermissionDenied matches an APIError with status 403.
	ErrPermissionDenied = errors.New("permission denied")
	// ErrNotFound matches an APIError with status 404.
	ErrNotFound = errors.New("not found")
	// ErrUnprocessableEntity matches an APIError with status 422.
	ErrUnprocessableEntity = errors.New("unprocessable entity")
	// ErrRateLimit matches an APIError with status 429.
	ErrRateLimit = errors.New("rate limited")
	// ErrServer matches an APIError with any status from 500 to 599.
	ErrServer = errors.New("server error")
	// ErrConnection matches a ConnectionError, including a TimeoutError.
	ErrConnection = errors.New("connection error")
	// ErrTimeout matches a TimeoutError.
	ErrTimeout = errors.New("timed out")
	// ErrResponse matches a ResponseError.
	ErrResponse = errors.New("unusable response")
	// ErrValidation matches a ValidationError.
	ErrValidation = errors.New("invalid request")
)

// newAPIError builds the error for a non 2xx response. now resolves an HTTP date in Retry-After.
func newAPIError(status int, header http.Header, body []byte, now time.Time) *APIError {
	parsed := decodeBody(body)

	retryAfter, _ := parseRetryAfter(header, now)

	return &APIError{
		Status:     status,
		RequestID:  header.Get(requestIDHeader),
		Header:     header,
		Body:       parsed,
		RetryAfter: retryAfter,
		message:    describe(status, parsed),
	}
}

// APIError is a non 2xx response from the API. Match it with errors.Is against a status sentinel,
// or recover it with errors.As to read the status, body and request ID.
type APIError struct {
	Status     int
	RequestID  string
	Header     http.Header
	Body       any
	RetryAfter time.Duration

	message string
}

// Error returns the status and the message extracted from the response body.
func (e *APIError) Error() string {
	return e.message
}

// Is reports whether this status matches one of the package status sentinels.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrBadRequest:
		return e.Status == http.StatusBadRequest
	case ErrAuthentication:
		return e.Status == http.StatusUnauthorized
	case ErrPermissionDenied:
		return e.Status == http.StatusForbidden
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrUnprocessableEntity:
		return e.Status == http.StatusUnprocessableEntity
	case ErrRateLimit:
		return e.Status == http.StatusTooManyRequests
	case ErrServer:
		return e.Status >= 500 && e.Status <= 599
	default:
		return false
	}
}

func describe(status int, body any) string {
	if detail := extractMessage(body); detail != "" {
		return fmt.Sprintf("jev: %d %s", status, truncate(detail))
	}

	if body == nil {
		return fmt.Sprintf("jev: %d status code, no body", status)
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Sprintf("jev: %d status code, unreadable body", status)
	}

	return fmt.Sprintf("jev: %d %s", status, truncate(string(encoded)))
}

// truncate runs after extraction, not instead of it, so a hostile body cannot reach a log line at
// full length by arriving as a plain string.
func truncate(raw string) string {
	if len(raw) <= maxBodyInError {
		return raw
	}

	return raw[:maxBodyInError] + "..."
}

// extractMessage walks the shapes the API uses for error bodies, most specific first.
func extractMessage(body any) string {
	if text, ok := body.(string); ok {
		return text
	}

	fields, ok := body.(map[string]any)
	if !ok {
		return ""
	}

	if text, ok := fields["error"].(string); ok {
		return text
	}

	if nested, ok := fields["error"].(map[string]any); ok {
		if text, ok := nested["message"].(string); ok {
			return text
		}
	}

	if text, ok := fields["message"].(string); ok {
		return text
	}

	if text, ok := fields["detail"].(string); ok {
		return text
	}

	if nested, ok := fields["detail"].(map[string]any); ok {
		if text, ok := nested["message"].(string); ok {
			return text
		}
	}

	if list, ok := fields["detail"].([]any); ok {
		return describeValidation(list)
	}

	return ""
}

func describeValidation(entries []any) string {
	parts := make([]string, 0, len(entries))

	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		msg, ok := fields["msg"].(string)
		if !ok {
			continue
		}

		path := validationPath(fields["loc"])
		if path == "" {
			parts = append(parts, msg)

			continue
		}

		parts = append(parts, path+": "+msg)
	}

	return strings.Join(parts, ", ")
}

// validationPath joins a FastAPI style location, dropping the leading "body" segment.
func validationPath(loc any) string {
	list, ok := loc.([]any)
	if !ok {
		return ""
	}

	segments := make([]string, 0, len(list))

	for _, item := range list {
		text := fmt.Sprint(item)
		if text == "body" {
			continue
		}

		segments = append(segments, text)
	}

	return strings.Join(segments, ".")
}

// decodeBody is lenient because proxies do not always set a JSON content type.
func decodeBody(body []byte) any {
	if len(body) == 0 {
		return nil
	}

	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return string(body)
	}

	return parsed
}

// RetryAfterError reports a server requested delay jev refused to wait out, because spending the
// remaining retries on a known answer is worse than failing now.
type RetryAfterError struct {
	APIError

	RetryAfter time.Duration
	Cap        time.Duration
}

// Error names the delay the server asked for and the cap that rejected it.
func (e *RetryAfterError) Error() string {
	return fmt.Sprintf(
		"jev: status %d, the server asked to retry after %s, above the %s cap",
		e.Status, e.RetryAfter, e.Cap,
	)
}

// Unwrap returns the embedded APIError, so errors.As and errors.Is reach it.
func (e *RetryAfterError) Unwrap() error {
	return &e.APIError
}

// ConnectionError is a transport failure, including a body that stopped arriving.
type ConnectionError struct {
	Err error
}

// Error describes the transport failure.
func (e *ConnectionError) Error() string {
	if e.Err == nil {
		return "jev: connection error"
	}

	return "jev: connection error: " + e.Err.Error()
}

// Unwrap returns the underlying transport error.
func (e *ConnectionError) Unwrap() error {
	return e.Err
}

// Is matches ErrConnection.
func (e *ConnectionError) Is(target error) bool {
	return target == ErrConnection
}

// TimeoutError is a ConnectionError whose cause was the attempt deadline.
//
// Unwrap reaches the embedded ConnectionError and then the transport error, which itself wraps
// context.DeadlineExceeded. A caller testing errors.Is against context.DeadlineExceeded to detect
// its own deadline will therefore match an attempt timeout too.
type TimeoutError struct {
	ConnectionError

	Timeout time.Duration
}

// Error reports the deadline that fired.
func (e *TimeoutError) Error() string {
	return fmt.Sprintf("jev: request timed out after %s", e.Timeout)
}

// Unwrap returns the embedded ConnectionError, so errors.As reaches it.
func (e *TimeoutError) Unwrap() error {
	return &e.ConnectionError
}

// Is matches both ErrTimeout and ErrConnection.
func (e *TimeoutError) Is(target error) bool {
	return target == ErrConnection || target == ErrTimeout
}

// ResponseError is a 2xx response this client could not use, such as a body that is not the JSON
// the endpoint documents, or a response missing an answer that was asked for.
type ResponseError struct {
	Status int
	Body   []byte
	Err    error
}

// Error describes what could not be read.
func (e *ResponseError) Error() string {
	return fmt.Sprintf("jev: %d response could not be used: %v", e.Status, e.Err)
}

// Unwrap returns the underlying decoding error.
func (e *ResponseError) Unwrap() error {
	return e.Err
}

// Is matches ErrResponse.
func (e *ResponseError) Is(target error) bool {
	return target == ErrResponse
}

// ValidationError is a request this package rejected before sending it. Question names the
// offending question when one is to blame.
type ValidationError struct {
	Question string
	Message  string
}

// Error describes why the request was rejected.
func (e *ValidationError) Error() string {
	return "jev: " + e.Message
}

// Is matches ErrValidation.
func (e *ValidationError) Is(target error) bool {
	return target == ErrValidation
}

// AnswerError reports an answer that is absent or of another type.
type AnswerError struct {
	Name    string
	Want    string
	Got     string
	Missing bool
}

// Error describes whether the answer was absent or of the wrong type.
func (e *AnswerError) Error() string {
	if e.Missing {
		return fmt.Sprintf("jev: no answer named %q", e.Name)
	}

	return fmt.Sprintf("jev: answer %q is a %s, not a %s", e.Name, e.Got, e.Want)
}
