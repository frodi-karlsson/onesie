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

	return func(ctx context.Context, chosen resolution, extra ...onesie.Option) (*onesie.Client, error) {
		opts := []onesie.Option{
			onesie.WithProvider(chosen.provider),
			onesie.WithUserAgent("onesie/" + info.Version),
			onesie.WithEnv(settings.lookupEnv),
		}

		if flags.allowHTTP {
			opts = append(opts, onesie.WithInsecureHTTP())
		}

		opts = append(opts, onesie.WithHTTPClient(&http.Client{Transport: pooled(settings.transport, flags.jobs)}))

		opts = append(opts, chosen.options(flags.baseURL, profileKey(settings, chosen), settings.lookupEnv)...)

		opts = append(opts, onesie.WithRetry(retryPolicy(*flags)), onesie.WithClock(settings.clock))

		// Last, so a caller that needs the attempt observer or any other per run option wins over
		// what the flags asked for.
		opts = append(opts, extra...)

		client, err := onesie.New(opts...)
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

func retryPolicy(flags runFlags) onesie.RetryPolicy {
	// Started from the default, because a Go struct cannot tell an unset field from a zero one and
	// the policy carries seven fields this run does not touch.
	policy := onesie.DefaultRetryPolicy()
	policy.MaxRetries = flags.retries
	policy.MaxRetryAfter = time.Duration(flags.maxRetryAfter) * time.Second

	return policy
}

func runOptions(flags runConfig, stats *collector) []onesie.Option {
	opts := observing(stats)

	// Only when given, so onesie-go keeps its longer default for a request with images.
	if flags.timeoutSet {
		opts = append(opts, onesie.WithAttemptTimeout(time.Duration(flags.timeout)*time.Second))
	}

	return opts
}

func profileKey(settings rootSettings, chosen resolution) func() (string, error) {
	// New calls it only after every other check passed, so a run refused for anything else never
	// reads the keychain.
	return func() (string, error) {
		if chosen.keyFrom != sourceKeychain {
			return chosen.key, nil
		}

		read, err := settings.keychain.Get(chosen.account)
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

func explained(settings rootSettings, baseURLFlag string, chosen resolution, err error) error {
	var refused *onesie.BaseURLError
	if errors.As(err, &refused) {
		return baseURLRefusal(refused, baseURLFrom(refused.Source, baseURLFlag, chosen), sendPlainHTTP)
	}

	if errors.Is(err, onesie.ErrNoAccountID) {
		return accountNote(err, chosen)
	}

	if chosen.profile != "" || chosen.keyFrom != sourceNone {
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

func baseURLFrom(source onesie.BaseURLSource, baseURLFlag string, chosen resolution) string {
	switch source {
	case onesie.BaseURLFromOption:
		name, _ := chosen.givenBaseURL(baseURLFlag)

		return name
	case onesie.BaseURLFromEnv:
		return chosen.provider.EnvBaseURL()
	case onesie.BaseURLFromAccount:
		if chosen.accountID != "" {
			return "the profile's account_id"
		}

		return chosen.provider.EnvAccountID()
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

func pooled(base http.RoundTripper, jobs int) http.RoundTripper {
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

type clientFactory func(ctx context.Context, chosen resolution, opts ...onesie.Option) (*onesie.Client, error)

func resolveModel(settings rootSettings, model string) (string, error) {
	chosen, err := settings.holder.resolved()
	if err != nil {
		return "", err
	}

	return chosen.modelFor(model, settings.lookupEnv), nil
}
