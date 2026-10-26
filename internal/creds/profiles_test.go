package creds_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

const cloudflareAccount = "76a0a6ac260b32ec3c8703ba592e769f"

func TestValidName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		given string
		valid bool
	}{
		{name: "should take lowercase letters", given: "work", valid: true},
		{name: "should take digits, a dash and an underscore", given: "ci_2-prod", valid: true},
		{name: "should take 64 characters", given: strings.Repeat("a", 64), valid: true},
		{name: "should take a leading underscore", given: "_x", valid: true},
		{name: "should refuse an empty name", given: ""},
		{name: "should refuse 65 characters", given: strings.Repeat("a", 65)},
		{name: "should refuse an uppercase letter", given: "Work"},
		{name: "should refuse a leading dash", given: "-x"},
		{name: "should refuse a space", given: "my work"},
		{name: "should refuse a letter outside ascii", given: "vinna-é"},
		{name: "should refuse none", given: "none"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := creds.ValidName(tc.given)
			if tc.valid && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !tc.valid && (err == nil || !strings.Contains(err.Error(), "is not a profile name")) {
				t.Fatalf("error = %v, want a name error", err)
			}
		})
	}
}

func TestKeyEnd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want string
	}{
		{name: "should give the last four characters of a 16 character key", key: "0123456789abcdef", want: "cdef"},
		{name: "should give nothing for a 15 character key", key: "0123456789abcde"},
		{name: "should give nothing for an empty key"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := creds.KeyEnd(tc.key); got != tc.want {
				t.Errorf("KeyEnd = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestProfilesValidate(t *testing.T) {
	t.Parallel()

	keychain := creds.Profile{Provider: "openrouter", Store: creds.StoreKeychain, Account: "work@abc"}

	tests := []struct {
		name     string
		profiles creds.Profiles
		wantErr  string
	}{
		{name: "should take no profiles", profiles: creds.Profiles{}},
		{
			name: "should take a keychain profile, a file profile and a default",
			profiles: creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{
				"work": keychain,
				"cf":   {Provider: "cloudflare", APIKey: "k", AccountID: cloudflareAccount, Model: "clef-flash"},
			}},
		},
		{
			name:     "should refuse a bad name",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{"Work": keychain}},
			wantErr:  "'Work' is not a profile name",
		},
		{
			name:     "should refuse a profile with no provider",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{"x": {APIKey: "k"}}},
			wantErr:  "profile 'x' names no provider",
		},
		{
			name:     "should refuse an unknown provider",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{"x": {Provider: "nope", APIKey: "k"}}},
			wantErr:  "profile 'x' names provider 'nope', which is not typesafe, openrouter, berget, levanto or cloudflare",
		},
		{
			name:     "should refuse a provider spelled with spaces",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{"x": {Provider: " typesafe", APIKey: "k"}}},
			wantErr:  "profile 'x' names provider ' typesafe'",
		},
		{
			name:     "should refuse a profile with no key",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{"x": {Provider: "typesafe"}}},
			wantErr:  "profile 'x' needs its key in exactly one place",
		},
		{
			name:     "should refuse a whitespace only api_key",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{"x": {Provider: "typesafe", APIKey: " \t\n"}}},
			wantErr:  "profile 'x' needs its key in exactly one place",
		},
		{
			name: "should refuse a profile with a key in both places",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"x": {Provider: "typesafe", APIKey: "k", Store: creds.StoreKeychain, Account: "x@a"},
			}},
			wantErr: "profile 'x' needs its key in exactly one place",
		},
		{
			name: "should refuse a keychain profile with no account",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"x": {Provider: "typesafe", Store: creds.StoreKeychain},
			}},
			wantErr: "profile 'x' needs its key in exactly one place",
		},
		{
			name: "should refuse a store other than the keychain",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"x": {Provider: "typesafe", Store: "vault", Account: "x@a"},
			}},
			wantErr: "profile 'x' needs its key in exactly one place",
		},
		{
			name: "should refuse an account id off cloudflare",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"x": {Provider: "openrouter", APIKey: "k", AccountID: cloudflareAccount},
			}},
			wantErr: "profile 'x' has account_id, which only cloudflare takes",
		},
		{
			name: "should refuse an account id that is not 32 hexadecimal characters",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"x": {Provider: "cloudflare", APIKey: "k", AccountID: "abc"},
			}},
			wantErr: "profile 'x' has an account_id that is not 32 hexadecimal characters",
		},
		{
			name: "should refuse a base url beside an account id",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"x": {Provider: "cloudflare", APIKey: "k", AccountID: cloudflareAccount, BaseURL: "https://x.example"},
			}},
			wantErr: "profile 'x' has both base_url and account_id. Keep one",
		},
		{
			name: "should refuse a key_end longer than 4 characters",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"x": {Provider: "typesafe", APIKey: "k", KeyEnd: "abcde"},
			}},
			wantErr: "profile 'x' has a key_end longer than 4 characters",
		},
		{
			name:     "should refuse a default that names no profile",
			profiles: creds.Profiles{Default: "gone", Profiles: map[string]creds.Profile{"work": keychain}},
			wantErr:  "default names profile 'gone', which does not exist",
		},
		{
			name: "should report the first broken profile in name order",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"b": {Provider: "nope", APIKey: "k"},
				"a": {Provider: "typesafe"},
			}},
			wantErr: "profile 'a' needs its key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.profiles.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestStoreLoadProfiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
		mode     os.FileMode
		absent   bool
		unixOnly bool
		want     creds.Profiles
		wantErr  string
		invalid  bool
	}{
		{
			name: "should read a file with every member",
			contents: `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
				`"account":"work@abc","key_end":"3f9a","model":"typesafe/jev"},` +
				`"cf":{"provider":"cloudflare","api_key":"k","account_id":"` + cloudflareAccount + `"}}}`,
			mode: 0o600,
			want: creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{
				"work": {Provider: "openrouter", Store: creds.StoreKeychain, Account: "work@abc", KeyEnd: "3f9a", Model: "typesafe/jev"},
				"cf":   {Provider: "cloudflare", APIKey: "k", AccountID: cloudflareAccount},
			}},
		},
		{
			name:     "should read a file with no profiles member as no profiles",
			contents: `{}`,
			mode:     0o600,
			want:     creds.Profiles{Profiles: map[string]creds.Profile{}},
		},
		{name: "should report an absent file as absent", absent: true},
		{
			name:     "should refuse an unknown member",
			contents: `{"profiles":{"work":{"provider":"typesafe","api_key":"k","extra":"m"}}}`,
			mode:     0o600,
			wantErr:  `unknown field "extra"`,
			invalid:  true,
		},
		{
			name:     "should refuse a file that is not JSON",
			contents: `not json`,
			mode:     0o600,
			wantErr:  "is not a JSON object of profiles",
			invalid:  true,
		},
		{
			name:     "should refuse a file that is JSON null",
			contents: `null`,
			mode:     0o600,
			wantErr:  "is not a JSON object of profiles",
			invalid:  true,
		},
		{
			name:     "should refuse a file that is a JSON array",
			contents: ` []`,
			mode:     0o600,
			wantErr:  "is not a JSON object of profiles",
			invalid:  true,
		},
		{
			name:     "should refuse a second value after the object",
			contents: `{"profiles":{}} {}`,
			mode:     0o600,
			wantErr:  "is not a JSON object of profiles",
			invalid:  true,
		},
		{
			name:     "should refuse a file that breaks a rule, naming the file",
			contents: `{"profiles":{"work":{"provider":"typesafe"}}}`,
			mode:     0o600,
			wantErr:  "profile 'work' needs its key in exactly one place",
			invalid:  true,
		},
		{
			name:     "should refuse a file over 1 MiB, naming it a profiles file",
			contents: `{"profiles":{},"x":"` + strings.Repeat("a", 1<<20) + `"}`,
			mode:     0o600,
			wantErr:  "profiles file",
		},
		{
			name:     "should refuse a syntax error without quoting the file",
			contents: `{"profiles":{"w":{"api_key":sk-secret}}}`,
			mode:     0o600,
			wantErr:  "is not a JSON object of profiles",
			invalid:  true,
		},
		{
			name:     "should refuse a file others can reach, naming it a profiles file",
			contents: `{"profiles":{}}`,
			mode:     0o640,
			unixOnly: true,
			wantErr:  "profiles file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits")
			}

			path := filepath.Join(t.TempDir(), "profiles.json")
			if !tc.absent {
				if err := os.WriteFile(path, []byte(tc.contents), tc.mode); err != nil {
					t.Fatal(err)
				}
			}

			got, found, err := creds.NewStore().LoadProfiles(path)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}

				var invalid *creds.InvalidProfilesError
				if errors.As(err, &invalid) != tc.invalid {
					t.Errorf("InvalidProfilesError = %v, want %v", errors.As(err, &invalid), tc.invalid)
				}

				if tc.invalid && !strings.Contains(err.Error(), path) {
					t.Errorf("error = %v, want it to name %s", err, path)
				}

				if strings.Contains(err.Error(), "sk-secret") || strings.Contains(err.Error(), "'s'") {
					t.Errorf("error = %v, quotes the file", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if found == tc.absent {
				t.Errorf("found = %v, want %v", found, !tc.absent)
			}

			if !tc.absent && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("profiles = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("should keep a key out of a type error", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "profiles.json")
		if err := os.WriteFile(path, []byte(`{"profiles":{"w":{"provider":"typesafe","api_key":12345678}}}`), 0o600); err != nil {
			t.Fatal(err)
		}

		_, _, err := creds.NewStore().LoadProfiles(path)
		if err == nil || strings.Contains(err.Error(), "12345678") {
			t.Errorf("error = %v, want one that does not repeat the value", err)
		}
	})
}

func TestStoreSaveProfiles(t *testing.T) {
	t.Parallel()

	t.Run("should write a file LoadProfiles reads back, at mode 600", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "onesie", "profiles.json")
		want := creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{
			"work": {Provider: "typesafe", APIKey: "k", Model: "jev-1"},
		}}

		if warning, err := creds.NewStore().SaveProfiles(path, want); err != nil || warning != nil {
			t.Fatalf("SaveProfiles = %v, %v", warning, err)
		}

		got, found, err := creds.NewStore().LoadProfiles(path)
		if err != nil || !found || !reflect.DeepEqual(got, want) {
			t.Fatalf("LoadProfiles = %+v, %v, %v, want %+v", got, found, err, want)
		}

		if runtime.GOOS != "windows" {
			info, statErr := os.Stat(path)
			if statErr != nil {
				t.Fatalf("unexpected error: %v", statErr)
			}

			if info.Mode().Perm() != 0o600 {
				t.Errorf("mode = %v, want 600", info.Mode().Perm())
			}
		}
	})

	t.Run("should write an empty profiles object for no profiles", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "profiles.json")
		if _, err := creds.NewStore().SaveProfiles(path, creds.Profiles{}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := os.ReadFile(path)
		if err != nil || string(data) != `{"profiles":{}}` {
			t.Errorf("file = %s, %v, want {\"profiles\":{}}", data, err)
		}
	})

	t.Run("should refuse to write a file that breaks a rule", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "profiles.json")

		_, err := creds.NewStore().SaveProfiles(path, creds.Profiles{Profiles: map[string]creds.Profile{
			"work": {Provider: "typesafe"},
		}})
		if err == nil || !strings.Contains(err.Error(), "needs its key") {
			t.Fatalf("error = %v, want the rule", err)
		}

		if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("a file was written: %v", statErr)
		}
	})
}
