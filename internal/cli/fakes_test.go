package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

func recordingServer(t *testing.T, status int) (*httptest.Server, func() []string) {
	t.Helper()

	var mu sync.Mutex

	var auths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		body := `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`
		if status != 0 {
			w.WriteHeader(status)

			body = `{"error":{"message":"the key was refused"}}`
		}

		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))

	t.Cleanup(srv.Close)

	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), auths...)
	}
}

func stubFactory(base string) func(context.Context, ...onesie.Option) (*onesie.Client, error) {
	return func(_ context.Context, extra ...onesie.Option) (*onesie.Client, error) {
		policy := onesie.DefaultRetryPolicy()
		// A refused connection is retried by default, which would spend seconds proving a point
		// the first attempt already made.
		policy.MaxRetries = 0

		return onesie.New(append([]onesie.Option{
			onesie.WithAPIKey("stub"),
			onesie.WithBaseURL(base),
			onesie.WithRetry(policy),
			onesie.WithEnv(func(string) (string, bool) { return "", false }),
		}, extra...)...)
	}
}

func fixedPath(path string) func() (string, error) {
	return func() (string, error) {
		return path, nil
	}
}

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := env[name]

		return value, ok
	}
}

func assertNoSecret(t *testing.T, out, errOut string) {
	t.Helper()

	// Every fixture key carries the SECRET- prefix, so one scan covers every source. Neither stream
	// is printed on failure, since quoting it would leak the key.
	if strings.Contains(out, "SECRET-") {
		t.Error("stdout carries a key")
	}

	if strings.Contains(errOut, "SECRET-") {
		t.Error("stderr carries a key")
	}
}

func noKeychain() *fakeKeychain {
	return &fakeKeychain{items: map[string]string{}, unavailable: true}
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

func (k *fakeKeychain) Get(account string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.count++

	if k.unavailable {
		return "", &creds.KeychainError{Op: "read the key from", Err: errors.New("no keychain in tests")}
	}

	key, ok := k.items[account]
	if !ok {
		return "", &creds.KeychainError{Op: "read the key from", Err: creds.ErrKeychainMissing}
	}

	return key, nil
}

func (k *fakeKeychain) Set(account, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.count++

	if k.unavailable {
		return &creds.KeychainError{Op: "store the key in", Err: errors.New("no keychain in tests")}
	}

	k.items[account] = key

	return nil
}

func (k *fakeKeychain) Delete(account string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.count++

	if k.unavailable {
		return &creds.KeychainError{Op: "remove the key from", Err: errors.New("no keychain in tests")}
	}

	delete(k.items, account)

	return nil
}

func (k *fakeKeychain) calls() int {
	k.mu.Lock()
	defer k.mu.Unlock()

	return k.count
}

type timingOutKeychain struct{}

func (timingOutKeychain) Get(string) (string, error) {
	return "", &creds.KeychainError{Op: "read the key from", Err: creds.ErrKeychainTimeout}
}

func (timingOutKeychain) Set(string, string) error {
	return &creds.KeychainError{Op: "store the key in", Err: creds.ErrKeychainTimeout}
}

func (timingOutKeychain) Delete(string) error {
	return &creds.KeychainError{Op: "remove the key from", Err: creds.ErrKeychainTimeout}
}
