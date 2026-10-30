package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/cache"
)

// NewCachedAnswerer answers from the cache open opens on first use, and asks next on a miss. client
// keys each entry by its provider and API address, and says whether model is pinned. warn hears the
// failure that turns the cache off for the rest of the run.
func NewCachedAnswerer(
	next Answerer, open func() (*cache.Store, error), client *onesie.Client, model string, warn func(error),
) *CachedAnswerer {
	return &CachedAnswerer{
		next:   next,
		open:   sync.OnceValues(open),
		model:  model,
		pinned: client.Provider().Pinned(model),
		origin: client.Provider().Name() + "\x00" + client.BaseURL(),
		warn:   warn,
	}
}

// Answer answers from the cache, or asks next and stores the answer.
func (a *CachedAnswerer) Answer(ctx context.Context, key RecordKey, req onesie.Request) (Reply, error) {
	if a.off.Load() {
		return a.next.Answer(ctx, key, req)
	}

	store, err := a.open()
	if Halting(err) {
		return Reply{}, err
	}

	if err != nil {
		a.fail(fmt.Errorf("the cache could not open, so onesie asks the API for the rest of this run: %w", err))

		return a.next.Answer(ctx, key, req)
	}

	keyed := req
	keyed.Model = a.model

	id, err := requestKey(keyed, a.origin)
	if err != nil {
		return a.next.Answer(ctx, key, req)
	}

	value, hit, err := store.Get(cache.Key(id), a.model, a.pinned)
	if err != nil {
		a.fail(failed(store, err))

		return a.next.Answer(ctx, key, req)
	}

	// An entry that fails the answer checks would fail every run until it expires, so it is passed
	// over and replaced by a fresh answer.
	if hit {
		if result, usable := decodeCached(value, req.Questions); usable {
			return Reply{Result: result, Cached: true}, nil
		}
	}

	fresh, err := a.next.Answer(ctx, key, req)
	if err != nil {
		return fresh, err
	}

	if putErr := a.put(store, cache.Key(id), fresh.Result); putErr != nil {
		a.fail(failed(store, putErr))
	}

	return fresh, nil
}

func decodeCached(value []byte, questions onesie.Questions) (*onesie.Result, bool) {
	var result onesie.Result
	if json.Unmarshal(value, &result) != nil || result.Check(questions) != nil {
		return nil, false
	}

	// A hit cost nothing, and a sum over the output stays true.
	result.Usage = onesie.Usage{}

	return &result, true
}

func (a *CachedAnswerer) put(store *cache.Store, id cache.Key, result *onesie.Result) error {
	stored := *result
	stored.RequestID = ""

	value, err := json.Marshal(&stored)
	if err != nil {
		return err
	}

	return store.Put(id, a.model, a.pinned, value)
}

func failed(store *cache.Store, err error) error {
	return fmt.Errorf("the cache at %s failed, so onesie asks the API for the rest of this run: %w", store.Dir(), err)
}

func (a *CachedAnswerer) fail(err error) {
	a.off.Store(true)
	a.warn(err)
}

// Salt is the salt of the answerer the cache wraps.
func (a *CachedAnswerer) Salt(key RecordKey) (string, bool) {
	return a.next.Salt(key)
}

// CachedAnswerer answers a repeated request from the cache, and asks the API for the rest.
type CachedAnswerer struct {
	next   Answerer
	open   func() (*cache.Store, error)
	model  string
	pinned bool
	origin string
	warn   func(error)
	off    atomic.Bool
}

// OpenCache opens the cache in dir. A refusal ends the run as a HaltError.
func OpenCache(dir string, opts cache.Options) (*cache.Store, error) {
	// A cache others can read, or a directory that may not be onesie's, is something the user asked
	// for and has to fix, so each ends the run. Any other failure only turns the cache off.
	store, err := cache.Open(dir, opts)
	if CacheRefused(err) {
		return nil, &HaltError{Err: err}
	}

	return store, err
}

// CacheRefused reports whether err is a cache onesie will not use, one others can read or a directory
// that may not be onesie's.
func CacheRefused(err error) bool {
	var (
		readable *cache.ModeError
		foreign  *cache.ForeignError
	)

	return errors.As(err, &readable) || errors.As(err, &foreign)
}
