package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// New builds a client. Resolution order is an explicit option, then the environment, then a
// default. Blank and whitespace only values are ignored at every level.
func New(opts ...Option) (*Client, error) {
	c := &Client{
		httpClient:       &http.Client{},
		logger:           slog.New(slog.DiscardHandler),
		retry:            DefaultRetryPolicy(),
		attemptTimeout:   DefaultAttemptTimeout,
		maxResponseBytes: DefaultMaxResponseBytes,
		header:           http.Header{},
		userAgent:        "jev-cli",
		lookupEnv:        os.LookupEnv,
		clock:            systemClock{},
		random:           rand.Float64,
		observe:          func(Attempt) {},
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	c.apiKey = orEnv(c.apiKey, c.lookupEnv, EnvAPIKey)
	c.baseURL = strings.TrimRight(orDefault(orEnv(c.baseURL, c.lookupEnv, EnvBaseURL), DefaultBaseURL), "/")
	c.defaultModel = orDefault(orEnv(c.defaultModel, c.lookupEnv, EnvDefaultModel), DefaultModel)

	// Computed once here rather than behind a package level singleton, which AGENTS.md bans.
	c.runtime = fmt.Sprintf("go/%s %s/%s", runtime.Version(), runtime.GOOS, runtime.GOARCH)

	if c.apiKey == "" {
		return nil, &ValidationError{Message: "no API key. Pass --api-key or set " + EnvAPIKey}
	}

	if err := validateBaseURL(c.baseURL); err != nil {
		return nil, err
	}

	if err := c.retry.validate(); err != nil {
		return nil, err
	}

	return c, nil
}

// Client talks to the TypeSafe System One API. Build one with New.
//
// A Client is safe for concurrent use by multiple goroutines. Any Clock, random source or
// RetryStatus predicate passed to New must be safe for concurrent use too.
type Client struct {
	apiKey           string
	baseURL          string
	defaultModel     string
	userAgent        string
	runtime          string
	httpClient       *http.Client
	logger           *slog.Logger
	retry            RetryPolicy
	attemptTimeout   time.Duration
	totalTimeout     time.Duration
	maxResponseBytes int64
	header           http.Header
	lookupEnv        func(string) (string, bool)
	clock            Clock
	random           func() float64
	requests         atomic.Uint64
	observe          func(Attempt)
}

// RetryPolicy returns a copy, so a caller can modify one field and pass it to WithRequestRetry.
func (c *Client) RetryPolicy() RetryPolicy {
	return c.retry
}

// AttemptTimeout returns the per attempt deadline, not a budget for the whole call.
func (c *Client) AttemptTimeout() time.Duration {
	return c.attemptTimeout
}

const (
	systemOnePath = "/v1/systemone"
	modelsPath    = "/v1/models"

	retryCountHeader = "X-TypeSafe-Retry-Count"
)

// Request is one evaluation. All questions see the same state and are answered independently.
type Request struct {
	State     any
	Questions Questions
	Model     string
}

// SystemOne answers named questions about a state. Every question is evaluated in parallel and in
// isolation, so batching is cheaper than one call per question.
func (c *Client) SystemOne(ctx context.Context, req Request, opts ...RequestOption) (*Result, error) {
	if err := ValidateQuestions(req.Questions); err != nil {
		return nil, err
	}

	model := req.Model
	if model == "" {
		model = c.defaultModel
	}

	body := struct {
		State     any       `json:"state"`
		Model     string    `json:"model"`
		Questions Questions `json:"questions"`
	}{State: req.State, Model: model, Questions: req.Questions}

	result := &Result{}

	res, err := c.do(ctx, http.MethodPost, systemOnePath, body, result, opts...)
	if err != nil {
		return nil, err
	}

	result.RequestID = res.header.Get(requestIDHeader)
	result.Header = res.header

	// The dropped TypeScript generics guaranteed this at compile time. Checking it here recovers
	// most of what they gave.
	for _, named := range req.Questions {
		if _, ok := result.Answers[named.ID]; !ok {
			return nil, &ResponseError{
				Status: res.status,
				Body:   res.body,
				Err:    fmt.Errorf("no answer for question %q", named.ID),
			}
		}
	}

	return result, nil
}

// SystemOneRaw sends a prepared request body and returns the response body unchanged. It runs the
// same retry loop as SystemOne and performs no validation or normalization. The request body is
// compacted on the way out, since encoding/json compacts whatever a Marshaler returns.
func (c *Client) SystemOneRaw(
	ctx context.Context,
	body json.RawMessage,
	opts ...RequestOption,
) (json.RawMessage, error) {
	res, err := c.do(ctx, http.MethodPost, systemOnePath, body, nil, opts...)
	if err != nil {
		return nil, err
	}

	return res.body, nil
}

// ListModels reports the model names this account may send.
func (c *Client) ListModels(ctx context.Context, opts ...RequestOption) ([]ModelCard, error) {
	var wire struct {
		Models []ModelCard `json:"models"`
	}

	res, err := c.do(ctx, http.MethodGet, modelsPath, nil, &wire, opts...)
	if err != nil {
		return nil, err
	}

	if wire.Models == nil {
		return nil, &ResponseError{
			Status: res.status,
			Body:   res.body,
			Err:    errors.New("expected a models list"),
		}
	}

	return wire.Models, nil
}

// ModelCard describes one model or alias.
type ModelCard struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ReleaseDate string `json:"release_date"`
}

