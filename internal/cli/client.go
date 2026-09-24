package cli

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

func defaultClientFactory(info BuildInfo, flags *runFlags, settings rootSettings) clientFactory {
	return func(_ context.Context, extra ...jev.Option) (*jev.Client, error) {
		provider, err := resolveProvider(settings, flags)
		if err != nil {
			return nil, err
		}

		opts := []jev.Option{
			jev.WithProvider(provider),
			jev.WithUserAgent("onesie/" + info.Version),
			jev.WithEnv(settings.lookupEnv),
		}

		if transport := pooled(flags.jobs); transport != nil {
			opts = append(opts, jev.WithHTTPClient(&http.Client{Transport: transport}))
		}

		// Trimmed, so a whitespace only flag is the same nothing here that it is to resolveKey
		// and storedOptions. Passing it raw installs an option jev.New trims back to empty, which
		// reads as a flag that was honoured.
		if key := strings.TrimSpace(flags.apiKey); key != "" {
			opts = append(opts, jev.WithAPIKey(key))
		}

		if baseURL := strings.TrimSpace(flags.baseURL); baseURL != "" {
			opts = append(opts, jev.WithBaseURL(baseURL))
		}

		// Section 16.1's third step. Every dry run returns before a client is built, so opening the
		// file here is what makes it invisible to a run that needs no key.
		stored, err := storedCredentials(settings, flags)
		if err != nil {
			return nil, err
		}

		opts = append(opts, stored...)

		opts = append(opts, jev.WithAttemptTimeout(
			time.Duration(flags.timeout)*time.Second))

		// Started from the default rather than a zero value, because a Go struct cannot tell an
		// unset field from a zero one and the policy carries seven fields this run does not touch.
		policy := jev.DefaultRetryPolicy()
		policy.MaxRetries = flags.retries
		policy.MaxRetryAfter = time.Duration(flags.maxRetryAfter) * time.Second

		opts = append(opts, jev.WithRetry(policy))

		// Last, so a caller that needs the attempt observer or any other per run option wins over
		// what the flags asked for.
		opts = append(opts, extra...)

		return jev.New(opts...)
	}
}

func storedCredentials(settings rootSettings, flags *runFlags) ([]jev.Option, error) {
	source, err := resolveKey(settings, flags)
	if err != nil {
		return nil, err
	}

	// Section 16.1's third step is the only one that builds options here, since jev.New applies
	// --api-key and then TYPESAFE_API_KEY itself. The guard changes no behaviour today, because
	// resolveKey has already returned for both earlier sources and neither of them carries a
	// stored base URL, but the rule belongs where the file's options are built rather than left to
	// be inferred from what a keySource happens to hold.
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

	if transport.MaxIdleConns < jobs {
		transport.MaxIdleConns = jobs
	}

	return transport
}

type clientFactory func(ctx context.Context, opts ...jev.Option) (*jev.Client, error)

func resolveModel(settings rootSettings, flags *runFlags, model string) (string, error) {
	provider, err := resolveProvider(settings, flags)
	if err != nil {
		return "", err
	}

	return provider.ResolveModel(model, settings.lookupEnv), nil
}
