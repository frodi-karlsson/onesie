package cli

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

const (
	allowHTTPHelp = "send the API key over a plain http base URL to a host other than this machine. " +
		"Without it such a base URL exits 2 before any request"
	plainHTTPReason = ", so the API key would cross the network unencrypted"
	sendPlainHTTP   = ". Use https, or pass --" + flagAllowHTTP + " to send it anyway"
	storePlainHTTP  = ". Use https, or pass --" + flagAllowHTTP + " to store it, and every run on the profile " +
		"then needs --" + flagAllowHTTP + " too"
	profileBaseURL = "the profile's base_url"
)

func clientBuilder(info BuildInfo, flags *runFlags, settings rootSettings) clientFactory {
	var warned, doubled sync.Once

	return func(_ context.Context, chosen Resolution, extra ...onesie.Option) (*onesie.Client, error) {
		client, err := NewClient(chosen, ClientConfig{
			UserAgent: "onesie/" + info.Version,
			LookupEnv: settings.lookupEnv,
			AllowHTTP: flags.allowHTTP,
			Transport: settings.transport,
			Jobs:      flags.jobs,
			BaseURL:   flags.baseURL,
			Retry:     retryPolicyOf(*flags),
			Clock:     settings.clock,
			Keychain:  settings.keychain,
		}, extra...)
		if err != nil {
			return nil, explained(settings, flags.baseURL, chosen, err)
		}

		var printErr error

		if onesie.InsecureBaseURL(client.BaseURL()) {
			warned.Do(func() {
				printErr = output.Warning(settings.holder.stderr(), output.Printable(client.BaseURL())+
					" is plain http, so the API key crosses the network unencrypted")
			})
		}

		if segment, repeats := client.BaseURLRepeatsPath(); repeats {
			source := baseURLFrom(client.BaseURLSource(), flags.baseURL, chosen)

			doubled.Do(func() {
				warning := source + " ends in /" + segment + " and onesie adds the API path itself, so questions go to " +
					output.Printable(client.SystemOneURL())

				printErr = settings.laterFailures.keepFirst(printErr, output.Warning(settings.holder.stderr(), warning))
			})
		}

		if printErr != nil {
			return nil, printErr
		}

		return client, nil
	}
}

// NewClient builds the client a resolution asks through. extra goes last, so a caller's own options,
// such as the attempt observer, win over the config's.
func NewClient(chosen Resolution, config ClientConfig, extra ...onesie.Option) (*onesie.Client, error) {
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
	opts = append(opts, onesie.WithRetry(config.Retry), onesie.WithClock(config.Clock))
	opts = append(opts, extra...)

	return onesie.New(opts...)
}

// ClientConfig is what NewClient builds a client with. BaseURL is the base URL the caller was given,
// empty for none.
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

func retryPolicyOf(flags runFlags) onesie.RetryPolicy {
	return RetryPolicy(flags.retries, time.Duration(flags.maxRetryAfter)*time.Second)
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

func runOptions(flags runConfig, stats *runner.Collector) []onesie.Option {
	opts := runner.Observing(stats)

	// Only when given, so onesie-go keeps its longer default for a request with images.
	if flags.timeoutSet {
		opts = append(opts, onesie.WithAttemptTimeout(time.Duration(flags.timeout)*time.Second))
	}

	return opts
}

// ProfileKey reads the key of a profile, from the keychain when the profile keeps it there.
func ProfileKey(chosen Resolution, keychain interface{ Get(string) (string, error) }) func() (string, error) {
	// New calls it only after every other check passed, so a run refused for anything else never
	// reads the keychain.
	return func() (string, error) {
		if chosen.KeyFrom != KeyFromKeychain {
			return chosen.key, nil
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

func explained(settings rootSettings, baseURLFlag string, chosen Resolution, err error) error {
	var refused *onesie.BaseURLError
	if errors.As(err, &refused) {
		return baseURLRefusal(refused, baseURLFrom(refused.Source, baseURLFlag, chosen), sendPlainHTTP)
	}

	if errors.Is(err, onesie.ErrNoAccountID) {
		return accountNote(err, chosen)
	}

	if chosen.Profile != "" || chosen.KeyFrom != KeyFromNone {
		return err
	}

	// Read only here, so a run that names its provider and has its key never opens the file. An
	// error reading it leaves the hint out, since the missing key is the failure to report.
	opened, openErr := settings.holder.opened()
	if openErr != nil {
		return err
	}

	return missingKeyHint(err, chosen, opened.Profiles)
}

func baseURLFrom(source onesie.BaseURLSource, baseURLFlag string, chosen Resolution) string {
	switch source {
	case onesie.BaseURLFromOption:
		name, _ := chosen.GivenBaseURL(baseURLFlag)

		return name
	case onesie.BaseURLFromEnv:
		return chosen.Provider.EnvBaseURL()
	case onesie.BaseURLFromAccount:
		if chosen.AccountID != "" {
			return "the profile's account_id"
		}

		return chosen.Provider.EnvAccountID()
	case onesie.BaseURLFromDefault:
		return providersOwn
	}

	return "the base URL"
}

func baseURLRefusal(refused *onesie.BaseURLError, source, plainHTTPAdvice string) error {
	message := source + " " + refused.Rule()
	if refused.Shown != "" {
		message = source + " " + output.Printable(refused.Shown) + " " + refused.Rule()
	}

	switch {
	case errors.Is(refused, onesie.ErrInsecureBaseURL):
		message += plainHTTPReason + plainHTTPAdvice
	case errors.Is(refused, onesie.ErrBaseURLUserInfo):
		message += ". Put the API key in a profile or the provider's key variable instead"
	}

	return &advisedError{cause: refused, message: message}
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

type clientFactory func(ctx context.Context, chosen Resolution, opts ...onesie.Option) (*onesie.Client, error)

func resolveModel(settings rootSettings, model string) (string, error) {
	chosen, err := settings.holder.resolved()
	if err != nil {
		return "", err
	}

	return chosen.ModelFor(model, settings.lookupEnv), nil
}