// Attempt describes one HTTP round trip. An observer sees every attempt, retries included.
type Attempt struct {
	Index    int
	Status   int
	Err      error
	Duration time.Duration
}

// do runs one logical request, retrying per the policy, and decodes a 2xx body into out.
func (c *Client) do(
	ctx context.Context,
	method, path string,
	body, out any,
	opts ...RequestOption,
) (*rawResponse, error) {
	cfg := requestConfig{
		attemptTimeout: c.attemptTimeout,
		totalTimeout:   c.totalTimeout,
		retry:          c.retry,
		header:         c.header.Clone(),
	}

	for _, opt := range opts {
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	if cfg.totalTimeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, cfg.totalTimeout)
		defer cancel()
	}

	var payload []byte

	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("jev: encoding request: %w", err)
		}

		payload = encoded
	}

	// Numbered so concurrent requests, and the attempts within one, can be told apart in the logs.
	tag := fmt.Sprintf("#%d %s %s", c.requests.Add(1), method, path)
	url := c.baseURL + path

	for attempt := 0; ; attempt++ {
		retriesLeft := cfg.retry.MaxRetries - attempt

		res, err := c.attempt(ctx, tag, attempt, method, url, payload, cfg)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("jev: %s: %w", tag, ctxErr)
			}

			if retriesLeft <= 0 || !retryableError(err, cfg.retry) {
				return nil, err
			}

			if waitErr := c.backOff(ctx, tag, attempt, retriesLeft, err.Error(), nil, cfg); waitErr != nil {
				return nil, waitErr
			}

			continue
		}

		if res.status >= 200 && res.status < 300 {
			return res, decodeInto(res, out)
		}

		apiErr := newAPIError(res.status, res.header, res.body, c.clock.Now())

		if retriesLeft <= 0 || !cfg.retry.RetryStatus(res.status) {
			return nil, apiErr
		}

		// A server asking for longer than the cap has already told us the answer. Spending the
		// remaining retries on jev's own backoff would just arrive early and fail again.
		if delay, tooLong := retryAfterTooLong(res.header, cfg.retry, c.clock.Now()); tooLong {
			return nil, &RetryAfterError{
				APIError:   *apiErr,
				RetryAfter: delay,
				Cap:        cfg.retry.MaxRetryAfter,
			}
		}

		reason := strconv.Itoa(res.status)
		if waitErr := c.backOff(ctx, tag, attempt, retriesLeft, reason, res.header, cfg); waitErr != nil {
			return nil, waitErr
		}
	}
}

