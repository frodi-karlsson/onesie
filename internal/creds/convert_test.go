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

func TestConvert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file creds.File
		want creds.Profiles
	}{
		{
			name: "should turn each provider into a profile named after it, with typesafe as the default",
			file: creds.File{Providers: map[string]creds.Entry{
				"typesafe": {Store: creds.StoreKeychain, Account: "typesafe@abc"},
				"levanto":  {APIKey: "k", BaseURL: "https://proxy.example"},
			}},
			want: creds.Profiles{Default: "typesafe", Profiles: map[string]creds.Profile{
				"typesafe": {Provider: "typesafe", Store: creds.StoreKeychain, Account: "typesafe@abc"},
				"levanto":  {Provider: "levanto", APIKey: "k", BaseURL: "https://proxy.example"},
			}},
		},
		{
			name: "should name a legacy keychain entry's item after its provider",
			file: creds.File{Providers: map[string]creds.Entry{"typesafe": {Store: creds.StoreKeychain}}},
			want: creds.Profiles{Default: "typesafe", Profiles: map[string]creds.Profile{
				"typesafe": {Provider: "typesafe", Store: creds.StoreKeychain, Account: "typesafe"},
			}},
		},
		{
			name: "should not make a lone entry of another provider the default",
			file: creds.File{Providers: map[string]creds.Entry{"openrouter": {APIKey: "k"}}},
			want: creds.Profiles{Profiles: map[string]creds.Profile{"openrouter": {Provider: "openrouter", APIKey: "k"}}},
		},
		{
			name: "should keep a cloudflare base url as a base url",
			file: creds.File{Providers: map[string]creds.Entry{"cloudflare": {
				APIKey: "k", BaseURL: "https://api.cloudflare.com/client/v4/accounts/" + cloudflareAccount + "/ai",
			}}},
			want: creds.Profiles{Profiles: map[string]creds.Profile{"cloudflare": {
				Provider: "cloudflare", APIKey: "k",
				BaseURL: "https://api.cloudflare.com/client/v4/accounts/" + cloudflareAccount + "/ai",
			}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := creds.Convert(tc.file); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Convert = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestStoreOpenProfiles(t *testing.T) {
	t.Parallel()

	const old = `{"providers":{"typesafe":{"store":"keychain","account":"typesafe@abc"},"levanto":{"api_key":"k"}}}`

	write := func(t *testing.T, path, contents string) {
		t.Helper()

		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("should read profiles.json and leave credentials.json alone when both exist", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, filepath.Join(dir, "profiles.json"), `{"profiles":{"w":{"provider":"typesafe","api_key":"k"}}}`)
		write(t, filepath.Join(dir, "credentials.json"), old)

		opened, err := creds.NewStore().OpenProfiles(dir)
		if err != nil || !opened.Found || len(opened.Converted) != 0 || len(opened.Profiles.Profiles) != 1 {
			t.Fatalf("OpenProfiles = %+v, %v, want the one profile and no conversion", opened, err)
		}

		if _, err := os.Stat(filepath.Join(dir, "credentials.json")); err != nil {
			t.Errorf("credentials.json is gone: %v", err)
		}
	})

	t.Run("should give no profiles when neither file exists", func(t *testing.T) {
		t.Parallel()

		opened, err := creds.NewStore().OpenProfiles(t.TempDir())
		if err != nil || opened.Found || len(opened.Profiles.Profiles) != 0 || len(opened.Converted) != 0 {
			t.Fatalf("OpenProfiles = %+v, %v, want nothing", opened, err)
		}
	})

	t.Run("should convert credentials.json once, write profiles.json and remove the old file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, filepath.Join(dir, "credentials.json"), old)

		opened, err := creds.NewStore().OpenProfiles(dir)
		if err != nil || !opened.Found {
			t.Fatalf("OpenProfiles = %+v, %v, want the converted profiles found", opened, err)
		}

		if !reflect.DeepEqual(opened.Converted, []string{"levanto", "typesafe"}) || opened.Profiles.Default != "typesafe" {
			t.Errorf("Converted = %v, default %q, want levanto and typesafe, default typesafe",
				opened.Converted, opened.Profiles.Default)
		}

		if len(opened.Warnings) != 0 {
			t.Errorf("warnings = %v, want none", opened.Warnings)
		}

		if opened.InMemory {
			t.Error("InMemory is true, want false once profiles.json is written")
		}

		if _, statErr := os.Stat(filepath.Join(dir, "credentials.json")); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("credentials.json is still there: %v", statErr)
		}

		again, err := creds.NewStore().OpenProfiles(dir)
		if err != nil || len(again.Converted) != 0 || !reflect.DeepEqual(again.Profiles, opened.Profiles) {
			t.Errorf("second OpenProfiles = %+v, %v, want the written profiles and no conversion", again, err)
		}
	})

	t.Run("should use the converted profiles for this run when profiles.json cannot be written", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, filepath.Join(dir, "credentials.json"), old)

		store := creds.NewStore(creds.WithCreateTemp(func(string, string) (*os.File, error) {
			return nil, errors.New("read only file system")
		}))

		opened, err := store.OpenProfiles(dir)
		if err != nil || !opened.Found || len(opened.Profiles.Profiles) != 2 {
			t.Fatalf("OpenProfiles = %+v, %v, want the two converted profiles", opened, err)
		}

		if len(opened.Warnings) != 1 || !strings.Contains(opened.Warnings[0].Error(), "for this run only") {
			t.Fatalf("warnings = %v, want one naming this run only", opened.Warnings)
		}

		if !opened.InMemory {
			t.Error("InMemory is false, want true when profiles.json was not written")
		}

		if count := strings.Count(opened.Warnings[0].Error(), "onesie: "); count != 1 {
			t.Errorf("warning = %v, want the onesie prefix once, got it %d times", opened.Warnings[0], count)
		}

		if _, err := os.Stat(filepath.Join(dir, "credentials.json")); err != nil {
			t.Errorf("credentials.json is gone, so the next run cannot convert: %v", err)
		}
	})

	t.Run("should warn and go on when credentials.json cannot be removed", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		oldPath := filepath.Join(dir, "credentials.json")
		write(t, oldPath, old)

		store := creds.NewStore(creds.WithRemove(func(path string) error {
			if path == oldPath {
				return errors.New("busy")
			}

			return os.Remove(path)
		}))

		opened, err := store.OpenProfiles(dir)
		if err != nil || len(opened.Warnings) != 1 || !strings.Contains(opened.Warnings[0].Error(), "could not remove") {
			t.Fatalf("OpenProfiles = %+v, %v, want one warning naming the removal", opened, err)
		}

		if _, err := os.Stat(filepath.Join(dir, "profiles.json")); err != nil {
			t.Errorf("profiles.json was not written: %v", err)
		}
	})

	t.Run("should count a credentials.json another process removed first as done", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		oldPath := filepath.Join(dir, "credentials.json")
		write(t, oldPath, old)

		store := creds.NewStore(creds.WithRemove(func(path string) error {
			if path == oldPath {
				if err := os.Remove(path); err != nil {
					return err
				}

				return os.ErrNotExist
			}

			return os.Remove(path)
		}))

		opened, err := store.OpenProfiles(dir)
		if err != nil || len(opened.Warnings) != 0 {
			t.Fatalf("OpenProfiles = %+v, %v, want no warning", opened, err)
		}
	})

	t.Run("should refuse an invalid profiles.json and leave credentials.json alone", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		profilesPath := filepath.Join(dir, "profiles.json")
		broken := `{"profiles":{"work":{"provider":"typesafe"}}}`
		write(t, profilesPath, broken)
		write(t, filepath.Join(dir, "credentials.json"), old)

		_, err := creds.NewStore().OpenProfiles(dir)

		var invalid *creds.InvalidProfilesError
		if !errors.As(err, &invalid) {
			t.Fatalf("error = %v, want an InvalidProfilesError", err)
		}

		if _, statErr := os.Stat(filepath.Join(dir, "credentials.json")); statErr != nil {
			t.Errorf("credentials.json is gone: %v", statErr)
		}

		data, readErr := os.ReadFile(profilesPath)
		if readErr != nil || string(data) != broken {
			t.Errorf("profiles.json = %s, %v, want it unchanged", data, readErr)
		}
	})

	t.Run("should convert nothing from a credentials.json today's Load refuses", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, filepath.Join(dir, "credentials.json"), `{"providers":{}}`)

		if _, err := creds.NewStore().OpenProfiles(dir); err == nil {
			t.Fatal("OpenProfiles took a credential file Load refuses")
		}

		if _, err := os.Stat(filepath.Join(dir, "profiles.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("profiles.json was written: %v", err)
		}
	})

	t.Run("should convert nothing from a credentials.json others can reach", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("windows carries no unix permission bits")
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(old), 0o644); err != nil {
			t.Fatal(err)
		}

		_, err := creds.NewStore().OpenProfiles(dir)

		var readable *creds.ReadableError
		if !errors.As(err, &readable) {
			t.Fatalf("error = %v, want a ReadableError", err)
		}
	})

	t.Run("should refuse a converted provider the profiles file cannot hold", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, filepath.Join(dir, "credentials.json"), `{"providers":{"nope":{"api_key":"k"}}}`)

		_, err := creds.NewStore().OpenProfiles(dir)

		var invalid *creds.InvalidProfilesError
		if !errors.As(err, &invalid) || !strings.Contains(err.Error(), "credential file") ||
			!strings.Contains(err.Error(), "credentials.json") {
			t.Fatalf("error = %v, want an InvalidProfilesError naming the credential file", err)
		}
	})
}

