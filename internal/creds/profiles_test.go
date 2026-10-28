package creds_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
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
			wantErr:  "is invalid. Profile 'work' has an unknown member 'extra'",
			invalid:  true,
		},
		{
			name:     "should refuse an unknown member at the top level",
			contents: `{"profiles":{},"extra":"m"}`,
			mode:     0o600,
			wantErr:  "is invalid. The file has an unknown member 'extra'",
			invalid:  true,
		},
		{
			name:     "should read a member spelled in another case as the member it folds to",
			contents: `{"Default":"work","PROFILES":{"work":{"Provider":"typesafe","API_KEY":"k"}}}`,
			mode:     0o600,
			want: creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{
				"work": {Provider: "typesafe", APIKey: "k"},
			}},
		},
		{
			name:     "should refuse a profile that is not an object",
			contents: `{"profiles":{"work":3}}`,
			mode:     0o600,
			wantErr:  "is invalid. Profile 'work' is a number, not an object",
			invalid:  true,
		},
		{
			name:     "should name the first unknown member by name when a profile has several",
			contents: `{"profiles":{"work":{"zeta":1,"provider":"typesafe","api_key":"k","alpha":2}}}`,
			mode:     0o600,
			wantErr:  "is invalid. Profile 'work' has an unknown member 'alpha'",
			invalid:  true,
		},
		{
			name:     "should name an unknown member ahead of a member of the wrong type",
			contents: `{"profiles":{"work":{"provider":3,"extra":"m"}}}`,
			mode:     0o600,
			wantErr:  "is invalid. Profile 'work' has an unknown member 'extra'",
			invalid:  true,
		},
		{
			name:     "should refuse a member of the wrong type, naming the profile and the member",
			contents: `{"profiles":{"work":{"provider":3,"api_key":"k"}}}`,
			mode:     0o600,
			wantErr:  "is invalid. Profile 'work' has a provider that is a number, not a string",
			invalid:  true,
		},
		{
			name:     "should refuse a top level member of the wrong type",
			contents: `{"profiles":[]}`,
			mode:     0o600,
			wantErr:  "is invalid. The file has a profiles that is an array, not an object",
			invalid:  true,
		},
		{
			name:     "should say a file that is not JSON is not valid JSON",
			contents: `not json`,
			mode:     0o600,
			wantErr:  "is not valid JSON",
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
			name:     "should say a second value after the object is not valid JSON",
			contents: `{"profiles":{}} {}`,
			mode:     0o600,
			wantErr:  "is not valid JSON",
			invalid:  true,
		},
		{
			name:     "should say a truncated file is not valid JSON",
			contents: `{"default":"work","profiles":{`,
			mode:     0o600,
			wantErr:  "is not valid JSON",
			invalid:  true,
		},
		{
			name:     "should refuse a file that breaks a rule, naming the file",
			contents: `{"profiles":{"work":{"provider":"typesafe"}}}`,
			mode:     0o600,
			wantErr:  "profiles.json is invalid. Profile 'work' needs its key in exactly one place",
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
			wantErr:  "is not valid JSON",
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

	t.Run("should overwrite an existing file", func(t *testing.T) {
		t.Parallel()

		t.Run("should replace the contents and restore mode 600", func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "profiles.json")

			if err := os.WriteFile(path, []byte(`{"profiles":{"work":{"provider":"typesafe","api_key":"old"}}}`), 0o644); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			// WriteFile respects umask, so the leaked mode is set explicitly afterwards.
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatalf("setting the fixture mode: %v", err)
			}

			warning, err := creds.NewStore().SaveProfiles(path, oneProfile("new"))
			if err != nil {
				t.Fatalf("SaveProfiles: %v", err)
			}

			if warning != nil {
				t.Fatalf("SaveProfiles warned unexpectedly: %v", warning)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}

			if strings.TrimSpace(string(data)) != `{"profiles":{"work":{"provider":"typesafe","api_key":"new"}}}` {
				t.Error("SaveProfiles left the old contents in place")
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}

			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Errorf("file mode = %o, want 600", info.Mode().Perm())
			}
		})
	})

	t.Run("should leave no temporary file behind", func(t *testing.T) {
		t.Parallel()

		// A scratch file left behind has leaked the key into a second path, so the directory is
		// asserted whole rather than only at the profiles path.
		tests := []struct {
			name        string
			blockRename bool
		}{
			{name: "should leave profiles.json as the only entry"},
			{name: "should remove the temporary file when the rename fails", blockRename: true},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				path := filepath.Join(dir, "profiles.json")

				if tc.blockRename {
					// A rename onto an existing directory fails, which is the cheapest way to reach
					// SaveProfiles's cleanup path without stubbing the filesystem.
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatalf("creating the blocking directory: %v", err)
					}
				}

				_, err := creds.NewStore().SaveProfiles(path, oneProfile("k"))

				if tc.blockRename && err == nil {
					t.Fatal("SaveProfiles succeeded onto a directory, want a failure")
				}

				if !tc.blockRename && err != nil {
					t.Fatalf("SaveProfiles: %v", err)
				}

				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatalf("reading the directory: %v", err)
				}

				if len(entries) != 1 {
					names := make([]string, 0, len(entries))
					for _, entry := range entries {
						names = append(names, entry.Name())
					}

					t.Fatalf("directory holds %v, want profiles.json alone", names)
				}

				if entries[0].Name() != "profiles.json" {
					t.Errorf("directory holds %s, want profiles.json", entries[0].Name())
				}
			})
		}
	})

	t.Run("should save through a symlink safely", func(t *testing.T) {
		t.Parallel()

		t.Run("should replace the link rather than write to its target", func(t *testing.T) {
			t.Parallel()

			if runtime.GOOS == "windows" {
				t.Skip("windows has no symlink without elevation, so the redirection is not reachable")
			}

			dir := t.TempDir()
			target := filepath.Join(t.TempDir(), "planted.json")
			path := filepath.Join(dir, "profiles.json")

			if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
				t.Fatalf("writing the target: %v", err)
			}

			if err := os.Symlink(target, path); err != nil {
				t.Fatalf("linking: %v", err)
			}

			if _, err := creds.NewStore().SaveProfiles(path, oneProfile("k")); err != nil {
				t.Fatalf("SaveProfiles: %v", err)
			}

			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("reading the target: %v", err)
			}

			// Never printed, since the target holds the key when this assertion fails.
			if string(data) != "untouched" {
				t.Error("SaveProfiles wrote through the symlink, so the key landed outside the config directory")
			}

			info, err := os.Lstat(path)
			if err != nil {
				t.Fatalf("lstat: %v", err)
			}

			if !info.Mode().IsRegular() {
				t.Errorf("profiles path is %v, want a regular file", info.Mode().Type())
			}
		})
	})

	t.Run("should warn rather than fail when chmod fails", func(t *testing.T) {
		t.Parallel()

		t.Run("should write the file and warn rather than fail", func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "onesie", "profiles.json")
			chmod := func(string, os.FileMode) error {
				return errors.New("this filesystem carries no modes")
			}

			warning, err := creds.NewStore(creds.WithChmod(chmod)).SaveProfiles(path, oneProfile("k"))
			if err != nil {
				t.Fatalf("SaveProfiles returned the mode failure as an error, want a written file: %v", err)
			}

			if warning == nil {
				t.Fatal("SaveProfiles returned no warning, want one naming the path")
			}

			if warning.Path != path {
				t.Errorf("warning path = %s, want %s", warning.Path, path)
			}

			if !strings.Contains(warning.Error(), path) {
				t.Errorf("warning = %v, want it to name %s", warning, path)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}

			if strings.TrimSpace(string(data)) != `{"profiles":{"work":{"provider":"typesafe","api_key":"k"}}}` {
				t.Error("SaveProfiles warned without writing the file")
			}
		})
	})

	t.Run("should create the temporary file beside the target", func(t *testing.T) {
		t.Parallel()

		// A rename out of os.TempDir crosses a mount and fails with EXDEV, and the key would sit in a
		// world listable directory on the way. The directory passed here is the only guard on that.
		t.Run("should create it in the profiles file's own directory", func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "onesie", "profiles.json")

			gotDir := ""
			createTemp := func(dir, pattern string) (*os.File, error) {
				gotDir = dir

				return os.CreateTemp(dir, pattern)
			}

			store := creds.NewStore(creds.WithCreateTemp(createTemp))
			if _, err := store.SaveProfiles(path, oneProfile("k")); err != nil {
				t.Fatalf("SaveProfiles: %v", err)
			}

			if gotDir != filepath.Dir(path) {
				t.Errorf("temporary file created in %q, want %s", gotDir, filepath.Dir(path))
			}
		})
	})

	t.Run("should fail when the temporary file cannot be written", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			handle  func(t *testing.T, dir string) (*os.File, error)
			sync    func(*os.File) error
			wantErr string
		}{
			{
				// A real file closed before SaveProfiles reaches it, so the write fails the same way
				// everywhere. A read only handle is not portable, since Go grants windows write
				// access to an O_RDONLY handle opened with O_CREATE. The file stays on disk for
				// SaveProfiles to remove.
				name: "should fail and remove the temporary file when the write fails",
				handle: func(_ *testing.T, dir string) (*os.File, error) {
					file, err := os.OpenFile(filepath.Join(dir, ".profiles-stub"),
						os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
					if err != nil {
						return nil, err
					}

					if closeErr := file.Close(); closeErr != nil {
						return nil, closeErr
					}

					return file, nil
				},
				wantErr: "writing",
			},
			{
				// The flush is stubbed to fail, since a handle whose flush fails is not portable.
				name: "should fail when the temporary file cannot be flushed",
				handle: func(_ *testing.T, dir string) (*os.File, error) {
					return os.CreateTemp(dir, ".profiles-stub")
				},
				sync:    func(*os.File) error { return errors.New("stub flush failure") },
				wantErr: "flushing",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				path := filepath.Join(dir, "profiles.json")

				createTemp := func(d, _ string) (*os.File, error) {
					return tc.handle(t, d)
				}

				opts := []creds.StoreOption{creds.WithCreateTemp(createTemp)}
				if tc.sync != nil {
					opts = append(opts, creds.WithSync(tc.sync))
				}

				store := creds.NewStore(opts...)

				_, err := store.SaveProfiles(path, oneProfile("k"))
				if err == nil {
					t.Fatal("SaveProfiles succeeded on a handle it could not write, want a failure")
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("SaveProfiles error = %v, want it to contain %s", err, tc.wantErr)
				}

				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatalf("reading the directory: %v", err)
				}

				if len(entries) != 0 {
					names := make([]string, 0, len(entries))
					for _, entry := range entries {
						names = append(names, entry.Name())
					}

					t.Fatalf("directory holds %v, want it empty", names)
				}
			})
		}
	})

	t.Run("should report what the filesystem refuses", func(t *testing.T) {
		t.Parallel()

		mkdirFailure := errors.New("stub mkdir failure")
		renameFailure := errors.New("stub rename failure")
		removeFailure := errors.New("stub remove failure")

		tests := []struct {
			name      string
			mkdirAll  func(string, os.FileMode) error
			rename    func(string, string) error
			remove    func(string) error
			wantErr   []error
			wantInErr string
			wantFile  bool
		}{
			{
				name:      "should fail before any file exists when the directory cannot be created",
				mkdirAll:  func(string, os.FileMode) error { return mkdirFailure },
				wantErr:   []error{mkdirFailure},
				wantInErr: "creating",
			},
			{
				name:      "should fail and remove the temporary file when the rename fails",
				rename:    func(string, string) error { return renameFailure },
				wantErr:   []error{renameFailure},
				wantInErr: "renaming",
			},
			{
				name:   "should report both failures when the temporary file cannot be removed",
				rename: func(string, string) error { return renameFailure },
				remove: func(string) error { return removeFailure },
				// The stubbed remove leaves the temporary file behind, so the directory is not
				// asserted empty here.
				wantErr:   []error{renameFailure, removeFailure},
				wantInErr: "renaming",
				wantFile:  true,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				dir := filepath.Join(t.TempDir(), "onesie")
				path := filepath.Join(dir, "profiles.json")

				var opts []creds.StoreOption
				if tc.mkdirAll != nil {
					opts = append(opts, creds.WithMkdirAll(tc.mkdirAll))
				}

				if tc.rename != nil {
					opts = append(opts, creds.WithRename(tc.rename))
				}

				if tc.remove != nil {
					opts = append(opts, creds.WithRemove(tc.remove))
				}

				_, err := creds.NewStore(opts...).SaveProfiles(path, oneProfile("k"))

				for _, want := range tc.wantErr {
					if !errors.Is(err, want) {
						t.Errorf("SaveProfiles error = %v, want it to wrap %v", err, want)
					}
				}

				if err == nil || !strings.Contains(err.Error(), tc.wantInErr) {
					t.Errorf("SaveProfiles error = %v, want it to contain %s", err, tc.wantInErr)
				}

				if tc.wantFile {
					return
				}

				entries, readErr := os.ReadDir(dir)
				if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("reading the directory: %v", readErr)
				}

				if len(entries) != 0 {
					t.Errorf("directory holds %d entries, want none", len(entries))
				}
			})
		}
	})

	t.Run("should flush the directory only where the platform can", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			goos      string
			wantOpens int
		}{
			{name: "should save when the directory cannot be opened for a flush", goos: "linux", wantOpens: 1},
			{name: "should not open the directory on windows", goos: "windows"},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				dir := filepath.Join(t.TempDir(), "onesie")
				path := filepath.Join(dir, "profiles.json")

				var opens []string

				open := func(name string) (*os.File, error) {
					opens = append(opens, name)

					return nil, errors.New("stub open failure")
				}

				store := creds.NewStore(creds.WithGOOS(tc.goos), creds.WithOpen(open))

				if _, err := store.SaveProfiles(path, oneProfile("k")); err != nil {
					t.Fatalf("SaveProfiles: %v", err)
				}

				if len(opens) != tc.wantOpens {
					t.Fatalf("opened %v, want %d opens", opens, tc.wantOpens)
				}

				if tc.wantOpens > 0 && opens[0] != dir {
					t.Errorf("opened %s, want the directory %s", opens[0], dir)
				}

				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("reading back: %v", err)
				}

				if strings.TrimSpace(string(data)) != `{"profiles":{"work":{"provider":"typesafe","api_key":"k"}}}` {
					t.Errorf("file = %s, want the saved key", data)
				}
			})
		}
	})
}

