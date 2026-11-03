package resolve

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

var errNoKeychain = errors.New(
	"the profile keeps its key in the keychain, and the client has no keychain to read it from")

// NewClient builds the client a resolution asks through. extra goes last, so a caller's own options,
// such as the attempt observer, win over the config's.
func NewClient(chosen Resolution, config ClientConfig, extra ...onesie.Option) (*onesie.Client, error) {
	config = config.withDefaults()

	opts := []onesie.Option{
		onesie.WithProvider(chosen.Provider),
		onesie.WithUserAgent(config.UserAgent),
		onesie.WithEnv(config.LookupEnv),
	}

	if config.AllowHTTP {
		opts = append(opts, onesie.WithInsecureHTTP())
	}

	opts = append(opts, onesie.WithHTTPClient(&http.Client{Transport: Pooled(config.Transport, config.Jobs)}))
	opts = append(opts, chosen.Options(config.BaseURL, ProfileKey(chosen, config.Keychain), config.LookupEnv)...)
	opts = append(opts, onesie.WithRetry(config.Retry))

	if config.Clock != nil {
		opts = append(opts, onesie.WithClock(config.Clock))
	}

	opts = append(opts, extra...)

	return onesie.New(opts...)
}

// ClientConfig is what NewClient builds a client with, and BaseURL is empty when none was given. A
// zero field takes a working default, and a nil Keychain refuses a profile that keeps its key there.
type ClientConfig struct {
	UserAgent string
	LookupEnv func(string) (string, bool)
	AllowHTTP bool
	Transport http.RoundTripper
	Jobs      int
	BaseURL   string
	Retry     onesie.RetryPolicy
	Clock     onesie.Clock
	Keychain  interface {
		Get(account string) (string, error)
	}
}

func (c ClientConfig) withDefaults() ClientConfig {
	if c.UserAgent == "" {
		c.UserAgent = "onesie"
	}

	if c.LookupEnv == nil {
		c.LookupEnv = os.LookupEnv
	}

	if c.Transport == nil {
		c.Transport = http.DefaultTransport
	}

	// Through reflect, since the policy's func field leaves it no == to compare against.
	if reflect.ValueOf(c.Retry).IsZero() {
		c.Retry = onesie.DefaultRetryPolicy()
	}

	return c
}

// RetryPolicy is the default retry policy with its retries and its cap on a server's retry after.
func RetryPolicy(retries int, maxRetryAfter time.Duration) onesie.RetryPolicy {
	// Started from the default, because a Go struct cannot tell an unset field from a zero one and
	// the policy carries seven fields this run does not touch.
	policy := onesie.DefaultRetryPolicy()
	policy.MaxRetries = retries
	policy.MaxRetryAfter = maxRetryAfter

	return policy
}

// ProfileKey reads the key of a profile, from the keychain when the profile keeps it there.
func ProfileKey(chosen Resolution, keychain interface{ Get(string) (string, error) }) func() (string, error) {
	// New calls it only after every other check passed, so a run refused for anything else never
	// reads the keychain.
	return func() (string, error) {
		if chosen.KeyFrom != KeyFromKeychain {
			return chosen.key, nil
		}

		if keychain == nil {
			return "", errNoKeychain
		}

		read, err := keychain.Get(chosen.Account)
		if err != nil {
			return "", err
		}

		// An empty item is a broken entry, so it exits 3 as every other keychain fault does.
		if read == "" {
			return "", &creds.KeychainError{Op: "read the key from", Err: creds.ErrKeychainMissing}
		}

		return read, nil
	}
}

// Pooled is base with a connection pool sized for jobs requests at once.
func Pooled(base http.RoundTripper, jobs int) http.RoundTripper {
	if jobs < 1 {
		jobs = 1
	}

	defaults, ok := base.(*http.Transport)
	if !ok {
		// A replaced default carries its own proxy and dialer settings, so it is used as it is.
		return base
	}

	// Cloned rather than built from scratch, so proxy support, the dial timeouts and HTTP/2 come
	// along. The default of two idle connections per host means most of a -j run pays for a fresh
	// handshake on a request the server answers in a fraction of that time.
	transport := defaults.Clone()
	transport.MaxIdleConnsPerHost = jobs

	// The idle limit alone does not bound the pool. A request that finds no idle connection
	// starts a dial, and when another job frees one first it takes that and the dial still
	// completes, so the run opens a connection past -j that the idle limit then closes.
	transport.MaxConnsPerHost = jobs

	if transport.MaxIdleConns < jobs {
		transport.MaxIdleConns = jobs
	}

	return transport
}