func TestStoreSaveProfilesIn(t *testing.T) {
	t.Parallel()

	profiles := creds.Profiles{Profiles: map[string]creds.Profile{"work": {Provider: "typesafe", APIKey: "k"}}}

	t.Run("should write profiles.json and remove a leftover credentials.json", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}

		warnings, err := creds.NewStore().SaveProfilesIn(dir, profiles)
		if err != nil || len(warnings) != 0 {
			t.Fatalf("SaveProfilesIn = %v, %v, want no warning", warnings, err)
		}

		if _, statErr := os.Stat(filepath.Join(dir, "credentials.json")); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("credentials.json is still there: %v", statErr)
		}

		if _, statErr := os.Stat(filepath.Join(dir, "profiles.json")); statErr != nil {
			t.Errorf("profiles.json was not written: %v", statErr)
		}
	})

	t.Run("should give no warning when there is no credentials.json", func(t *testing.T) {
		t.Parallel()

		warnings, err := creds.NewStore().SaveProfilesIn(t.TempDir(), profiles)
		if err != nil || len(warnings) != 0 {
			t.Fatalf("SaveProfilesIn = %v, %v, want no warning", warnings, err)
		}
	})

	t.Run("should warn that the target of a symlinked credentials.json still holds the key", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("a symlink needs privileges on windows")
		}

		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "real.json")
		if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(target, filepath.Join(dir, "credentials.json")); err != nil {
			t.Fatal(err)
		}

		warnings, err := creds.NewStore().SaveProfilesIn(dir, profiles)
		if err != nil || len(warnings) != 1 {
			t.Fatalf("SaveProfilesIn = %v, %v, want one warning", warnings, err)
		}

		if !strings.Contains(warnings[0].Error(), target) || !strings.Contains(warnings[0].Error(), "still holds the key") {
			t.Errorf("warning = %v, want it to name %s and say it still holds the key", warnings[0], target)
		}

		if _, statErr := os.Stat(target); statErr != nil {
			t.Errorf("the target is gone: %v", statErr)
		}

		if _, statErr := os.Lstat(filepath.Join(dir, "credentials.json")); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("the link is still there: %v", statErr)
		}
	})
}

