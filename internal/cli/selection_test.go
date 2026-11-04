package cli

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/resolve"
)

const testAccountID = "76a0a6ac260b32ec3c8703ba592e769f"

func TestCheckChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   runFlags
		env     map[string]string
		wantErr string
	}{
		{name: "should pass a known provider", flags: runFlags{provider: "berget", providerSet: true}},
		{name: "should pass nothing chosen"},
		{
			name:  "should pass a profile without checking that it exists",
			flags: runFlags{profile: "missing", profileSet: true},
		},
		{
			name:    "should refuse an unknown provider",
			flags:   runFlags{provider: "nope", providerSet: true},
			wantErr: "onesie: unknown provider 'nope'",
		},
		{
			name:    "should refuse an unknown provider from ONESIE_PROVIDER",
			env:     map[string]string{"ONESIE_PROVIDER": "nope"},
			wantErr: "onesie: unknown provider 'nope'",
		},
		{
			name:    "should refuse two choices of the same strength",
			flags:   runFlags{profile: "x", profileSet: true, provider: "typesafe", providerSet: true},
			wantErr: "both choose what the run uses",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := checkChoice(&tc.flags, lookupFrom(tc.env))
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("checkChoice returned an error: %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}

	t.Run("should check the choice of a --print-questions run without the file", func(t *testing.T) {
		t.Parallel()

		printing := func(extra ...string) []string {
			return append([]string{"--ask", "u=is it urgent", "--print-questions"}, extra...)
		}

		runs := []struct {
			name     string
			file     string
			mode     os.FileMode
			args     []string
			wantCode int
			wantOut  string
			wantErr  string
		}{
			{
				name: "should not read a profiles file open to others",
				file: `{"default":"work","profiles":{"work":{"provider":"openrouter","api_key":"SECRET-WORK"}}}`,
				mode: 0o644, args: printing(), wantOut: "u:",
			},
			{
				name: "should refuse --profile beside --provider", args: printing("--profile", "x", "--provider", "typesafe"),
				wantCode: ExitUsage, wantErr: "onesie: --profile and --provider both choose what the run uses",
			},
			{
				name: "should not check that a profile exists", args: printing("--profile", "missing"), wantOut: "u:",
			},
			{
				name: "should refuse an unknown provider", args: printing("--provider", "bogus"),
				wantCode: ExitUsage, wantErr: "onesie: unknown provider 'bogus'",
			},
		}

		for _, tc := range runs {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				writeConfig(t, dir, "profiles.json", tc.file, tc.mode)

				out, errOut, code := runInConfig(t, dir, tc.args, "", nil)

				assertNoSecret(t, out, errOut)

				if code != tc.wantCode {
					t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
				}

				if !strings.Contains(out, tc.wantOut) || !strings.Contains(errOut, tc.wantErr) {
					t.Errorf("stdout = %q, stderr = %q, want %q and %q", out, errOut, tc.wantOut, tc.wantErr)
				}
			})
		}
	})
}

