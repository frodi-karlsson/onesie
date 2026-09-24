package creds

import (
	"errors"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	keychainService = "onesie"
	keychainTimeout = 10 * time.Second
)

// ErrKeychainMissing means the keychain holds no item for the provider.
var ErrKeychainMissing = errors.New("the keychain holds no key for this provider")

// NewKeychain builds a Keychain over the OS keychain. A test overrides only what it must.
func NewKeychain(opts ...KeychainOption) *Keychain {
	chain := &Keychain{backend: osKeyring{}, timeout: keychainTimeout}

	for _, opt := range opts {
		opt(chain)
	}

	return chain
}

// Keychain stores one key per provider in the OS keychain, under the onesie service.
type Keychain struct {
	backend Backend
	timeout time.Duration
}

// Backend is the keyring a Keychain talks to. The OS keyring in production.
type Backend interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

// KeychainOption customises a Keychain.
type KeychainOption func(*Keychain)

// WithBackend replaces the OS keyring, so a test needs no real keychain.
func WithBackend(backend Backend) KeychainOption {
	return func(k *Keychain) {
		k.backend = backend
	}
}

// WithTimeout bounds each keychain call, since a macOS prompt nobody answers would block forever.
func WithTimeout(timeout time.Duration) KeychainOption {
	return func(k *Keychain) {
		k.timeout = timeout
	}
}

// Get reads the provider's key.
func (k *Keychain) Get(provider string) (string, error) {
	var key string

	err := k.bounded("read the key", func() error {
		found, err := k.backend.Get(keychainService, provider)
		key = found

		return err
	})
	if err != nil {
		return "", err
	}

	return key, nil
}

// Set stores the provider's key, replacing any earlier one.
func (k *Keychain) Set(provider, key string) error {
	return k.bounded("store the key", func() error {
		return k.backend.Set(keychainService, provider, key)
	})
}

// Delete removes the provider's key. A missing item is not an error.
func (k *Keychain) Delete(provider string) error {
	err := k.bounded("remove the key", func() error {
		return k.backend.Delete(keychainService, provider)
	})
	if errors.Is(err, ErrKeychainMissing) {
		return nil
	}

	return err
}

func (k *Keychain) bounded(op string, call func() error) error {
	done := make(chan error, 1)

	go func() {
		done <- call()
	}()

	select {
	case err := <-done:
		if errors.Is(err, keyring.ErrNotFound) {
			return &KeychainError{Op: op, Err: ErrKeychainMissing}
		}

		if err != nil {
			return &KeychainError{Op: op, Err: err}
		}

		return nil
	case <-time.After(k.timeout):
		// The call is left running. It can only finish or be killed with the process, and a CLI
		// exits right after reporting this.
		return &KeychainError{Op: op, Err: fmt.Errorf("the keychain did not answer within %s", k.timeout)}
	}
}

// KeychainError is a keychain call that failed. It never carries the key.
type KeychainError struct {
	Op  string
	Err error
}

// Error names what onesie tried and why the keychain refused.
func (e *KeychainError) Error() string {
	return fmt.Sprintf("onesie: could not %s in the OS keychain: %v", e.Op, e.Err)
}

// Unwrap returns the keychain's reason.
func (e *KeychainError) Unwrap() error {
	return e.Err
}

type osKeyring struct{}

func (osKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (osKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (osKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}
