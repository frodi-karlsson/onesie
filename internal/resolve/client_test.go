package resolve

import (
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestNewClient(t *testing.T) {
	t.Parallel()

	file, err := ProfileRun("ts", creds.Profile{Provider: "typesafe", APIKey: "SECRET-FILE"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	keychained, err := ProfileRun("work",
		creds.Profile{Provider: "typesafe", Store: creds.StoreKeychain, Account: "work@abc"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		run     Resolution
		wantErr string
	}{
		{name: "should build a client from a config that sets only the environment", run: file},
		{
			name: "should refuse a keychain profile with no keychain", run: keychained,
			wantErr: "the profile keeps its key in the keychain, and the client has no keychain to read it from",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			client, err := NewClient(tc.run, ClientConfig{LookupEnv: lookupFrom(nil)})

			switch {
			case tc.wantErr == "" && (err != nil || client == nil):
				t.Errorf("NewClient = %v, %v, want a client", client, err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("NewClient error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestClientConfig_withDefaults(t *testing.T) {
	t.Parallel()

	t.Run("should fill a zero config with the working defaults", func(t *testing.T) {
		t.Parallel()

		got := ClientConfig{}.withDefaults()

		if got.UserAgent != "onesie" {
			t.Errorf("UserAgent = %q, want onesie", got.UserAgent)
		}

		if reflect.ValueOf(got.LookupEnv).Pointer() != reflect.ValueOf(os.LookupEnv).Pointer() {
			t.Error("LookupEnv is not os.LookupEnv")
		}

		if got.Transport != http.DefaultTransport {
			t.Errorf("Transport = %T, want http.DefaultTransport", got.Transport)
		}

		want := onesie.DefaultRetryPolicy()
		if got.Retry.RetryStatus == nil {
			t.Fatal("Retry has no retry status rule, want the default's")
		}

		got.Retry.RetryStatus, want.RetryStatus = nil, nil

		if !reflect.DeepEqual(got.Retry, want) {
			t.Errorf("Retry = %+v, want %+v", got.Retry, want)
		}
	})

	t.Run("should keep what the config sets", func(t *testing.T) {
		t.Parallel()

		transport := &countingRoundTripper{base: http.DefaultTransport}
		retry := RetryPolicy(7, time.Minute)

		got := ClientConfig{LookupEnv: lookupFrom(nil), Transport: transport, Retry: retry}.withDefaults()

		if reflect.ValueOf(got.LookupEnv).Pointer() == reflect.ValueOf(os.LookupEnv).Pointer() {
			t.Error("LookupEnv is os.LookupEnv, want the one given")
		}

		if got.Transport != transport || got.Retry.MaxRetries != 7 || got.Retry.MaxRetryAfter != time.Minute {
			t.Errorf("config = %+v, want the transport and the retry policy given", got)
		}
	})
}

func TestPooled(t *testing.T) {
	t.Parallel()

	t.Run("should clone the base transport with a pool of -j connections per host", func(t *testing.T) {
		t.Parallel()

		base := &http.Transport{MaxIdleConns: 2}

		got, ok := Pooled(base, 8).(*http.Transport)
		if !ok || got == base {
			t.Fatalf("pooled = %T, want a clone of the base transport", Pooled(base, 8))
		}

		if got.MaxIdleConnsPerHost != 8 || got.MaxConnsPerHost != 8 || got.MaxIdleConns != 8 {
			t.Errorf("pool = %d idle per host, %d per host, %d idle, want 8 each",
				got.MaxIdleConnsPerHost, got.MaxConnsPerHost, got.MaxIdleConns)
		}

		if base.MaxIdleConnsPerHost != 0 {
			t.Errorf("the base transport changed to %d idle per host, want it left alone", base.MaxIdleConnsPerHost)
		}
	})

	t.Run("should size the pool for one job when -j is below one", func(t *testing.T) {
		t.Parallel()

		got, ok := Pooled(&http.Transport{MaxIdleConns: 2}, 0).(*http.Transport)
		if !ok || got.MaxIdleConnsPerHost != 1 || got.MaxConnsPerHost != 1 || got.MaxIdleConns != 2 {
			t.Errorf("pooled = %+v, want one connection per host and the base's two idle", got)
		}
	})

	t.Run("should use a base that is not an http.Transport as it is", func(t *testing.T) {
		t.Parallel()

		base := &countingRoundTripper{base: http.DefaultTransport}
		if got := Pooled(base, 4); got != base {
			t.Errorf("pooled = %T, want the base itself", got)
		}
	})
}

func TestProfileKey(t *testing.T) {
	t.Parallel()

	file, err := ProfileRun("ts", creds.Profile{Provider: "typesafe", APIKey: "SECRET-FILE"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	keychained, err := ProfileRun("work",
		creds.Profile{Provider: "typesafe", Store: creds.StoreKeychain, Account: "work@abc"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		run          Resolution
		items        map[string]string
		want         string
		wantKeychain bool
	}{
		{name: "should give a file profile's key without the keychain", run: file, want: "SECRET-FILE"},
		{
			name: "should read a keychain profile's key from the keychain", run: keychained,
			items: map[string]string{"work@abc": "SECRET-KC"}, want: "SECRET-KC",
		},
		{
			name: "should refuse an empty keychain item as a keychain failure", run: keychained,
			items: map[string]string{"work@abc": ""}, wantKeychain: true,
		},
		{name: "should pass a missing keychain item on as a keychain failure", run: keychained, wantKeychain: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chain := workingKeychain(tc.items)

			got, err := ProfileKey(tc.run, creds.NewKeychain(creds.WithBackend(chain)))()

			var keychainErr *creds.KeychainError
			if errors.As(err, &keychainErr) != tc.wantKeychain || got != tc.want {
				t.Errorf("key = %q, error = %v, want %q and a keychain failure %v", got, err, tc.want, tc.wantKeychain)
			}

			if tc.run.KeyFrom == KeyFromFile && chain.calls() != 0 {
				t.Errorf("keychain calls = %d, want none for a file profile", chain.calls())
			}
		})
	}
}

func TestRetryPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		retries       int
		maxRetryAfter time.Duration
	}{
		{name: "should start from the default and set the retries and the cap", retries: 7, maxRetryAfter: 90 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			want := onesie.DefaultRetryPolicy()
			want.MaxRetries = tc.retries
			want.MaxRetryAfter = tc.maxRetryAfter

			got := RetryPolicy(tc.retries, tc.maxRetryAfter)
			if got.RetryStatus == nil {
				t.Fatal("RetryPolicy left the default's retry status rule out")
			}

			// A func compares equal only to nil, so the rule is checked above and left out here.
			got.RetryStatus, want.RetryStatus = nil, nil

			if !reflect.DeepEqual(got, want) {
				t.Errorf("RetryPolicy = %+v, want %+v", got, want)
			}
		})
	}
}
