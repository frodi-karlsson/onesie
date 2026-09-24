package creds_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestKeychain(t *testing.T) {
	t.Parallel()

	t.Run("should store and read a key under the onesie service and the provider name", func(t *testing.T) {
		t.Parallel()

		backend := newFakeBackend()
		chain := creds.NewKeychain(creds.WithBackend(backend))

		if err := chain.Set("openrouter", "SECRET-OR"); err != nil {
			t.Fatalf("Set: %v", err)
		}

		if _, ok := backend.items["onesie/openrouter"]; !ok {
			t.Errorf("items = %v, want an item under onesie/openrouter", backend.keys())
		}

		got, err := chain.Get("openrouter")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}

		if got != "SECRET-OR" {
			t.Error("Get returned a different key than Set stored")
		}
	})

	t.Run("should report a missing item as ErrKeychainMissing", func(t *testing.T) {
		t.Parallel()

		_, err := creds.NewKeychain(creds.WithBackend(newFakeBackend())).Get("typesafe")
		if !errors.Is(err, creds.ErrKeychainMissing) {
			t.Fatalf("error = %v, want ErrKeychainMissing", err)
		}

		var keychainErr *creds.KeychainError
		if !errors.As(err, &keychainErr) {
			t.Fatalf("error = %T, want a KeychainError", err)
		}
	})

	t.Run("should report an empty item as missing", func(t *testing.T) {
		t.Parallel()

		backend := newFakeBackend()
		backend.items["onesie/typesafe"] = ""

		_, err := creds.NewKeychain(creds.WithBackend(backend)).Get("typesafe")
		if !errors.Is(err, creds.ErrKeychainMissing) {
			t.Errorf("error = %v, want ErrKeychainMissing", err)
		}
	})

	t.Run("should treat deleting a missing item as done", func(t *testing.T) {
		t.Parallel()

		if err := creds.NewKeychain(creds.WithBackend(newFakeBackend())).Delete("typesafe"); err != nil {
			t.Errorf("Delete: %v", err)
		}
	})

	t.Run("should wrap a backend failure without the key in the message", func(t *testing.T) {
		t.Parallel()

		backend := newFakeBackend()
		backend.fail = errors.New("no secret service on the bus")

		err := creds.NewKeychain(creds.WithBackend(backend)).Set("typesafe", "SECRET-TS")

		var keychainErr *creds.KeychainError
		if !errors.As(err, &keychainErr) {
			t.Fatalf("error = %v, want a KeychainError", err)
		}

		if strings.Contains(err.Error(), "SECRET-") {
			t.Error("the error carries the key")
		}

		if !strings.Contains(err.Error(), "no secret service on the bus") {
			t.Errorf("error = %q, want the backend's reason", err.Error())
		}
	})

	t.Run("should give up on a backend that does not answer in time", func(t *testing.T) {
		t.Parallel()

		backend := newFakeBackend()
		backend.hang = make(chan struct{})
		defer close(backend.hang)

		chain := creds.NewKeychain(creds.WithBackend(backend), creds.WithTimeout(10*time.Millisecond))

		_, err := chain.Get("typesafe")
		if !errors.Is(err, creds.ErrKeychainTimeout) {
			t.Errorf("error = %v, want ErrKeychainTimeout", err)
		}
	})
}

func TestKeychainAccount(t *testing.T) {
	t.Parallel()

	t.Run("should name the provider and differ between credential files", func(t *testing.T) {
		t.Parallel()

		first := creds.KeychainAccount("typesafe", "/a/credentials.json")
		second := creds.KeychainAccount("typesafe", "/b/credentials.json")

		if !strings.HasPrefix(first, "typesafe@") {
			t.Errorf("account = %q, want it to start with typesafe@", first)
		}

		if first == second {
			t.Errorf("two credential files share the account %q", first)
		}

		if again := creds.KeychainAccount("typesafe", "/a/credentials.json"); again != first {
			t.Errorf("account = %q then %q, want it stable", first, again)
		}
	})
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{items: map[string]string{}}
}

type fakeBackend struct {
	mu    sync.Mutex
	items map[string]string
	fail  error
	hang  chan struct{}
}

func (b *fakeBackend) Set(service, user, password string) error {
	if err := b.wait(); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.items[service+"/"+user] = password

	return nil
}

func (b *fakeBackend) Get(service, user string) (string, error) {
	if err := b.wait(); err != nil {
		return "", err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	password, ok := b.items[service+"/"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}

	return password, nil
}

func (b *fakeBackend) Delete(service, user string) error {
	if err := b.wait(); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.items[service+"/"+user]; !ok {
		return keyring.ErrNotFound
	}

	delete(b.items, service+"/"+user)

	return nil
}

func (b *fakeBackend) wait() error {
	if b.hang != nil {
		<-b.hang
	}

	return b.fail
}

func (b *fakeBackend) keys() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	keys := make([]string, 0, len(b.items))
	for key := range b.items {
		keys = append(keys, key)
	}

	return keys
}
