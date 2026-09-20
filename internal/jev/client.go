package jev

import (
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"runtime"
	"strings"
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
		return nil, &ValidationError{Message: "no API key. Pass WithAPIKey or set " + EnvAPIKey}
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
}

// RetryPolicy returns a copy, so a caller can modify one field and pass it to WithRequestRetry.
func (c *Client) RetryPolicy() RetryPolicy {
	return c.retry
}

// AttemptTimeout returns the per attempt deadline, not a budget for the whole call.
func (c *Client) AttemptTimeout() time.Duration {
	return c.attemptTimeout
}