func oneProfile(key string) creds.Profiles {
	return creds.Profiles{Profiles: map[string]creds.Profile{"work": {Provider: "typesafe", APIKey: key}}}
}

func TestQuotedName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		given string
		want  string
	}{
		{name: "should wrap a profile name in single quotes", given: "work", want: "'work'"},
		{name: "should wrap an empty name in single quotes", given: "", want: "''"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := creds.QuotedName(tc.given); got != tc.want {
				t.Errorf("QuotedName(%q) = %q, want %q", tc.given, got, tc.want)
			}
		})
	}
}

func TestValidAccountID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "should take 32 lowercase hexadecimal characters", id: cloudflareAccount, want: true},
		{name: "should take 32 uppercase hexadecimal characters", id: strings.ToUpper(cloudflareAccount), want: true},
		{name: "should refuse 31 characters", id: cloudflareAccount[:31]},
		{name: "should refuse 33 characters", id: cloudflareAccount + "0"},
		{name: "should refuse a character that is not hexadecimal", id: "g" + cloudflareAccount[1:]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := creds.ValidAccountID(tc.id); got != tc.want {
				t.Errorf("ValidAccountID(%q) = %v, want %v", tc.id, got, tc.want)
			}

			// A run passes the account to onesie-go, which checks the same rule, so the two must agree.
			_, err := onesie.New(
				onesie.WithProvider(onesie.Cloudflare()), onesie.WithAPIKey("SECRET-K"), onesie.WithAccountID(tc.id))
			if (err == nil) != tc.want {
				t.Errorf("onesie-go took %q with error %v, want it taken %v", tc.id, err, tc.want)
			}
		})
	}
}
