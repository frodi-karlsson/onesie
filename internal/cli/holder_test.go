package cli

import (
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestOpenProfiles(t *testing.T) {
	t.Parallel()

	const both = `{"providers":{"levanto":{"api_key":"SECRET-LV"},` +
		`"typesafe":{"store":"keychain","account":"typesafe@abc"}}}`

	const converted = "onesie: converted credentials.json into profiles.json, with profiles 'levanto' and 'typesafe'. " +
		"'typesafe' is the default"

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
			want: []string{"onesie: converted credentials.json into profiles.json, with profile 'levanto'. " +
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

			settings := defaultSettings(BuildInfo{})
			settings.profilesPath = fixedPath(filepath.Join(dir, "profiles.json"))
			settings.credStore = creds.NewStore()
			settings.lookupEnv = lookupFrom(tc.env)

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

		settings := defaultSettings(BuildInfo{})
		settings.profilesPath = fixedPath(filepath.Join(dir, "profiles.json"))
		settings.credStore = creds.NewStore(creds.WithCreateTemp(func(string, string) (*os.File, error) {
			return nil, errors.New("the disk is full")
		}))
		settings.lookupEnv = lookupFrom(nil)

		opened, err := openProfiles(settings, &out)
		if err != nil {
			t.Fatalf("openProfiles returned an error: %v", err)
		}

		if _, ok := opened.Profiles.Profiles["levanto"]; !ok {
			t.Error("the converted profiles were not kept for the run")
		}

		const inMemory = "onesie: converted credentials.json for this run, with profiles 'levanto' and 'typesafe'. " +
			"'typesafe' is the default"

		got := linesOf(out.String())
		if len(got) != 2 || got[0] != inMemory || !strings.Contains(got[1], "is converted for this run only") {
			t.Errorf("stderr lines = %q, want the conversion for this run and the warning", got)
		}
	})

	t.Run("should find no profiles when the config dir cannot be resolved", func(t *testing.T) {
		t.Parallel()

		settings := defaultSettings(BuildInfo{})
		settings.profilesPath = func() (string, error) { return "", errors.New("onesie: cannot find a home directory") }
		settings.credStore = creds.NewStore()
		settings.lookupEnv = lookupFrom(nil)

		opened, err := openProfiles(settings, io.Discard)
		if err != nil || opened.Found || len(opened.Profiles.Profiles) != 0 {
			t.Errorf("openProfiles found %d profiles, found %v, error %v, want none and no error",
				len(opened.Profiles.Profiles), opened.Found, err)
		}
	})
}

