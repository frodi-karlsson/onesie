package cli

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/cache"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	flagCache = "cache"
	cacheHelp = "store responses on disk and answer a repeated request from them. Also ONESIE_CACHE=1"
)

func answersFor(
	cmd *cobra.Command, settings rootSettings, flags runConfig, built *plan.Plan, known []plan.Question,
	model string,
) (AnswererFactory, error) {
	if flags.mockPath != "" {
		chosen, err := settings.holder.resolved()
		if err != nil {
			return nil, err
		}

		return mockAnswers(settings, flags, chosen.provider, built, known)
	}

	if !caching(flags) {
		return liveAnswers(settings, flags), nil
	}

	var warned atomic.Bool

	warn := func(err error) {
		if !warned.CompareAndSwap(false, true) {
			return
		}

		// The answer is already in hand, so a warning that cannot print fails nothing.
		if printErr := output.Warning(cmd.ErrOrStderr(), err.Error()); printErr != nil {
			return
		}
	}

	return cachedAnswers(settings, flags, model, warn), nil
}

func caching(flags runConfig) bool {
	// A mock replaces the answerer the cache would wrap, and a dry run builds none, so skipping the
	// cache there costs nothing and needs no word.
	if flags.mockPath != "" || flags.printRequest {
		return false
	}

	return flags.env.cache
}

func liveAnswers(settings rootSettings, flags runConfig) AnswererFactory {
	return func(ctx context.Context, stats *Collector) (Answerer, error) {
		chosen, err := settings.holder.resolved()
		if err != nil {
			return nil, err
		}

		client, err := settings.newClient(ctx, chosen, runOptions(flags, stats)...)
		if err != nil {
			return nil, err
		}

		return explainedAnswerer(client), nil
	}
}

func cachedAnswers(settings rootSettings, flags runConfig, model string, warn func(error)) AnswererFactory {
	return func(ctx context.Context, stats *Collector) (Answerer, error) {
		chosen, err := settings.holder.resolved()
		if err != nil {
			return nil, err
		}

		client, err := settings.newClient(ctx, chosen, runOptions(flags, stats)...)
		if err != nil {
			return nil, noKeyForCache(settings, err)
		}

		open := func() (*cache.Store, error) { return openCache(settings, flags.env.aliasTTL) }

		return NewCachedAnswerer(explainedAnswerer(client), open, client, model, warn), nil
	}
}

func explainedAnswerer(client *onesie.Client) LiveAnswerer {
	provider := client.Provider()

	return NewLiveAnswerer(client, func(err error, req onesie.Request) error {
		return explain(err, requestFacts{
			provider: provider, model: req.Model, longContext: req.LongContext, boxed: req.Needs().Box,
		})
	})
}

func noKeyForCache(settings rootSettings, err error) error {
	if !errors.Is(err, onesie.ErrValidation) {
		return err
	}

	chosen, resolveErr := settings.holder.resolved()
	if resolveErr != nil || chosen.keyFrom != sourceNone {
		return err
	}

	return &advisedError{
		cause: err, message: text(err) + ". The cache needs the key too, to find the API address it keys answers by",
	}
}

func openCache(settings rootSettings, ttl time.Duration) (*cache.Store, error) {
	dir, err := cache.Dir(configEnv(settings.lookupEnv, settings.homeDir, settings.goos))
	if err != nil {
		return nil, err
	}

	return OpenCache(dir, cache.Options{Now: settings.now, GOOS: settings.goos, AliasTTL: ttl})
}