// attempt is one HTTP round trip. The body is fully read under the attempt deadline, so a slow
// body cannot outlive the timeout.
func (c *Client) attempt(
	ctx context.Context,
	tag string,
	attempt int,
	method, url string,
	payload []byte,
	cfg requestConfig,
) (*rawResponse, error) {
	actx, cancel := context.WithTimeout(ctx, cfg.attemptTimeout)
	defer cancel()

	var reader io.Reader
	if payload != nil {
		// A fresh reader per attempt, so a retry can send the body again.
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(actx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("jev: building request: %w", err)
	}

	c.setHeaders(req, cfg, attempt, payload != nil)

	if c.logger.Enabled(actx, slog.LevelDebug) {
		c.logger.DebugContext(actx, "jev request",
			"tag", tag,
			"url", url,
			"headers", redactedHeader(req.Header),
			"body", string(payload),
		)
	}

	started := c.clock.Now()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		failure := classify(ctx, actx, err, cfg.attemptTimeout)
		c.observe(Attempt{Index: attempt, Err: failure, Duration: c.clock.Now().Sub(started)})

		return nil, failure
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.logger.DebugContext(actx, "jev closing response body", "tag", tag, "error", closeErr)
		}
	}()

	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return nil, classify(ctx, actx, err, cfg.attemptTimeout)
	}

	if int64(len(data)) > c.maxResponseBytes {
		return nil, &ConnectionError{
			Err: fmt.Errorf("response body exceeded %d bytes", c.maxResponseBytes),
		}
	}

	c.logger.InfoContext(actx, "jev response",
		"tag", tag,
		"status", resp.StatusCode,
		"elapsed", c.clock.Now().Sub(started),
		"request_id", resp.Header.Get(requestIDHeader),
	)

	c.observe(Attempt{
		Index:    attempt,
		Status:   resp.StatusCode,
		Duration: c.clock.Now().Sub(started),
	})

	return &rawResponse{status: resp.StatusCode, header: resp.Header, body: data}, nil
}

func (c *Client) setHeaders(req *http.Request, cfg requestConfig, attempt int, hasBody bool) {
	// Caller headers first, so the ones set below cannot be clobbered.
	for name, values := range cfg.header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("X-TypeSafe-SDK", c.userAgent)
	req.Header.Set("X-TypeSafe-Runtime", c.runtime)

	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	} else {
		req.Header.Del("Content-Type")
	}

	req.Header.Del(retryCountHeader)

	if attempt > 0 {
		req.Header.Set(retryCountHeader, strconv.Itoa(attempt))
	}
}

func (c *Client) backOff(
	ctx context.Context,
	tag string,
	attempt, retriesLeft int,
	reason string,
	header http.Header,
	cfg requestConfig,
) error {
	delay := retryDelay(attempt, header, cfg.retry, c.clock.Now(), c.random)

	c.logger.InfoContext(ctx, "jev retrying",
		"tag", tag,
		"in", delay,
		"retry", attempt+1,
		"of", attempt+retriesLeft,
		"after", reason,
	)

	if err := c.clock.Sleep(ctx, delay); err != nil {
		return fmt.Errorf("jev: %s: %w", tag, err)
	}

	return nil
}

type rawResponse struct {
	status int
	header http.Header
	body   []byte
}

func decodeInto(res *rawResponse, out any) error {
	if out == nil {
		return nil
	}

	if len(res.body) == 0 {
		return &ResponseError{Status: res.status, Err: errors.New("empty response body")}
	}

	if err := json.Unmarshal(res.body, out); err != nil {
		return &ResponseError{Status: res.status, Body: res.body, Err: err}
	}

	return nil
}

// classify decides whether a transport failure was the caller, the attempt deadline, or the network.
func classify(ctx, actx context.Context, err error, timeout time.Duration) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if actx.Err() != nil {
		return &TimeoutError{ConnectionError: ConnectionError{Err: err}, Timeout: timeout}
	}

	return &ConnectionError{Err: err}
}

func retryableError(err error, policy RetryPolicy) bool {
	var timeout *TimeoutError
	if errors.As(err, &timeout) {
		return policy.RetryTimeout
	}

	var connection *ConnectionError
	if errors.As(err, &connection) {
		return policy.RetryConnection
	}

	return false
}
