package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	refusedBaseURL = "the base URL must not hold @, ? or #. " +
		"Put the API key in a profile or the provider's key variable instead"
	allowHTTPHelp = "send the API key over a plain http base URL to a host other than localhost, " +
		"127.0.0.0/8, also in IPv4-mapped form, or ::1. Without it such a base URL exits 2 before any request"
	plainHTTPReason = " is plain http to a host other than this machine, so the API key would cross the " +
		"network unencrypted"
)

func clientBuilder(info BuildInfo, flags *runFlags, settings rootSettings) clientFactory {
	var warned, doubled sync.Once

	return func(_ context.Context, chosen resolution, extra ...onesie.Option) (*onesie.Client, error) {
		// Ahead of the keychain and of New, so a base URL that carries a key is refused in onesie's
		// words and nothing is sent.
		if refused := refuseUnclearBaseURL(chosen.baseURLGiven(flags.baseURL, settings.lookupEnv)); refused != nil {
			return nil, refused
		}

		opts := []onesie.Option{
			onesie.WithProvider(chosen.provider),
			onesie.WithUserAgent("onesie/" + info.Version),
			onesie.WithEnv(settings.lookupEnv),
			// plainHTTPCheck decides plain http below, in the words of the source that set it.
			onesie.WithInsecureHTTP(),
		}

		if transport := pooled(settings.transport, flags.jobs); transport != nil {
			opts = append(opts, onesie.WithHTTPClient(&http.Client{Transport: transport}))
		}

		if baseURL := strings.TrimSpace(flags.baseURL); baseURL != "" {
			opts = append(opts, onesie.WithBaseURL(baseURL))
		}

		opts = append(opts, chosen.options(flags.baseURL, profileKey(settings, chosen), settings.lookupEnv)...)

		opts = append(opts, onesie.WithAttemptTimeout(
			time.Duration(flags.timeout)*time.Second))

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
			return nil, explained(settings, chosen, err)
		}

		warning, refused := plainHTTPCheck(
			client.BaseURL(), chosen.baseURLSource(flags.baseURL, settings.lookupEnv), flags.allowHTTP)
		if refused != nil {
			return nil, refused
		}

		var printErr error

		if warning != "" {
			warned.Do(func() {
				_, printErr = fmt.Fprintln(settings.holder.stderr(), warning)
			})
		}

		if endsInVersion(client.BaseURL()) {
			doubled.Do(func() {
				_, printErr = fmt.Fprintln(settings.holder.stderr(), "warning: "+
					chosen.baseURLSource(flags.baseURL, settings.lookupEnv)+
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

func plainHTTPCheck(baseURL, source string, allowHTTP bool) (warning string, refused error) {
	switch {
	case !plainRemote(baseURL):
		return "", nil
	case !allowHTTP:
		return "", errors.New(source + " " + output.Printable(baseURL) + plainHTTPReason +
			". Use https, or pass --" + flagAllowHTTP + " to send it anyway")
	}

	return "warning: " + output.Printable(baseURL) + " is plain http, so the API key crosses the network unencrypted", nil
}

func validBaseURL(baseURL string) error {
	if refused := refuseUnclearBaseURL(baseURL); refused != nil {
		return refused
	}

	return onesie.ValidateBaseURL(baseURL)
}

func refuseUnclearBaseURL(baseURL string) error {
	// Any literal @, ? or #, since a password holding a /, a ? or a # parses cleanly as a host with
	// the rest of the password in the path, query or fragment. A provider takes its key from the
	// profile, so a base URL has no use for credentials.
	if strings.ContainsAny(baseURL, "@?#") {
		return &onesie.ValidationError{Message: refusedBaseURL}
	}

	return nil
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

func explained(settings rootSettings, chosen resolution, err error) error {
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

func endsInVersion(baseURL string) bool {
	parsed, err := url.Parse(baseURL)

	return err == nil && strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v1")
}

func plainRemote(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" {
		return false
	}

	// Not localhost. with its trailing dot, which a resolver may send on to DNS rather than answer
	// from the hosts file.
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return false
	}

	ip := net.ParseIP(host)

	return ip == nil || !ip.IsLoopback()
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