func TestNewHolder(t *testing.T) {
	t.Parallel()

	const (
		openrouterWork = `{"default":"work","profiles":{"work":{"provider":"openrouter","api_key":"SECRET-WORK",` +
			`"model":"typesafe/jev-1.13"}}}`
		noDefault   = `{"profiles":{"work":{"provider":"openrouter","api_key":"SECRET-WORK"}}}`
		oldTypesafe = `{"providers":{"typesafe":{"api_key":"SECRET-OLD"}}}`
		oldKeychain = `{"providers":{"openrouter":{"store":"keychain","account":"openrouter"}}}`
	)

	asking := func(extra ...string) []string {
		return append([]string{"is this urgent", "--state", "the server is down"}, extra...)
	}
	printing := func(extra ...string) []string {
		return append([]string{"--ask", "u=is it urgent", "--print-questions"}, extra...)
	}
	mock := writeMock(t, `{"answer":0.9}`)

	tests := []struct {
		name     string
		file     string
		mode     os.FileMode
		unixOnly bool
		old      string
		args     []string
		env      map[string]string
		wantCode int
		wantOut  string
		wantErr  []string
		notErr   []string
		wantFile bool
		noFile   bool
	}{
		{
			name: "should send the default profile's model on --print-request", file: openrouterWork,
			args: asking("--print-request"), wantOut: `"model":"typesafe/jev-1.13"`,
		},
		{
			name: "should let -m beat the profile's model", file: openrouterWork,
			args: asking("--print-request", "-m", "other"), wantOut: `"model":"other"`,
		},
		{
			name: "should exit 3 on --print-request when profiles.json is open to others", file: openrouterWork,
			mode: 0o644, unixOnly: true, args: asking("--print-request"), wantCode: ExitAuth,
			wantErr: []string{"profiles file", "is readable or writable by its group or others, mode 644"},
		},
		{
			name: "should exit 3 under --mock when profiles.json is open to others", file: openrouterWork,
			mode: 0o644, unixOnly: true, args: asking("--mock", mock), wantCode: ExitAuth,
			wantErr: []string{"is readable or writable by its group or others, mode 644"},
		},
		{
			name: "should refuse an empty --profile", args: asking("--print-request", "--profile", ""),
			wantCode: ExitUsage, wantErr: []string{"onesie: --profile is empty"},
		},
		{
			name: "should refuse a whitespace only --provider", args: asking("--print-request", "--provider", " "),
			wantCode: ExitUsage, wantErr: []string{"onesie: --provider is empty"},
		},
		{
			name: "should name the fixes when there are profiles and no default", file: noDefault,
			args: asking("--print-request"), wantCode: ExitUsage,
			wantErr: []string{"Run onesie profile use NAME, or pass --profile NAME or --provider NAME"},
		},
		{
			name: "should exit 2 for a bare run after converting a lone levanto entry",
			old:  `{"providers":{"levanto":{"api_key":"SECRET-LV"}}}`, args: asking("-r"), wantCode: ExitUsage,
			wantFile: true,
			wantErr: []string{
				"onesie: converted credentials.json into profiles.json, with profile 'levanto'. No profile is the default",
				"onesie: no profile is the default and nothing chose one. " +
					"Run onesie profile use NAME, or pass --profile NAME or --provider NAME",
			},
		},
		{
			name: "should name the profiles there are for a missing one", file: openrouterWork,
			args: asking("--print-request", "--profile", "nope"), wantCode: ExitUsage,
			wantErr: []string{"onesie: --profile names profile 'nope', which does not exist. The profiles are 'work'"},
		},
		{
			name: "should point --provider at --profile when it names a profile", file: openrouterWork,
			args: asking("--print-request", "--provider", "work"), wantCode: ExitUsage,
			wantErr: []string{"onesie: unknown provider 'work'", "'work' is a profile, so pass --profile 'work' in place of --provider"},
		},
		{
			name: "should point ONESIE_PROVIDER at ONESIE_PROFILE when it names a profile", file: openrouterWork,
			env: map[string]string{"ONESIE_PROVIDER": "work"}, args: asking("--print-request"), wantCode: ExitUsage,
			wantErr: []string{"'work' is a profile, so set ONESIE_PROFILE='work' in place of ONESIE_PROVIDER"},
		},
		{
			name: "should keep the unknown provider error when profiles.json cannot be read for the hint",
			file: openrouterWork, mode: 0o644, unixOnly: true, args: asking("--print-request", "--provider", "work"),
			wantCode: ExitUsage, wantErr: []string{"onesie: unknown provider 'work'"},
			notErr: []string{"is a profile", "is readable or writable by its group or others"},
		},
		{
			name: "should keep the missing key error when profiles.json cannot be read for the hint",
			file: openrouterWork, mode: 0o644, unixOnly: true, args: asking("--provider", "openrouter", "-r"),
			wantCode: ExitUsage, wantErr: []string{"no API key. Set OPENROUTER_API_KEY"},
			notErr: []string{"--profile work", "is readable or writable by its group or others"},
		},
		{
			name: "should name profile create for a run with no key and no profiles", args: asking("-r"),
			wantCode: ExitUsage,
			wantErr: []string{"onesie: no API key. Set TYPESAFE_API_KEY, or store one with onesie profile create NAME " +
				"--provider typesafe"},
		},
		{
			name: "should convert credentials.json on --print-request", old: oldTypesafe,
			args: asking("--print-request"), wantOut: `"model":"jev-latest"`, wantFile: true,
			wantErr: []string{"onesie: converted credentials.json into profiles.json, with profile 'typesafe'"},
		},
		{
			name: "should not convert credentials.json on --print-questions", old: oldTypesafe,
			args: printing(), wantOut: "u:", notErr: []string{"converted"}, noFile: true,
		},
		{
			name: "should name the converted profile when --provider finds no key", old: oldKeychain,
			args: asking("--provider", "openrouter", "-r"), wantCode: ExitUsage, wantFile: true,
			wantErr: []string{
				"onesie: converted credentials.json into profiles.json, with profile 'openrouter'",
				"no API key. Set OPENROUTER_API_KEY, or drop --provider and pass --profile 'openrouter'",
			},
		},
		{
			name: "should name the converted profile when ONESIE_PROVIDER finds no key", old: oldKeychain,
			env: map[string]string{"ONESIE_PROVIDER": "openrouter"}, args: asking("-r"), wantCode: ExitUsage,
			wantFile: true,
			wantErr: []string{
				"onesie: ONESIE_PROVIDER is set, so a run uses no profile",
				"no API key. Set OPENROUTER_API_KEY, or unset ONESIE_PROVIDER and run onesie profile use 'openrouter', " +
					"or run with --profile 'openrouter'",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode check does not apply")
			}

			dir := t.TempDir()
			writeConfig(t, dir, "profiles.json", tc.file, tc.mode)
			writeConfig(t, dir, "credentials.json", tc.old, 0)

			out, errOut, code := runInConfig(t, dir, tc.args, "", tc.env)

			assertNoSecret(t, out, errOut)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
			}

			if !strings.Contains(out, tc.wantOut) {
				t.Errorf("stdout = %q, want it to contain %q", out, tc.wantOut)
			}

			for _, want := range tc.wantErr {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, want)
				}
			}

			for _, unwanted := range tc.notErr {
				if strings.Contains(errOut, unwanted) {
					t.Errorf("stderr = %q, want no %q", errOut, unwanted)
				}
			}

			_, statErr := os.Stat(filepath.Join(dir, "profiles.json"))

			if tc.wantFile && statErr != nil {
				t.Errorf("profiles.json is missing, stat error = %v", statErr)
			}

			if tc.noFile && !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("profiles.json was written, stat error = %v", statErr)
			}
		})
	}

	t.Run("should never open the file when the provider is named and the run succeeds", func(t *testing.T) {
		t.Parallel()

		refusing := creds.NewStore(creds.WithOpen(func(path string) (*os.File, error) {
			t.Errorf("the run opened %s", path)

			return nil, errors.New("the test store refuses every call")
		}))

		namings := []struct {
			args []string
			env  map[string]string
		}{
			{args: []string{"--provider", "typesafe"}},
			{env: map[string]string{"ONESIE_PROVIDER": "typesafe"}},
		}

		for _, naming := range namings {
			srv, auths := recordingServer(t, 0)

			env := map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"}
			maps.Copy(env, naming.env)

			args := append(append([]string{}, naming.args...),
				"is this urgent", "-r", "--state", "the server is down", "--base-url", srv.URL)

			out, errOut, code := runInConfig(t, t.TempDir(), args, "", env, withCredentialStore(refusing))

			assertNoSecret(t, out, errOut)

			if code != ExitOK {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			// Compared, never printed, since the header carries the key.
			if got := auths(); len(got) != 1 || got[0] != "Bearer SECRET-ENV" {
				t.Errorf("the run sent %d requests, want one carrying the environment key", len(got))
			}
		}
	})
}