func TestWithProfileHints(t *testing.T) {
	t.Parallel()

	withDefault := creds.Profiles{
		Default: "work",
		Profiles: map[string]creds.Profile{
			"work": {
				Provider: "openrouter", Store: creds.StoreKeychain, Account: "work@abc", KeyEnd: "3f9a",
				Model: "typesafe/jev",
			},
			"cf": {Provider: "cloudflare", APIKey: "SECRET-CF", AccountID: testAccountID},
		},
	}
	noDefault := creds.Profiles{Profiles: withDefault.Profiles}
	empty := creds.Profiles{Profiles: map[string]creds.Profile{}}

	const unknownWork = "unknown provider 'work'. Use typesafe, openrouter, berget, levanto or cloudflare"

	tests := []struct {
		name     string
		chosen   resolve.Chosen
		profiles creds.Profiles
		openErr  error
		wantErr  string
	}{
		{
			name: "should point --provider at --profile when it names a profile", profiles: withDefault,
			chosen:  resolve.Chosen{Provider: "work", By: "--provider"},
			wantErr: unknownWork + ". 'work' is a profile, so pass --profile 'work' in place of --provider",
		},
		{
			name: "should point ONESIE_PROVIDER at ONESIE_PROFILE when it names a profile", profiles: withDefault,
			chosen: resolve.Chosen{Provider: "work", By: "ONESIE_PROVIDER"},
			wantErr: unknownWork + ". 'work' is a profile, so set ONESIE_PROFILE='work' in place of " +
				"ONESIE_PROVIDER",
		},
		{
			name:    "should keep the provider's error when the file cannot be opened for the hint",
			chosen:  resolve.Chosen{Provider: "work", By: "--provider"},
			openErr: errors.New("profiles.json is readable by others"),
			wantErr: unknownWork,
		},
		{
			name: "should name the profiles there are for a missing one", profiles: withDefault,
			chosen:  resolve.Chosen{Profile: "nope", By: "ONESIE_PROFILE"},
			wantErr: "ONESIE_PROFILE names profile 'nope', which does not exist. The profiles are 'cf', 'work'",
		},
		{
			name: "should hint --provider for a --profile that names a provider", profiles: withDefault,
			chosen: resolve.Chosen{Profile: "levanto", By: "--profile"},
			wantErr: "--profile names profile 'levanto', which does not exist. The profiles are 'cf', 'work'. " +
				"'levanto' is a provider, so pass --provider levanto in place of --profile",
		},
		{
			name: "should hint ONESIE_PROVIDER for an ONESIE_PROFILE that names a provider", profiles: withDefault,
			chosen: resolve.Chosen{Profile: "berget", By: "ONESIE_PROFILE"},
			wantErr: "ONESIE_PROFILE names profile 'berget', which does not exist. The profiles are 'cf', 'work'. " +
				"'berget' is a provider, so set ONESIE_PROVIDER=berget in place of ONESIE_PROFILE",
		},
		{
			name: "should give no provider hint for a name that differs from one only in case", profiles: withDefault,
			chosen:  resolve.Chosen{Profile: "Levanto", By: "--profile"},
			wantErr: "--profile names profile 'Levanto', which does not exist. The profiles are 'cf', 'work'",
		},
		{
			name: "should say there are no profiles for a missing one", profiles: empty,
			chosen: resolve.Chosen{Profile: "nope", By: "--profile"},
			wantErr: "--profile names profile 'nope', which does not exist. There are no profiles. " +
				"Run onesie profile create NAME --provider PROVIDER to make one",
		},
		{
			name: "should name the fixes when there are profiles and no default", profiles: noDefault,
			wantErr: "no profile is the default and nothing chose one. " +
				"Run onesie profile use NAME, or pass --profile NAME or --provider NAME",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			opened := func() (creds.Opened, error) { return creds.Opened{Profiles: tc.profiles}, tc.openErr }

			got := withProfileHints(refusal(tc.chosen, tc.profiles), opened)
			if got == nil || text(got) != tc.wantErr {
				t.Errorf("error = %v, want %q", got, tc.wantErr)
			}
		})
	}
}

func refusal(chosen resolve.Chosen, profiles creds.Profiles) error {
	if chosen.Provider != "" {
		_, err := resolve.ProviderRun(chosen.Provider, chosen.By, lookupFrom(nil))

		return &resolve.ProviderError{Given: chosen, Err: err}
	}

	_, err := resolve.Profile(chosen, profiles, lookupFrom(nil))

	return err
}

