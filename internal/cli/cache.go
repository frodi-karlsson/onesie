package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/cache"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	envCache    = "ONESIE_CACHE"
	envCacheTTL = "ONESIE_CACHE_TTL"
	flagCache   = "cache"
	cacheHelp   = "store responses on disk and answer a repeated request from them. Also ONESIE_CACHE=1"
)

func answersFor(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, built *plan.Plan, model string,
) (answererFactory, error) {
	if path, spelled := mockSource(settings, flags); path != "" {
		answers, err := mockAnswers(settings, flags, path, spelled, built)
		if err != nil {
			return nil, err
		}

		return wrapped(settings, answers), nil
	}

	on, err := caching(cmd, settings, flags)
	if err != nil {
		return nil, err
	}

	if !on {
		return wrapped(settings, liveAnswers(settings)), nil
	}

	provider, err := resolveProvider(settings, flags)
	if err != nil {
		return nil, err
	}

	var warned atomic.Bool

	warn := func(err error) {
		if !warned.CompareAndSwap(false, true) {
			return
		}

		// The answer is already in hand, so a warning that cannot print fails nothing.
		if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), "warning: "+err.Error()); printErr != nil {
			return
		}
	}

	return wrapped(settings, cachedAnswers(settings, flags, provider.Name, model, built.Questions, warn)), nil
}

func caching(cmd *cobra.Command, settings rootSettings, flags *runFlags) (bool, error) {
	// A mock replaces the answerer the cache would wrap, and a dry run builds none, so skipping the
	// cache there costs nothing and needs no word.
	if path, _ := mockSource(settings, flags); path != "" || flags.printRequest {
		return false, nil
	}

	if cmd.Flags().Changed(flagCache) {
		return flags.cache, nil
	}

	value, found := settings.lookupEnv(envCache)
	if !found || value == "" {
		return false, nil
	}

	on, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("onesie: %s=%s is not a value --cache takes. Use 1 or 0", envCache, value)
	}

	return on, nil
}

func cachedAnswers(
	settings rootSettings, flags *runFlags, provider, model string, questions []plan.Question, warn func(error),
) answererFactory {
	return func(ctx context.Context, stats *collector) (answerer, error) {
		client, err := settings.newClient(ctx, observing(stats)...)
		if err != nil {
			return nil, noKeyForCache(settings, flags, err)
		}

		return &cachedAnswerer{
			next:      liveAnswerer{client: client},
			open:      sync.OnceValues(func() (*cache.Store, error) { return openCache(settings) }),
			model:     model,
			provider:  provider,
			origin:    provider + "\x00" + client.BaseURL(),
			questions: questions,
			warn:      warn,
		}, nil
	}
}

func noKeyForCache(settings rootSettings, flags *runFlags, err error) error {
	if !errors.Is(err, jev.ErrValidation) {
		return err
	}

	source, locateErr := locateKey(settings, flags)
	if locateErr != nil || source.name != sourceNone {
		return err
	}

	return fmt.Errorf("%w. The cache needs the key too, to find the API address it keys answers by", err)
}

func openCache(settings rootSettings) (*cache.Store, error) {
	dir, err := cache.Dir(configEnv(settings.lookupEnv, settings.homeDir, settings.goos))
	if err != nil {
		return nil, err
	}

	// A cache others can read, or a lifetime onesie cannot read, is something the user asked for and
	// has to fix, so either ends the run. Any other failure only turns the cache off.
	ttl, err := aliasTTL(settings.lookupEnv)
	if err != nil {
		return nil, &cacheRefusedError{err: err}
	}

	store, err := cache.Open(dir, cache.Options{Now: settings.now, GOOS: settings.goos, AliasTTL: ttl})

	var readable *cache.ModeError
	if errors.As(err, &readable) {
		return nil, &cacheRefusedError{err: err}
	}

	return store, err
}

func aliasTTL(lookupEnv func(string) (string, bool)) (time.Duration, error) {
	value, found := lookupEnv(envCacheTTL)
	if !found || value == "" {
		return 0, nil
	}

	ttl, err := time.ParseDuration(value)
	if err != nil || ttl < 0 {
		return 0, fmt.Errorf("onesie: %s=%s is not a duration of zero or more, such as 90m or 72h", envCacheTTL, value)
	}

	if ttl == 0 {
		return cache.NoAliasTTL, nil
	}

	return ttl, nil
}

func (a *cachedAnswerer) answer(ctx context.Context, key recordKey, req jev.Request) (reply, error) {
	if a.off.Load() {
		return a.next.answer(ctx, key, req)
	}

	store, err := a.open()
	if halting(err) {
		return reply{}, err
	}

	if err != nil {
		a.fail(fmt.Errorf("the cache could not open, so onesie asks the API for the rest of this run: %w", err))

		return a.next.answer(ctx, key, req)
	}

	id, err := requestKey(req.State, a.model, req.Questions, a.origin)
	if err != nil {
		return a.next.answer(ctx, key, req)
	}

	value, hit, err := store.Get(cache.Key(id), a.provider, a.model)
	if err != nil {
		a.fail(failed(store, err))

		return a.next.answer(ctx, key, req)
	}

	if hit {
		if result, usable := decodeCached(value, req.Questions); usable {
			return reply{result: result, cached: true}, nil
		}
	}

	fresh, err := a.next.answer(ctx, key, req)
	if err != nil || !a.usable(fresh.result) {
		return fresh, err
	}

	if putErr := a.put(store, cache.Key(id), fresh.result); putErr != nil {
		a.fail(failed(store, putErr))
	}

	return fresh, nil
}

func decodeCached(value []byte, questions jev.Questions) (*jev.Result, bool) {
	var result jev.Result
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, false
	}

	for _, named := range questions {
		if _, found := result.Answers[named.ID]; !found {
			return nil, false
		}
	}

	// A hit cost nothing, and a sum over the output stays true.
	result.Usage = jev.Usage{}

	return &result, true
}

func (a *cachedAnswerer) usable(result *jev.Result) bool {
	for _, question := range a.questions {
		if _, err := answer.Normalize(question, result.Answers[question.ID]); err != nil {
			return false
		}
	}

	return true
}

func (a *cachedAnswerer) put(store *cache.Store, id cache.Key, result *jev.Result) error {
	stored := *result
	stored.RequestID = ""

	value, err := json.Marshal(&stored)
	if err != nil {
		return err
	}

	return store.Put(id, a.provider, a.model, value)
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
	next      answerer
	open      func() (*cache.Store, error)
	model     string
	provider  string
	origin    string
	questions []plan.Question
	warn      func(error)
	off       atomic.Bool
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
