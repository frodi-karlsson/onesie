package creds

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	keychainService = "onesie"
	keychainTimeout = 10 * time.Second
)

// KeychainAccount names the keychain item for a provider's key in the credential file at path. The
// path is part of the name, so two config dirs never share, overwrite or delete one item.
func KeychainAccount(provider, path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}

	sum := sha256.Sum256([]byte(path))

	return provider + "@" + hex.EncodeToString(sum[:6])
}

var (
	// ErrKeychainMissing means the keychain holds no item for the provider.
	ErrKeychainMissing = errors.New("the keychain holds no key for this provider. Run onesie auth set to store one")
	// ErrKeychainTimeout means the keychain did not answer, usually because it is waiting on a prompt.
	ErrKeychainTimeout = errors.New("the keychain did not answer in time")
)

// NewKeychain builds a Keychain over the OS keychain. A test overrides only what it must.
func NewKeychain(opts ...KeychainOption) *Keychain {
	chain := &Keychain{backend: osKeyring{}, timeout: keychainTimeout}

	for _, opt := range opts {
		opt(chain)
	}

	return chain
}

// Keychain stores keys in the OS keychain under the onesie service, one item per account.
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

// Get reads the account's key. An empty item reads as missing, since an empty key is no key.
func (k *Keychain) Get(account string) (string, error) {
	var key string

	err := k.bounded("read the key from", func() error {
		found, err := k.backend.Get(keychainService, account)
		key = found

		return err
	})
	if err != nil {
		return "", err
	}

	if key == "" {
		return "", &KeychainError{Op: "read the key from", Err: ErrKeychainMissing}
	}

	return key, nil
}

// Set stores the account's key, replacing any earlier one.
func (k *Keychain) Set(account, key string) error {
	return k.bounded("store the key in", func() error {
		return k.backend.Set(keychainService, account, key)
	})
}

// Delete removes the account's key. A missing item is not an error.
func (k *Keychain) Delete(account string) error {
	err := k.bounded("remove the key from", func() error {
		return k.backend.Delete(keychainService, account)
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
		return &KeychainError{Op: op, Err: fmt.Errorf("%w, after %s", ErrKeychainTimeout, k.timeout)}
	}
}

// KeychainError is a keychain call that failed. It never carries the key.
type KeychainError struct {
	Op  string
	Err error
}

// Error names what onesie tried and why the keychain refused.
func (e *KeychainError) Error() string {
	return fmt.Sprintf("onesie: could not %s the OS keychain: %v", e.Op, e.Err)
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