func TestStoreClearProfilesIn(t *testing.T) {
	t.Parallel()

	write := func(t *testing.T, dir, name string) {
		t.Helper()

		if err := os.WriteFile(filepath.Join(dir, name), []byte(`{}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	gone := func(t *testing.T, path string) {
		t.Helper()

		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("%s is still there: %v", filepath.Base(path), statErr)
		}
	}

	t.Run("should remove profiles.json and a leftover credentials.json", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, dir, "profiles.json")
		write(t, dir, "credentials.json")

		warnings, err := creds.NewStore().ClearProfilesIn(dir)
		if err != nil || len(warnings) != 0 {
			t.Fatalf("ClearProfilesIn = %v, %v, want no warning", warnings, err)
		}

		gone(t, filepath.Join(dir, "profiles.json"))
		gone(t, filepath.Join(dir, "credentials.json"))
	})

	t.Run("should remove a credentials.json whose conversion was never written", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, dir, "credentials.json")

		warnings, err := creds.NewStore().ClearProfilesIn(dir)
		if err != nil || len(warnings) != 0 {
			t.Fatalf("ClearProfilesIn = %v, %v, want no warning", warnings, err)
		}

		gone(t, filepath.Join(dir, "credentials.json"))
	})

	t.Run("should refuse a directory at profiles.json and name it as the profiles file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "profiles.json"), 0o700); err != nil {
			t.Fatal(err)
		}

		_, err := creds.NewStore().ClearProfilesIn(dir)
		if err == nil || !strings.Contains(err.Error(), "profiles file") || !strings.Contains(err.Error(), "is not a regular file") {
			t.Errorf("error = %v, want the profiles file refused as not a regular file", err)
		}
	})

	t.Run("should give no warning when neither file is there", func(t *testing.T) {
		t.Parallel()

		warnings, err := creds.NewStore().ClearProfilesIn(t.TempDir())
		if err != nil || len(warnings) != 0 {
			t.Fatalf("ClearProfilesIn = %v, %v, want no warning", warnings, err)
		}
	})

	t.Run("should warn when credentials.json cannot be removed and still remove profiles.json", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		write(t, dir, "profiles.json")
		write(t, dir, "credentials.json")

		store := creds.NewStore(creds.WithRemove(func(path string) error {
			if filepath.Base(path) == "credentials.json" {
				return errors.New("the disk is read only")
			}

			return os.Remove(path)
		}))

		warnings, err := store.ClearProfilesIn(dir)
		if err != nil || len(warnings) != 1 {
			t.Fatalf("ClearProfilesIn = %v, %v, want one warning", warnings, err)
		}

		message := warnings[0].Error()
		if !strings.Contains(message, "could not remove "+filepath.Join(dir, "credentials.json")) ||
			strings.Contains(message, "converted") {
			t.Errorf("warning = %q, want it to name the removal and nothing converted", message)
		}

		gone(t, filepath.Join(dir, "profiles.json"))
	})
}
