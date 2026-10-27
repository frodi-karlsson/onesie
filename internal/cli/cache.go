package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/cache"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	flagCache = "cache"
	cacheHelp = "store responses on disk and answer a repeated request from them. Also ONESIE_CACHE=1"
)

func answersFor(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, built *plan.Plan, known []plan.Question,
	model string,
) (answererFactory, error) {
	if path, spelled := mockSource(settings, flags); path != "" {
		chosen, err := runResolution(settings)
		if err != nil {
			return nil, err
		}

		answers, err := mockAnswers(settings, flags, chosen.provider, path, spelled, built, known)
		if err != nil {
			return nil, err
		}

		return wrapped(settings, answers), nil
	}

	if !caching(settings, flags) {
		return wrapped(settings, liveAnswers(settings, flags)), nil
	}

	chosen, err := runResolution(settings)
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

	return wrapped(settings, cachedAnswers(settings, flags, chosen.provider, model, built.Questions, warn)), nil
}

func caching(settings rootSettings, flags *runFlags) bool {
	// A mock replaces the answerer the cache would wrap, and a dry run builds none, so skipping the
	// cache there costs nothing and needs no word.
	if path, _ := mockSource(settings, flags); path != "" || flags.printRequest {
		return false
	}

	return flags.env.cache
}

func cachedAnswers(
	settings rootSettings, flags *runFlags, provider onesie.Provider, model string, questions []plan.Question,
	warn func(error),
) answererFactory {
	return func(ctx context.Context, stats *collector) (answerer, error) {
		client, err := settings.newClient(ctx, observing(stats)...)
		if err != nil {
			return nil, noKeyForCache(settings, err)
		}

		return &cachedAnswerer{
			next:      newLiveAnswerer(client, provider, flags),
			open:      sync.OnceValues(func() (*cache.Store, error) { return openCache(settings, flags.env.aliasTTL) }),
			model:     model,
			provider:  provider.Name,
			origin:    provider.Name + "\x00" + client.BaseURL(),
			questions: questions,
			warn:      warn,
		}, nil
	}
}

func noKeyForCache(settings rootSettings, err error) error {
	if !errors.Is(err, onesie.ErrValidation) {
		return err
	}

	chosen, resolveErr := runResolution(settings)
	if resolveErr != nil || chosen.keyFrom != sourceNone {
		return err
	}

	return fmt.Errorf("%w. The cache needs the key too, to find the API address it keys answers by", err)
}

func openCache(settings rootSettings, ttl time.Duration) (*cache.Store, error) {
	dir, err := cache.Dir(configEnv(settings.lookupEnv, settings.homeDir, settings.goos))
	if err != nil {
		return nil, err
	}

	// A cache others can read, or a directory that may not be onesie's, is something the user asked
	// for and has to fix, so each ends the run. Any other failure only turns the cache off.
	store, err := cache.Open(dir, cache.Options{Now: settings.now, GOOS: settings.goos, AliasTTL: ttl})

	var (
		readable *cache.ModeError
		foreign  *cache.ForeignError
	)
	if errors.As(err, &readable) || errors.As(err, &foreign) {
		return nil, &cacheRefusedError{err: err}
	}

	return store, err
}

func (a *cachedAnswerer) answer(ctx context.Context, key recordKey, req onesie.Request) (reply, error) {
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

	id, err := requestKey(onesie.Request{
		State: req.State, Model: a.model, Questions: req.Questions, Assets: req.Assets, LongContext: req.LongContext,
	}, a.origin)
	if err != nil {
		return a.next.answer(ctx, key, req)
	}

	value, hit, err := store.Get(cache.Key(id), a.provider, a.model)
	if err != nil {
		a.fail(failed(store, err))

		return a.next.answer(ctx, key, req)
	}

	// An entry stored before a check onesie now makes would fail every run until it expires, so one
	// that fails is passed over and replaced by a fresh answer.
	if hit {
		if result, decoded := decodeCached(value, req.Questions); decoded && a.usable(result) {
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

func decodeCached(value []byte, questions onesie.Questions) (*onesie.Result, bool) {
	var result onesie.Result
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, false
	}

	for _, named := range questions {
		if _, found := result.Answers[named.ID]; !found {
			return nil, false
		}
	}

	// A hit cost nothing, and a sum over the output stays true.
	result.Usage = onesie.Usage{}

	return &result, true
}

func (a *cachedAnswerer) usable(result *onesie.Result) bool {
	for _, question := range a.questions {
		if _, err := answer.Normalize(question, result.Answers[question.ID]); err != nil {
			return false
		}
	}

	return true
}

func (a *cachedAnswerer) put(store *cache.Store, id cache.Key, result *onesie.Result) error {
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
