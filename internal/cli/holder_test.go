package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestOpenProfiles(t *testing.T) {
	t.Parallel()

	const both = `{"providers":{"levanto":{"api_key":"SECRET-LV"},` +
		`"typesafe":{"store":"keychain","account":"typesafe@abc"}}}`

	const converted = "onesie: converted credentials.json into profiles.json, with profiles levanto and typesafe. " +
		"typesafe is the default"

	tests := []struct {
		name string
		old  string
		file string
		env  map[string]string
		want []string
	}{
		{name: "should name the converted profiles and the default", old: both, want: []string{converted}},
		{
			name: "should say a run under ONESIE_PROVIDER uses no profile", old: both,
			env: map[string]string{"ONESIE_PROVIDER": "openrouter"},
			want: []string{converted, "onesie: ONESIE_PROVIDER is set, so a run uses no profile and reads only " +
				"the provider's key variable. To use a stored key, unset it and run onesie profile use NAME, " +
				"or pass --profile NAME"},
		},
		{
			name: "should say no profile is the default for a lone entry of another provider",
			old:  `{"providers":{"levanto":{"api_key":"SECRET-LV"}}}`,
			want: []string{"onesie: converted credentials.json into profiles.json, with profile levanto. " +
				"No profile is the default, so run onesie profile use NAME to choose one"},
		},
		{name: "should print nothing when profiles.json is there", old: both, file: `{"profiles":{}}`},
		{name: "should print nothing when neither file is there"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeConfig(t, dir, "credentials.json", tc.old, 0)
			writeConfig(t, dir, "profiles.json", tc.file, 0)

			var out bytes.Buffer

			settings := rootSettings{
				profilesPath: fixedPath(filepath.Join(dir, "profiles.json")),
				credStore:    creds.NewStore(),
				lookupEnv:    lookupFrom(tc.env),
			}

			if _, err := openProfiles(settings, &out); err != nil {
				t.Fatalf("openProfiles returned an error: %v", err)
			}

			if got := linesOf(out.String()); !slices.Equal(got, tc.want) {
				t.Errorf("stderr lines = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("should warn and keep the converted profiles when profiles.json cannot be written", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		writeConfig(t, dir, "credentials.json", both, 0)

		var out bytes.Buffer

		settings := rootSettings{
			profilesPath: fixedPath(filepath.Join(dir, "profiles.json")),
			credStore: creds.NewStore(creds.WithCreateTemp(func(string, string) (*os.File, error) {
				return nil, errors.New("the disk is full")
			})),
			lookupEnv: lookupFrom(nil),
		}

		opened, err := openProfiles(settings, &out)
		if err != nil {
			t.Fatalf("openProfiles returned an error: %v", err)
		}

		if _, ok := opened.Profiles.Profiles["levanto"]; !ok {
			t.Error("the converted profiles were not kept for the run")
		}

		const inMemory = "onesie: converted credentials.json for this run, with profiles levanto and typesafe. " +
			"typesafe is the default"

		got := linesOf(out.String())
		if len(got) != 2 || got[0] != inMemory || !strings.Contains(got[1], "is converted for this run only") {
			t.Errorf("stderr lines = %q, want the conversion for this run and the warning", got)
		}
	})

	t.Run("should find no profiles when the config dir cannot be resolved", func(t *testing.T) {
		t.Parallel()

		settings := rootSettings{
			profilesPath: func() (string, error) { return "", errors.New("onesie: cannot find a home directory") },
			credStore:    creds.NewStore(),
			lookupEnv:    lookupFrom(nil),
		}

		opened, err := openProfiles(settings, io.Discard)
		if err != nil || opened.Found || len(opened.Profiles.Profiles) != 0 {
			t.Errorf("openProfiles found %d profiles, found %v, error %v, want none and no error",
				len(opened.Profiles.Profiles), opened.Found, err)
		}
	})
}

func linesOf(text string) []string {
	if text == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}
