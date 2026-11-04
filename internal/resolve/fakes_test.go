package resolve

import (
	"errors"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

const testAccountID = "76a0a6ac260b32ec3c8703ba592e769f"

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := env[name]

		return value, ok
	}
}

func workingKeychain(items map[string]string) *fakeKeychain {
	if items == nil {
		items = map[string]string{}
	}

	return &fakeKeychain{items: items}
}

type fakeKeychain struct {
	mu          sync.Mutex
	items       map[string]string
	unavailable bool
	count       int
}

func (k *fakeKeychain) Get(_, account string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.count++

	if k.unavailable {
		return "", errors.New("no keychain in tests")
	}

	key, ok := k.items[account]
	if !ok {
		return "", creds.ErrKeychainMissing
	}

	return key, nil
}

func (k *fakeKeychain) Set(_, account, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.count++

	if k.unavailable {
		return errors.New("no keychain in tests")
	}

	k.items[account] = key

	return nil
}

func (k *fakeKeychain) Delete(_, account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.count++

	if k.unavailable {
		return errors.New("no keychain in tests")
	}

	delete(k.items, account)

	return nil
}

func (k *fakeKeychain) calls() int {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.count
}

func (t *countingRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	t.requests.Add(1)

	return t.base.RoundTrip(request)
}

type countingRoundTripper struct {
	base     http.RoundTripper
	requests atomic.Int32
}