func TestFirstProfileOf(t *testing.T) {
	t.Parallel()

	both := map[string]creds.Profile{
		"personal": {Provider: "openrouter", APIKey: "SECRET-HOME"},
		"work":     {Provider: "openrouter", APIKey: "SECRET-WORK"},
		"ts":       {Provider: "typesafe", APIKey: "SECRET-TS"},
	}

	tests := []struct {
		name     string
		profiles creds.Profiles
		provider string
		want     string
	}{
		{
			name:     "should prefer the default when its provider matches",
			profiles: creds.Profiles{Default: "work", Profiles: both}, provider: "openrouter", want: "work",
		},
		{
			name:     "should take the first by name when the default has another provider",
			profiles: creds.Profiles{Default: "ts", Profiles: both}, provider: "openrouter", want: "personal",
		},
		{
			name:     "should take the first by name when there is no default",
			profiles: creds.Profiles{Profiles: both}, provider: "openrouter", want: "personal",
		},
		{
			name:     "should name nothing when no profile has the provider",
			profiles: creds.Profiles{Default: "work", Profiles: both}, provider: "berget",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := firstProfileOf(tc.provider, tc.profiles); got != tc.want {
				t.Errorf("firstProfileOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMissingKeyHint(t *testing.T) {
	t.Parallel()

	missing := func(provider onesie.Provider) error {
		_, err := onesie.New(onesie.WithProvider(provider), onesie.WithEnv(lookupFrom(nil)))

		return err
	}

	profiles := creds.Profiles{Profiles: map[string]creds.Profile{
		"work":     {Provider: "openrouter", APIKey: "SECRET-WORK"},
		"personal": {Provider: "openrouter", APIKey: "SECRET-HOME"},
		"ts":       {Provider: "typesafe", APIKey: "SECRET-TS"},
	}}

	bareOf := func(provider, by string, env map[string]string) resolve.Resolution {
		run, err := resolve.ProviderRun(provider, by, lookupFrom(env))
		if err != nil {
			t.Fatal(err)
		}

		return run
	}

	tests := []struct {
		name string
		err  error
		run  resolve.Resolution
		want string
	}{
		{
			name: "should name the first profile with the provider under ONESIE_PROVIDER",
			err:  missing(onesie.OpenRouter()), run: bareOf("openrouter", "ONESIE_PROVIDER", nil),
			want: "no API key. Set OPENROUTER_API_KEY, or unset ONESIE_PROVIDER and run " +
				"onesie profile use 'personal', or run with --profile 'personal'",
		},
		{
			name: "should say a stored profile is not used under ONESIE_PROVIDER when none has the provider",
			err:  missing(onesie.Levanto()), run: bareOf("levanto", "ONESIE_PROVIDER", nil),
			want: "no API key. Set LEVANTO_API_KEY. ONESIE_PROVIDER is set, so a stored profile is not " +
				"used. To use one instead, unset ONESIE_PROVIDER and run onesie profile create NAME --provider levanto",
		},
		{
			name: "should say to drop --provider under --provider",
			err:  missing(onesie.TypeSafe()), run: bareOf("typesafe", "--provider", nil),
			want: "no API key. Set TYPESAFE_API_KEY, or drop --provider and pass --profile 'ts'",
		},
		{
			name: "should name profile create when no profile has the provider",
			err:  missing(onesie.Berget()), run: bareOf("berget", "--provider", nil),
			want: "no API key. Set BERGET_API_KEY, or store one with onesie profile create NAME --provider berget",
		},
		{
			name: "should leave the error alone when the variable holds a key",
			err:  missing(onesie.OpenRouter()),
			run:  bareOf("openrouter", "--provider", map[string]string{"OPENROUTER_API_KEY": "SECRET-OR"}),
			want: "no API key. Set OPENROUTER_API_KEY",
		},
		{
			name: "should leave an unrelated validation error alone",
			err:  &onesie.ValidationError{Message: "base URL must be an absolute http or https URL, got proxy.example"},
			run:  bareOf("openrouter", "ONESIE_PROVIDER", nil),
			want: "base URL must be an absolute http or https URL, got proxy.example",
		},
		{
			name: "should leave an error that is not a validation error alone",
			err:  errors.New("the server is down"), run: bareOf("openrouter", "--provider", nil),
			want: "the server is down",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := missingKeyHint(tc.err, tc.run, profiles)
			if text(got) != tc.want {
				t.Errorf("error = %q, want %q", text(got), tc.want)
			}

			if !errors.Is(got, tc.err) {
				t.Error("the hint lost the error it wraps")
			}
		})
	}

	t.Run("should leave a profile run alone", func(t *testing.T) {
		t.Parallel()

		run, err := resolve.ProfileRun("work", profiles.Profiles["work"], "--profile")
		if err != nil {
			t.Fatal(err)
		}

		if got := missingKeyHint(missing(onesie.OpenRouter()), run, profiles); text(got) != "no API key. Set OPENROUTER_API_KEY" {
			t.Errorf("error = %q, want it unchanged", text(got))
		}
	})
}

func TestAccountNote(t *testing.T) {
	t.Parallel()

	_, noAccount := onesie.New(
		onesie.WithProvider(onesie.Cloudflare()), onesie.WithAPIKey("SECRET-CF"), onesie.WithEnv(lookupFrom(nil)))
	if !errors.Is(noAccount, onesie.ErrNoAccountID) {
		t.Fatalf("New = %v, want onesie.ErrNoAccountID", noAccount)
	}

	cf, err := resolve.ProfileRun("cf", creds.Profile{Provider: "cloudflare", APIKey: "SECRET-CF"}, "the default")
	if err != nil {
		t.Fatal(err)
	}

	bare, err := resolve.ProviderRun("cloudflare", "--provider", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		err  error
		run  resolve.Resolution
		want string
	}{
		{
			name: "should name profile update for a cloudflare profile with no account", err: noAccount, run: cf,
			want: "no cloudflare account id. Set CLOUDFLARE_ACCOUNT_ID, " +
				"or run onesie profile update 'cf' --account-id ACCOUNT_ID",
		},
		{
			name: "should name profile create for a run without a profile", err: noAccount, run: bare,
			want: "no cloudflare account id. Set CLOUDFLARE_ACCOUNT_ID, " +
				"or store it with onesie profile create NAME --provider cloudflare --account-id ACCOUNT_ID",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := accountNote(tc.err, tc.run)
			if text(got) != tc.want {
				t.Errorf("error = %q, want %q", text(got), tc.want)
			}

			if !errors.Is(got, tc.err) {
				t.Error("the note lost the error it wraps")
			}
		})
	}
}