func TestResolver_Resolved(t *testing.T) {
	t.Parallel()

	profiles := creds.Profiles{Default: "work", Profiles: map[string]creds.Profile{
		"work": {Provider: "openrouter", APIKey: "SECRET-WORK"},
	}}
	unreadable := errors.New("profiles.json is readable by others")
	refusedChoice := errors.New("the settings could not be read")

	tests := []struct {
		name         string
		given        Given
		givenErr     error
		env          map[string]string
		openErr      error
		neverOpen    bool
		wantProfile  string
		wantProvider string
		wantBy       string
		wantKeyFrom  string
		wantErr      string
		wantIs       error
		wantRefused  *Chosen
	}{
		{
			name: "should take the default profile when nothing chose", wantProfile: "work",
			wantProvider: "openrouter", wantBy: "the default", wantKeyFrom: KeyFromFile,
		},
		{
			name: "should take the profile --profile names", given: Given{Profile: "work", ProfileSet: true},
			wantProfile: "work", wantProvider: "openrouter", wantBy: "--profile", wantKeyFrom: KeyFromFile,
		},
		{
			name:    "should pass a refused choice on",
			given:   Given{Profile: "work", ProfileSet: true, Provider: "berget", ProviderSet: true},
			wantErr: "--profile and --provider both choose what the run uses. Pass one",
		},
		{
			name:     "should pass on the error the given choice failed with",
			givenErr: refusedChoice, neverOpen: true,
			wantErr: refusedChoice.Error(), wantIs: refusedChoice,
		},
		{
			name:  "should run without a profile under --provider and ignore the profile's key",
			given: Given{Provider: "openrouter", ProviderSet: true}, wantProvider: "openrouter",
			wantBy: "--provider", wantKeyFrom: KeyFromNone, neverOpen: true,
		},
		{
			name:         "should take the key from the provider's variable under ONESIE_PROVIDER",
			env:          map[string]string{"ONESIE_PROVIDER": "openrouter", "OPENROUTER_API_KEY": "SECRET-OR"},
			wantProvider: "openrouter", wantBy: "ONESIE_PROVIDER", wantKeyFrom: KeyFromEnv, neverOpen: true,
		},
		{
			name: "should refuse an unknown provider", given: Given{Provider: "bogus", ProviderSet: true},
			wantErr:   "onesie: unknown provider 'bogus'. Use typesafe, openrouter, berget, levanto or cloudflare",
			neverOpen: true,
		},
		{
			name:  "should return a ProviderError carrying the given choice without opening the file",
			given: Given{Provider: "work", ProviderSet: true}, neverOpen: true,
			wantErr:     "onesie: unknown provider 'work'. Use typesafe, openrouter, berget, levanto or cloudflare",
			wantRefused: &Chosen{Provider: "work", By: "--provider"},
		},
		{
			name: "should return the error opening the file for a run that needs a profile", openErr: unreadable,
			wantErr: unreadable.Error(), wantIs: unreadable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			open := func() (creds.Opened, error) {
				if tc.neverOpen {
					t.Error("the resolver opened the profiles file")
				}

				return creds.Opened{Profiles: profiles}, tc.openErr
			}
			given := func() (Given, error) { return tc.given, tc.givenErr }

			got, err := NewResolver(given, open, lookupFrom(tc.env), plan.Flags).Resolved()
			if tc.wantErr != "" {
				assertRefusal(t, err, tc.wantErr, tc.wantIs, tc.wantRefused)

				return
			}

			if err != nil {
				t.Fatalf("Resolved returned an error: %v", err)
			}

			if got.Profile != tc.wantProfile || got.Provider.Name() != tc.wantProvider || got.By != tc.wantBy ||
				got.KeyFrom != tc.wantKeyFrom {
				t.Errorf("resolution = profile %q, provider %q, by %q, key from %q, want %q, %q, %q, %q",
					got.Profile, got.Provider.Name(), got.By, got.KeyFrom,
					tc.wantProfile, tc.wantProvider, tc.wantBy, tc.wantKeyFrom)
			}
		})
	}
}

func assertRefusal(t *testing.T, err error, want string, is error, refused *Chosen) {
	t.Helper()

	if err == nil || err.Error() != want || (is != nil && !errors.Is(err, is)) {
		t.Errorf("error = %v, want %q", err, want)
	}

	var provider *ProviderError
	if refused != nil && (!errors.As(err, &provider) || provider.Given != *refused) {
		t.Errorf("error = %#v, want a ProviderError carrying %+v", err, *refused)
	}
}

func linesOf(text string) []string {
	if text == "" {
		return nil
	}

	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}
