package cli

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
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
	profileBaseURL  = "the profile's base_url"
)

func clientBuilder(info BuildInfo, flags *runFlags, settings rootSettings) clientFactory {
	var warned, doubled sync.Once

	return func(_ context.Context, chosen resolution, extra ...onesie.Option) (*onesie.Client, error) {
		opts := []onesie.Option{
			onesie.WithProvider(chosen.provider),
			onesie.WithUserAgent("onesie/" + info.Version),
			onesie.WithEnv(settings.lookupEnv),
		}

		if flags.allowHTTP {
			opts = append(opts, onesie.WithInsecureHTTP())
		}

		if transport := pooled(settings.transport, flags.jobs); transport != nil {
			opts = append(opts, onesie.WithHTTPClient(&http.Client{Transport: transport}))
		}

		opts = append(opts, chosen.options(flags.baseURL, profileKey(settings, chosen), settings.lookupEnv)...)

		// Started from the default rather than a zero value, because a Go struct cannot tell an
		// unset field from a zero one and the policy carries seven fields this run does not touch.
		policy := onesie.DefaultRetryPolicy()
		policy.MaxRetries = flags.retries
		policy.MaxRetryAfter = time.Duration(flags.maxRetryAfter) * time.Second

		opts = append(opts, onesie.WithRetry(policy))

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

		if endsInVersion(client.BaseURL()) {
			source, _ := baseURLFrom(client.BaseURLSource(), flags.baseURL, chosen, settings.lookupEnv)

			doubled.Do(func() {
				printErr = output.Warning(settings.holder.stderr(), source+
					" ends in /v1 and onesie adds the API path itself, so requests go to "+
					output.Printable(client.SystemOneURL()))
			})
		}

		if printErr != nil {
			return nil, printErr
		}

		return client, nil
	}
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
		source, given := baseURLFrom(refused.Source, baseURLFlag, chosen, settings.lookupEnv)

		return baseURLRefusal(refused, source, given)
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

func baseURLFrom(
	source onesie.BaseURLSource, baseURLFlag string, chosen resolution, lookupEnv func(string) (string, bool),
) (name, given string) {
	switch source {
	case onesie.BaseURLFromOption:
		return chosen.givenBaseURL(baseURLFlag)
	case onesie.BaseURLFromEnv:
		return chosen.provider.EnvBaseURL(), setVariable(lookupEnv, chosen.provider.EnvBaseURL())
	case onesie.BaseURLFromAccount:
		if chosen.accountID != "" {
			return "the profile's account_id", ""
		}

		return chosen.provider.EnvAccountID(), ""
	case onesie.BaseURLFromDefault:
		return providersOwn, ""
	}

	return "the base URL", ""
}

func baseURLRefusal(refused *onesie.BaseURLError, source, given string) error {
	message := source + " " + refused.Rule()
	if refused.Shown != "" {
		message = source + " " + output.Printable(refused.Shown) + " " + refused.Rule()
	}

	switch {
	case errors.Is(refused, onesie.ErrInsecureBaseURL):
		message += plainHTTPReason + ". Use https, or pass --" + flagAllowHTTP + " to send it anyway"
	case strings.Contains(given, "@"):
		message += ". Put the API key in a profile or the provider's key variable instead"
	}

	return &advisedError{cause: refused, message: message}
}

func endsInVersion(baseURL string) bool {
	parsed, err := url.Parse(baseURL)

	return err == nil && strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v1")
}

func pooled(base http.RoundTripper, jobs int) http.RoundTripper {
	if jobs < 1 {
		jobs = 1
	}

	defaults, ok := base.(*http.Transport)
	if !ok {
		// Something replaced the default. Building one here would drop whatever proxy and dialer
		// settings that replacement carries, so the client keeps its own default instead.
		return nil
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
