package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/resolve"
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

	return func(_ context.Context, chosen resolve.Resolution, extra ...onesie.Option) (*onesie.Client, error) {
		client, err := resolve.NewClient(chosen, resolve.ClientConfig{
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

func retryPolicyOf(flags runFlags) onesie.RetryPolicy {
	return resolve.RetryPolicy(flags.retries, time.Duration(flags.maxRetryAfter)*time.Second)
}

func runOptions(flags runConfig, stats *runner.Collector) []onesie.Option {
	opts := runner.Observing(stats)

	// Only when given, so onesie-go keeps its longer default for a request with images.
	if flags.timeoutSet {
		opts = append(opts, onesie.WithAttemptTimeout(time.Duration(flags.timeout)*time.Second))
	}

	return opts
}

func explained(settings rootSettings, baseURLFlag string, chosen resolve.Resolution, err error) error {
	var refused *onesie.BaseURLError
	if errors.As(err, &refused) {
		return baseURLRefusal(refused, baseURLFrom(refused.Source, baseURLFlag, chosen), sendPlainHTTP)
	}

	if errors.Is(err, onesie.ErrNoAccountID) {
		return accountNote(err, chosen)
	}

	if chosen.Profile != "" || chosen.KeyFrom != resolve.KeyFromNone {
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

func baseURLFrom(source onesie.BaseURLSource, baseURLFlag string, chosen resolve.Resolution) string {
	switch source {
	case onesie.BaseURLFromOption:
		if strings.TrimSpace(baseURLFlag) != "" {
			return "--" + flagBaseURL
		}

		return profileBaseURL
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

type clientFactory func(ctx context.Context, chosen resolve.Resolution, opts ...onesie.Option) (*onesie.Client, error)

func resolveModel(settings rootSettings, model string) (string, error) {
	chosen, err := settings.holder.resolved()
	if err != nil {
		return "", err
	}

	return chosen.ModelFor(model, settings.lookupEnv), nil
}
