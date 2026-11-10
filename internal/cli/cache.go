package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	setting "github.com/frodi-karlsson/onesie/internal/settings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/cache"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const flagCache = "cache"

func answersFor(
	cmd *cobra.Command, settings rootSettings, flags runConfig, built *plan.Plan, known []plan.Question,
) (answererFactory, error) {
	if flags.mockPath != "" {
		chosen, err := connected(settings, flags)
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

		// The answer is already in hand, so a warning that cannot print follows the run's outcome
		// instead of failing it.
		settings.laterFailures.note(output.Warning(cmd.ErrOrStderr(), err.Error()))
	}

	return cachedAnswers(settings, flags, warn), nil
}

func caching(flags runConfig) bool {
	// A mock replaces the answerer the cache would wrap, and a dry run builds none, so skipping the
	// cache there costs nothing and needs no word.
	if flags.mockPath != "" || setting.Get(flags.merged, setting.PrintRequest) {
		return false
	}

	return flags.env.cache
}

func cachedAnswers(settings rootSettings, flags runConfig, warn func(error)) answererFactory {
	return func(ctx context.Context, stats *collector) (answerer, error) {
		chosen, err := connected(settings, flags)
		if err != nil {
			return nil, err
		}

		client, err := settings.newClient(ctx, chosen, runOptions(flags, stats)...)
		if err != nil {
			return nil, noKeyForCache(settings, flags, err)
		}

		return &cachedAnswerer{
			next:          newLiveAnswerer(client),
			open:          sync.OnceValues(func() (*cache.Store, error) { return openCache(settings, flags.env.aliasTTL) }),
			provider:      client.Provider(),
			customBaseURL: client.Provider().CustomBaseURL(client.BaseURL()),
			origin:        client.Provider().Name() + "\x00" + client.BaseURL(),
			warn:          warn,
		}, nil
	}
}

func noKeyForCache(settings rootSettings, flags runConfig, err error) error {
	if !errors.Is(err, onesie.ErrNoAPIKey) {
		return err
	}

	chosen, resolveErr := connected(settings, flags)
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

	// A cache others can read, or a directory that may not be onesie's, is something the user asked
	// for and has to fix, so each ends the run. Any other failure only turns the cache off.
	store, err := cache.Open(dir, cache.Options{Now: settings.now, GOOS: settings.goos, AliasTTL: ttl})
	if cacheRefused(err) {
		return nil, &cacheRefusedError{err: err}
	}

	return store, err
}

func cacheRefused(err error) bool {
	var (
		readable *cache.ModeError
		foreign  *cache.ForeignError
	)

	return errors.As(err, &readable) || errors.As(err, &foreign)
}

func (a *cachedAnswerer) answer(ctx context.Context, key recordKey, ready prepared) (reply, error) {
	if a.off.Load() {
		return a.next.answer(ctx, key, ready)
	}

	store, err := a.open()
	if halting(err) {
		return reply{}, err
	}

	if err != nil {
		a.fail(fmt.Errorf("the cache could not open, so onesie asks the API for the rest of this run: %w", err))

		return a.next.answer(ctx, key, ready)
	}

	id, model := cache.Key(ready.key(a.origin)), ready.body.Model()
	pinned := a.pinned(model)

	value, hit, err := store.Get(id, model, pinned)
	if err != nil {
		a.fail(failed(store, err))

		return a.next.answer(ctx, key, ready)
	}

	// An entry that fails the answer checks would fail every run until it expires, so it is passed
	// over and replaced by a fresh answer.
	if hit {
		if result, usable := decodeCached(value, ready.body); usable {
			return reply{result: result, cached: true}, nil
		}
	}

	fresh, err := a.next.answer(ctx, key, ready)
	if err != nil {
		return fresh, err
	}

	if putErr := put(store, id, model, pinned, fresh.result); putErr != nil {
		a.fail(failed(store, putErr))
	}

	return fresh, nil
}

func (a *cachedAnswerer) pinned(model string) bool {
	return a.provider.Pinned(model) && !a.customBaseURL
}

func decodeCached(value []byte, body *onesie.PreparedBody) (*onesie.Result, bool) {
	var result onesie.Result
	if json.Unmarshal(value, &result) != nil || body.CheckResult(&result) != nil {
		return nil, false
	}

	// A hit cost nothing, and a sum over the output stays true.
	result.Usage = onesie.Usage{}

	return &result, true
}

func put(store *cache.Store, id cache.Key, model string, pinned bool, result *onesie.Result) error {
	stored := *result
	stored.RequestID = ""

	value, err := json.Marshal(&stored)
	if err != nil {
		return err
	}

	return store.Put(id, model, pinned, value)
}

func failed(store *cache.Store, err error) error {
	return fmt.Errorf("the cache at %s failed, so onesie asks the API for the rest of this run: %w", store.Dir(), err)
}

func (a *cachedAnswerer) fail(err error) {
	a.off.Store(true)
	a.warn(err)
}

func (a *cachedAnswerer) salt(key recordKey) (string, bool) {
	return a.next.salt(key)
}

type cachedAnswerer struct {
	next          answerer
	open          func() (*cache.Store, error)
	provider      onesie.Provider
	customBaseURL bool
	origin        string
	warn          func(error)
	off           atomic.Bool
}

func (e *cacheRefusedError) Error() string {
	return e.err.Error()
}

func (e *cacheRefusedError) Unwrap() error {
	return e.err
}

type cacheRefusedError struct {
	err error
}

func halting(err error) bool {
	// A cache the run cannot use ends it before any request, the way a record the mock file does
	// not answer does, rather than failing one record after another.
	var refused *cacheRefusedError

	return uncovered(err) || errors.As(err, &refused)
}
