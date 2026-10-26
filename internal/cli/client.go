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
	refusedBaseURL = "the base URL must be a plain URL with no credentials, query or fragment. " +
		"Put the API key in a profile or the provider's key variable instead"
	allowHTTPHelp = "send the API key over a plain http base URL to a host other than localhost, " +
		"127.0.0.0/8 or ::1. Without it such a base URL exits 2 before any request"
	plainHTTPReason = " is plain http to a host other than this machine, so the API key would cross the " +
		"network unencrypted"
)

func defaultClientFactory(settings rootSettings, build resolvedClientFactory) clientFactory {
	return func(ctx context.Context, extra ...onesie.Option) (*onesie.Client, error) {
		chosen, err := runResolution(settings)
		if err != nil {
			return nil, err
		}

		return build(ctx, chosen, extra...)
	}
}

func clientBuilder(info BuildInfo, flags *runFlags, settings rootSettings) resolvedClientFactory {
	var warned, doubled sync.Once

	return func(_ context.Context, chosen resolution, extra ...onesie.Option) (*onesie.Client, error) {
		opts := []onesie.Option{
			onesie.WithProvider(chosen.provider),
			onesie.WithUserAgent("onesie/" + info.Version),
			onesie.WithEnv(chosen.env(settings.lookupEnv)),
		}

		if transport := pooled(flags.jobs); transport != nil {
			opts = append(opts, onesie.WithHTTPClient(&http.Client{Transport: transport}))
		}

		if baseURL := strings.TrimSpace(flags.baseURL); baseURL != "" {
			opts = append(opts, onesie.WithBaseURL(baseURL))
		}

		// Every dry run returns before a client is built, so reading the keychain here is what keeps
		// it away from a run that needs no key.
		keyed, err := keyOptions(settings, flags, chosen)
		if err != nil {
			return nil, err
		}

		opts = append(opts, keyed...)

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
			return nil, explained(settings, flags, chosen, withoutCredentials(err))
		}

		// After New, since only the client knows which of the flag, the variable and the profile chose
		// the base URL.
		if refused := refuseUnclearBaseURL(client.BaseURL()); refused != nil {
			return nil, refused
		}

		if plainRemote(client.BaseURL()) && !flags.allowHTTP {
			return nil, plainHTTPRefused(chosen.baseURLSource(flags.baseURL, settings.lookupEnv), client.BaseURL())
		}

		var printErr error

		if plainRemote(client.BaseURL()) {
			warned.Do(func() {
				_, printErr = fmt.Fprintln(settings.holder.stderr(), "warning: "+
					output.Printable(client.BaseURL())+
					" is plain http, so the API key crosses the network unencrypted")
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

func plainHTTPRefused(source, baseURL string) error {
	return errors.New("onesie: " + source + " " + output.Printable(baseURL) + plainHTTPReason +
		". Use https, or pass --" + flagAllowHTTP + " to send it anyway")
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
	parsed, err := url.Parse(baseURL)
	if err != nil || strings.ContainsAny(baseURL, "@?#") || parsed.User != nil {
		return &onesie.ValidationError{Message: refusedBaseURL}
	}

	return nil
}

func withoutCredentials(err error) error {
	// onesie-go's own check runs inside New, ahead of onesie's, and its message ends with the URL as
	// given, credentials and all.
	var invalid *onesie.ValidationError
	if errors.As(err, &invalid) && strings.Contains(invalid.Message, "@") {
		return &onesie.ValidationError{Question: invalid.Question, Message: refusedBaseURL}
	}

	return err
}

func ignoringResolution(newClient clientFactory) resolvedClientFactory {
	return func(ctx context.Context, _ resolution, extra ...onesie.Option) (*onesie.Client, error) {
		return newClient(ctx, extra...)
	}
}

func keyOptions(settings rootSettings, flags *runFlags, chosen resolution) ([]onesie.Option, error) {
	key := chosen.key

	if chosen.keyFrom == sourceKeychain {
		read, err := settings.keychain.Get(chosen.account)
		if err != nil {
			return nil, err
		}

		// An empty key would leave onesie-go to read the provider's variable, which a profile run
		// never does.
		if read == "" {
			return nil, &creds.KeychainError{Op: "read the key from", Err: creds.ErrKeychainMissing}
		}

		key = read
	}

	return chosen.options(flags.baseURL, key, settings.lookupEnv), nil
}

func explained(settings rootSettings, flags *runFlags, chosen resolution, err error) error {
	if chosen.profile != "" {
		return accountNote(err, chosen, flags.baseURL, settings.lookupEnv)
	}

	if chosen.keyFrom != sourceNone {
		return err
	}

	// Read only now, so a run that names its provider and has its key never opens the file. An
	// error reading it leaves the hint out, since the missing key is the failure to report.
	opened, openErr := openedProfiles(settings)
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

	host := parsed.Hostname()
	if host == "localhost" {
		return false
	}

	ip := net.ParseIP(host)

	return ip == nil || !ip.IsLoopback()
}

func pooled(jobs int) http.RoundTripper {
	if jobs < 1 {
		jobs = 1
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		// Something replaced the default. Building one here would drop whatever proxy and dialer
		// settings that replacement carries, so the client keeps its own default instead.
		return nil
	}

	// Cloned rather than built from scratch, so proxy support, the dial timeouts and HTTP/2 come
	// along. The default of two idle connections per host means most of a -j run pays for a fresh
	// handshake on a request the server answers in a fraction of that time.
	transport := base.Clone()
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

type clientFactory func(ctx context.Context, opts ...onesie.Option) (*onesie.Client, error)

type resolvedClientFactory func(ctx context.Context, chosen resolution, opts ...onesie.Option) (*onesie.Client, error)

func resolveModel(settings rootSettings, model string) (string, error) {
	chosen, err := runResolution(settings)
	if err != nil {
		return "", err
	}

	return chosen.modelFor(model, settings.lookupEnv), nil
}
