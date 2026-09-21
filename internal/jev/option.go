package jev

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/frodi-karlsson/jev-cli/internal/limits"
)

const (
	// DefaultBaseURL is the API root used when none is configured.
	DefaultBaseURL = "https://api.typesafe.ai"
	// DefaultModel is the model used when a request omits one.
	DefaultModel = "jev-latest"
	// DefaultAttemptTimeout bounds one HTTP attempt, not the whole call.
	DefaultAttemptTimeout = limits.DefaultAttemptTimeout
	// DefaultMaxResponseBytes caps how much of a response body is read.
	DefaultMaxResponseBytes = 8 << 20

	// EnvAPIKey names the environment variable holding the API key.
	EnvAPIKey = "TYPESAFE_API_KEY"
	// EnvBaseURL names the environment variable overriding the API root.
	EnvBaseURL = "TYPESAFE_BASE_URL"
	// EnvDefaultModel names the environment variable overriding the default model.
	EnvDefaultModel = "TYPESAFE_DEFAULT_MODEL"
)

// Option configures a Client at construction.
type Option func(*Client) error

// WithAPIKey sets the API key, taking precedence over the environment.
func WithAPIKey(key string) Option {
	return func(c *Client) error {
		c.apiKey = key

		return nil
	}
}

// WithBaseURL sets the API root, taking precedence over the environment.
func WithBaseURL(raw string) Option {
	return func(c *Client) error {
		c.baseURL = raw

		return nil
	}
}

// WithDefaultModel sets the model used when a request omits one.
func WithDefaultModel(model string) Option {
	return func(c *Client) error {
		c.defaultModel = model

		return nil
	}
}

// WithUserAgent sets the name this client reports. Pass the binary name and its version.
func WithUserAgent(name string) Option {
	return func(c *Client) error {
		if strings.TrimSpace(name) == "" {
			return &ValidationError{Message: "user agent must not be empty"}
		}

		c.userAgent = name

		return nil
	}
}

// WithHTTPClient replaces the transport.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) error {
		if hc == nil {
			return &ValidationError{Message: "http client must not be nil"}
		}

		c.httpClient = hc

		return nil
	}
}

// WithLogger sets the logger. The default discards everything, as a library should.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) error {
		if l == nil {
			return &ValidationError{Message: "logger must not be nil"}
		}

		c.logger = l

		return nil
	}
}

// WithRetry replaces the retry policy.
func WithRetry(p RetryPolicy) Option {
	return func(c *Client) error {
		c.retry = p

		return nil
	}
}

// WithAttemptTimeout bounds one HTTP attempt. Retries each get a fresh budget, so a call can
// outlast this by the retry count. Use WithTotalTimeout or the context to bound the whole call.
func WithAttemptTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d <= 0 {
			return &ValidationError{Message: "attempt timeout must be positive"}
		}

		c.attemptTimeout = d

		return nil
	}
}

// WithTotalTimeout bounds a whole call including every retry and every wait between them. Zero,
// the default, means unbounded apart from the caller's context.
func WithTotalTimeout(d time.Duration) Option {
	return func(c *Client) error {
		if d < 0 {
			return &ValidationError{Message: "total timeout must not be negative"}
		}

		c.totalTimeout = d

		return nil
	}
}

// WithMaxResponseBytes caps how much of a response body is read, so a misbehaving server cannot
// drive this client to allocate without bound.
func WithMaxResponseBytes(n int64) Option {
	return func(c *Client) error {
		if n <= 0 {
			return &ValidationError{Message: "max response bytes must be positive"}
		}

		c.maxResponseBytes = n

		return nil
	}
}

// WithHeader adds a header sent with every request. It cannot override the headers this client
// sets itself, such as Authorization.
func WithHeader(name, value string) Option {
	return func(c *Client) error {
		c.header.Set(name, value)

		return nil
	}
}

// WithEnv replaces the environment lookup. It exists so configuration tests need no process
// mutation, because t.Setenv cannot be combined with t.Parallel.
func WithEnv(lookup func(string) (string, bool)) Option {
	return func(c *Client) error {
		if lookup == nil {
			return &ValidationError{Message: "env lookup must not be nil"}
		}

		c.lookupEnv = lookup

		return nil
	}
}

// WithClock replaces the time source. It exists so retry tests are deterministic and instant.
func WithClock(clock Clock) Option {
	return func(c *Client) error {
		if clock == nil {
			return &ValidationError{Message: "clock must not be nil"}
		}

		c.clock = clock

		return nil
	}
}

// WithRandom replaces the jitter source, which must return a value from 0 up to but excluding 1.
// It exists so backoff tests can assert exact delays.
func WithRandom(random func() float64) Option {
	return func(c *Client) error {
		if random == nil {
			return &ValidationError{Message: "random must not be nil"}
		}

		c.random = random

		return nil
	}
}

// WithAttemptObserver reports every HTTP attempt, so a caller can count retries by status. It is
// called from every goroutine sharing the client, so it must be safe for concurrent use.
func WithAttemptObserver(fn func(Attempt)) Option {
	return func(c *Client) error {
		if fn == nil {
			return &ValidationError{Message: "attempt observer must not be nil"}
		}

		c.observe = fn

		return nil
	}
}

// RequestOption overrides client settings for one call.
type RequestOption func(*requestConfig) error

// WithRequestAttemptTimeout overrides the per attempt timeout for one call.
func WithRequestAttemptTimeout(d time.Duration) RequestOption {
	return func(rc *requestConfig) error {
		if d <= 0 {
			return &ValidationError{Message: "attempt timeout must be positive"}
		}

		rc.attemptTimeout = d

		return nil
	}
}

// WithRequestTotalTimeout overrides the total budget for one call. Zero means unbounded.
func WithRequestTotalTimeout(d time.Duration) RequestOption {
	return func(rc *requestConfig) error {
		if d < 0 {
			return &ValidationError{Message: "total timeout must not be negative"}
		}

		rc.totalTimeout = d

		return nil
	}
}

// WithRequestRetry replaces the whole policy for one call. Start from Client.RetryPolicy and
// modify a field, because a Go struct cannot tell an unset field from a zero one.
func WithRequestRetry(p RetryPolicy) RequestOption {
	return func(rc *requestConfig) error {
		if err := p.validate(); err != nil {
			return err
		}

		rc.retry = p

		return nil
	}
}

// WithRequestHeader adds a header to one call. It cannot override the headers this client sets
// itself, such as Authorization.
func WithRequestHeader(name, value string) RequestOption {
	return func(rc *requestConfig) error {
		rc.header.Set(name, value)

		return nil
	}
}

type requestConfig struct {
	attemptTimeout time.Duration
	totalTimeout   time.Duration
	retry          RetryPolicy
	header         http.Header
}

func validateBaseURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return &ValidationError{Message: "base URL must be an absolute http or https URL, got " + raw}
	}

	return nil
}

func orEnv(value string, lookup func(string) (string, bool), name string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}

	found, ok := lookup(name)
	if !ok {
		return ""
	}

	return strings.TrimSpace(found)
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}

	return value
}
