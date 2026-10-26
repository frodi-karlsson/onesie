package cli

import (
	"context"
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

func defaultClientFactory(settings rootSettings, flags *runFlags, build resolvedClientFactory) clientFactory {
	return func(ctx context.Context, extra ...onesie.Option) (*onesie.Client, error) {
		chosen, err := runResolution(settings, flags)
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

		// Every dry run returns before a client is built, so reading the key here is what keeps the
		// file and the keychain away from a run that needs no key.
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
			return nil, err
		}

		var printErr error

		if plainRemote(client.BaseURL()) {
			warned.Do(func() {
				_, printErr = fmt.Fprintln(settings.holder.stderr(), "warning: "+output.Printable(client.BaseURL())+
					" is plain http, so the API key crosses the network unencrypted")
			})
		}

		if endsInVersion(client.BaseURL()) {
			doubled.Do(func() {
				_, printErr = fmt.Fprintln(settings.holder.stderr(), "warning: "+baseURLSource(settings, flags, chosen)+
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

func ignoringResolution(newClient clientFactory) resolvedClientFactory {
	return func(ctx context.Context, _ resolution, extra ...onesie.Option) (*onesie.Client, error) {
		return newClient(ctx, extra...)
	}
}

func keyOptions(settings rootSettings, flags *runFlags, chosen resolution) ([]onesie.Option, error) {
	if chosen.profile == "" {
		return storedCredentials(settings, flags)
	}

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

func endsInVersion(baseURL string) bool {
	parsed, err := url.Parse(baseURL)

	return err == nil && strings.HasSuffix(strings.TrimRight(parsed.Path, "/"), "/v1")
}

func baseURLSource(settings rootSettings, flags *runFlags, chosen resolution) string {
	if chosen.profile != "" {
		return chosen.baseURLSource(flags.baseURL, settings.lookupEnv)
	}

	if strings.TrimSpace(flags.baseURL) != "" {
		return "--base-url"
	}

	if name := chosen.provider.EnvBaseURL; name != "" {
		if value, found := settings.lookupEnv(name); found && strings.TrimSpace(value) != "" {
			return name
		}
	}

	if name := chosen.provider.EnvAccountID; name != "" {
		if value, found := settings.lookupEnv(name); found && strings.TrimSpace(value) != "" {
			return name
		}
	}

	return "the base URL in the credential file"
}

func plainRemote(baseURL string) bool {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" {
		return false
	}

	host := parsed.Hostname()
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}

	ip := net.ParseIP(host)

	return ip == nil || !ip.IsLoopback()
}

func storedCredentials(settings rootSettings, flags *runFlags) ([]onesie.Option, error) {
	source, err := resolveKey(settings, flags)
	if err != nil {
		return nil, err
	}

	// onesie.New reads the provider's variable itself, so only the file and the keychain need options.
	if source.name != sourceFile && source.name != sourceKeychain {
		return nil, nil
	}

	return storedOptions(settings, flags, source), nil
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

func resolveModel(settings rootSettings, flags *runFlags, model string) (string, error) {
	chosen, err := runResolution(settings, flags)
	if err != nil {
		return "", err
	}

	return chosen.provider.ResolveModel(model, settings.lookupEnv), nil
}
