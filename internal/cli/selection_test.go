package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

const testAccountID = "76a0a6ac260b32ec3c8703ba592e769f"

func TestChoose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   runFlags
		env     map[string]string
		want    choice
		wantErr string
	}{
		{name: "should choose nothing when nothing is given", want: choice{}},
		{
			name:  "should choose the profile --profile names",
			flags: runFlags{profile: "work", profileSet: true},
			want:  choice{profile: "work", by: "--profile"},
		},
		{
			name:  "should choose the provider --provider names",
			flags: runFlags{provider: "openrouter", providerSet: true},
			want:  choice{provider: "openrouter", by: "--provider"},
		},
		{
			name:  "should trim the name a flag gives",
			flags: runFlags{profile: " work ", profileSet: true},
			want:  choice{profile: "work", by: "--profile"},
		},
		{
			name: "should choose the profile ONESIE_PROFILE names",
			env:  map[string]string{"ONESIE_PROFILE": "work"},
			want: choice{profile: "work", by: "ONESIE_PROFILE"},
		},
		{
			name: "should choose the provider ONESIE_PROVIDER names",
			env:  map[string]string{"ONESIE_PROVIDER": "berget"},
			want: choice{provider: "berget", by: "ONESIE_PROVIDER"},
		},
		{
			name:  "should let --profile beat ONESIE_PROVIDER",
			flags: runFlags{profile: "work", profileSet: true},
			env:   map[string]string{"ONESIE_PROVIDER": "berget"},
			want:  choice{profile: "work", by: "--profile"},
		},
		{
			name:  "should let --provider beat ONESIE_PROFILE",
			flags: runFlags{provider: "levanto", providerSet: true},
			env:   map[string]string{"ONESIE_PROFILE": "work"},
			want:  choice{provider: "levanto", by: "--provider"},
		},
		{
			name:  "should let --profile beat ONESIE_PROFILE",
			flags: runFlags{profile: "home", profileSet: true},
			env:   map[string]string{"ONESIE_PROFILE": "work"},
			want:  choice{profile: "home", by: "--profile"},
		},
		{
			name:  "should let --provider beat ONESIE_PROVIDER",
			flags: runFlags{provider: "typesafe", providerSet: true},
			env:   map[string]string{"ONESIE_PROVIDER": "berget"},
			want:  choice{provider: "typesafe", by: "--provider"},
		},
		{
			name:  "should let --profile win over both variables",
			flags: runFlags{profile: "work", profileSet: true},
			env:   map[string]string{"ONESIE_PROFILE": "home", "ONESIE_PROVIDER": "berget"},
			want:  choice{profile: "work", by: "--profile"},
		},
		{
			name:    "should refuse --profile beside --provider",
			flags:   runFlags{profile: "work", profileSet: true, provider: "typesafe", providerSet: true},
			wantErr: "--profile and --provider both choose what the run uses. Pass one",
		},
		{
			name: "should refuse ONESIE_PROFILE beside ONESIE_PROVIDER",
			env:  map[string]string{"ONESIE_PROFILE": "work", "ONESIE_PROVIDER": "berget"},
			wantErr: "ONESIE_PROFILE and ONESIE_PROVIDER are both set, and each chooses what the run uses. " +
				"Unset one",
		},
		{
			name: "should count an empty ONESIE_PROFILE as unset",
			env:  map[string]string{"ONESIE_PROFILE": "", "ONESIE_PROVIDER": "berget"},
			want: choice{provider: "berget", by: "ONESIE_PROVIDER"},
		},
		{
			name: "should count a whitespace only ONESIE_PROVIDER as unset",
			env:  map[string]string{"ONESIE_PROVIDER": "  "},
			want: choice{},
		},
		{
			name:  "should leave an unknown provider for the resolution to refuse",
			flags: runFlags{provider: "nope", providerSet: true},
			want:  choice{provider: "nope", by: "--provider"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := choose(&tc.flags, lookupFrom(tc.env))
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("choose returned an error: %v", err)
			}

			if got != tc.want {
				t.Errorf("choice = %+v, want %+v", got, tc.want)
			}
		})
	}
}

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
			wantErr: "onesie: unknown provider nope",
		},
		{
			name:    "should refuse an unknown provider from ONESIE_PROVIDER",
			env:     map[string]string{"ONESIE_PROVIDER": "nope"},
			wantErr: "onesie: unknown provider nope",
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
				wantCode: ExitUsage, wantErr: "onesie: unknown provider bogus",
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

func TestResolveProfile(t *testing.T) {
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

	tests := []struct {
		name         string
		chosen       choice
		profiles     creds.Profiles
		env          map[string]string
		wantProfile  string
		wantProvider string
		wantBy       string
		wantKeyFrom  string
		wantErr      string
	}{
		{
			name: "should take the default when nothing chose", profiles: withDefault,
			wantProfile: "work", wantProvider: "openrouter", wantBy: "the default", wantKeyFrom: sourceKeychain,
		},
		{
			name: "should take the profile --profile names over the default", profiles: withDefault,
			chosen:      choice{profile: "cf", by: "--profile"},
			wantProfile: "cf", wantProvider: "cloudflare", wantBy: "--profile", wantKeyFrom: sourceFile,
		},
		{
			name: "should take the profile ONESIE_PROFILE names over the default", profiles: withDefault,
			chosen:      choice{profile: "cf", by: "ONESIE_PROFILE"},
			wantProfile: "cf", wantProvider: "cloudflare", wantBy: "ONESIE_PROFILE", wantKeyFrom: sourceFile,
		},
		{
			name: "should name the profiles there are for a missing one", profiles: withDefault,
			chosen:  choice{profile: "nope", by: "ONESIE_PROFILE"},
			wantErr: "ONESIE_PROFILE names profile 'nope', which does not exist. The profiles are 'cf', 'work'",
		},
		{
			name: "should say there are no profiles for a missing one", profiles: empty,
			chosen: choice{profile: "nope", by: "--profile"},
			wantErr: "--profile names profile 'nope', which does not exist. There are no profiles. " +
				"Run onesie profile create NAME --provider PROVIDER to make one",
		},
		{
			name: "should run typesafe without a profile when there are none", profiles: empty,
			wantProvider: "typesafe", wantBy: "nothing", wantKeyFrom: sourceNone,
		},
		{
			name: "should run typesafe without a profile when there is no file", profiles: creds.Profiles{},
			env:          map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
			wantProvider: "typesafe", wantBy: "nothing", wantKeyFrom: sourceEnv,
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

			got, err := resolveProfile(tc.chosen, tc.profiles, lookupFrom(tc.env))
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("resolveProfile returned an error: %v", err)
			}

			if got.profile != tc.wantProfile || got.provider.Name != tc.wantProvider || got.by != tc.wantBy ||
				got.keyFrom != tc.wantKeyFrom {
				t.Errorf("resolution = profile %q, provider %q, by %q, key from %q, want %q, %q, %q, %q",
					got.profile, got.provider.Name, got.by, got.keyFrom,
					tc.wantProfile, tc.wantProvider, tc.wantBy, tc.wantKeyFrom)
			}
		})
	}
}

func TestProfileRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		profile     creds.Profile
		wantKeyFrom string
		wantKey     string
		wantAccount string
		wantKeyEnd  string
	}{
		{
			name:        "should name the keychain item and keep the stored ending",
			profile:     creds.Profile{Provider: "openrouter", Store: creds.StoreKeychain, Account: "work@abc", KeyEnd: "3f9a"},
			wantKeyFrom: sourceKeychain, wantAccount: "work@abc", wantKeyEnd: "3f9a",
		},
		{
			name:        "should carry a file key and its ending at 16 characters",
			profile:     creds.Profile{Provider: "typesafe", APIKey: "SECRET-012345678"},
			wantKeyFrom: sourceFile, wantKey: "SECRET-012345678", wantKeyEnd: "5678",
		},
		{
			name:        "should give a file key under 16 characters no ending",
			profile:     creds.Profile{Provider: "typesafe", APIKey: "SECRET-01234567"},
			wantKeyFrom: sourceFile, wantKey: "SECRET-01234567",
		},
		{
			name:        "should end a padded file key on its last real characters",
			profile:     creds.Profile{Provider: "typesafe", APIKey: "  SECRET-012345678\n"},
			wantKeyFrom: sourceFile, wantKey: "  SECRET-012345678\n", wantKeyEnd: "5678",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := profileRun("work", tc.profile, "--profile")
			if err != nil {
				t.Fatalf("profileRun returned an error: %v", err)
			}

			if got.keyFrom != tc.wantKeyFrom || got.account != tc.wantAccount || got.keyEnd != tc.wantKeyEnd {
				t.Errorf("key from %q, account %q, ending %q, want %q, %q, %q",
					got.keyFrom, got.account, got.keyEnd, tc.wantKeyFrom, tc.wantAccount, tc.wantKeyEnd)
			}

			// Compared, never printed, since it is the key.
			if got.key != tc.wantKey {
				t.Error("the resolved key is not the profile's")
			}
		})
	}

	t.Run("should carry the model, base url and account id", func(t *testing.T) {
		t.Parallel()

		got, err := profileRun("cf", creds.Profile{
			Provider: "cloudflare", APIKey: "SECRET-CF", AccountID: testAccountID, Model: "clef-flash",
		}, "the default")
		if err != nil {
			t.Fatalf("profileRun returned an error: %v", err)
		}

		if got.model != "clef-flash" || got.accountID != testAccountID || got.by != "the default" {
			t.Errorf("resolution = %v with model %q and account %q", got, got.model, got.accountID)
		}
	})
}

func TestResolutionModelFor(t *testing.T) {
	t.Parallel()

	pinned, err := profileRun("work", creds.Profile{Provider: "typesafe", APIKey: "SECRET-W", Model: "jev-1.13.0"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	unpinned, err := profileRun("work", creds.Profile{Provider: "typesafe", APIKey: "SECRET-W"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	berget, err := providerRun("berget", "--provider", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		run      resolution
		explicit string
		env      map[string]string
		want     string
	}{
		{name: "should let -m or the question file beat the profile's model", run: pinned, explicit: "other", want: "other"},
		{name: "should take the profile's model over a blank explicit one", run: pinned, explicit: "  ", want: "jev-1.13.0"},
		{
			name: "should let the profile's model beat TYPESAFE_DEFAULT_MODEL", run: pinned,
			env: map[string]string{onesie.EnvDefaultModel: "env-model"}, want: "jev-1.13.0",
		},
		{
			name: "should take TYPESAFE_DEFAULT_MODEL when the profile names none", run: unpinned,
			env: map[string]string{onesie.EnvDefaultModel: "env-model"}, want: "env-model",
		},
		{name: "should fall back to the built in default", run: unpinned, want: onesie.DefaultModel},
		{name: "should fall back to the provider's own default", run: berget, want: onesie.BergetDefaultModel},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.run.modelFor(tc.explicit, lookupFrom(tc.env)); got != tc.want {
				t.Errorf("modelFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolutionOptions(t *testing.T) {
	t.Parallel()

	profileOf := func(profile creds.Profile) resolution {
		run, err := profileRun("p", profile, "--profile")
		if err != nil {
			t.Fatal(err)
		}

		return run
	}

	cloudflareURL := func(account string) string {
		return "https://api.cloudflare.com/client/v4/accounts/" + account + "/ai"
	}

	const otherAccount = "0123456789abcdef0123456789abcdef"

	tests := []struct {
		name string
		run  resolution
		flag string
		env  map[string]string
		want string
	}{
		{
			name: "should keep --base-url ahead of the profile's base_url",
			run:  profileOf(creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", BaseURL: "https://profile.example"}),
			flag: "https://flag.example", want: "https://flag.example",
		},
		{
			name: "should keep the profile's base_url ahead of TYPESAFE_BASE_URL",
			run:  profileOf(creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", BaseURL: "https://profile.example"}),
			env:  map[string]string{onesie.EnvBaseURL: "https://env.example"}, want: "https://profile.example",
		},
		{
			name: "should take TYPESAFE_BASE_URL when the profile has no base_url",
			run:  profileOf(creds.Profile{Provider: "typesafe", APIKey: "SECRET-P"}),
			env:  map[string]string{onesie.EnvBaseURL: "https://env.example"}, want: "https://env.example",
		},
		{
			name: "should keep the profile's account_id ahead of CLOUDFLARE_ACCOUNT_ID",
			run:  profileOf(creds.Profile{Provider: "cloudflare", APIKey: "SECRET-P", AccountID: testAccountID}),
			env:  map[string]string{"CLOUDFLARE_ACCOUNT_ID": otherAccount}, want: cloudflareURL(testAccountID),
		},
		{
			name: "should take CLOUDFLARE_ACCOUNT_ID when the profile has no account",
			run:  profileOf(creds.Profile{Provider: "cloudflare", APIKey: "SECRET-P"}),
			env:  map[string]string{"CLOUDFLARE_ACCOUNT_ID": otherAccount}, want: cloudflareURL(otherAccount),
		},
		{
			name: "should keep --base-url ahead of the profile's account_id",
			run:  profileOf(creds.Profile{Provider: "cloudflare", APIKey: "SECRET-P", AccountID: testAccountID}),
			flag: "https://flag.example", want: "https://flag.example",
		},
		{
			name: "should fall back to the built in URL",
			run:  profileOf(creds.Profile{Provider: "openrouter", APIKey: "SECRET-P"}),
			want: onesie.OpenRouter().BaseURL,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lookup := lookupFrom(tc.env)

			// The options clientBuilder adds ahead of these, so the order is the one a real run builds.
			opts := []onesie.Option{onesie.WithProvider(tc.run.provider), onesie.WithEnv(tc.run.env(lookup))}
			if tc.flag != "" {
				opts = append(opts, onesie.WithBaseURL(tc.flag))
			}

			client, err := onesie.New(append(opts, tc.run.options(tc.flag, "SECRET-KEY", lookup)...)...)
			if err != nil {
				t.Fatalf("building the client: %v", err)
			}

			if got := client.BaseURL(); got != tc.want {
				t.Errorf("base url = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("should add nothing for a run without a profile", func(t *testing.T) {
		t.Parallel()

		bare, err := providerRun("typesafe", "nothing", lookupFrom(nil))
		if err != nil {
			t.Fatal(err)
		}

		if got := bare.options("", "", lookupFrom(nil)); len(got) != 0 {
			t.Errorf("options = %d, want none", len(got))
		}
	})
}

func TestResolutionEnv(t *testing.T) {
	t.Parallel()

	lookup := lookupFrom(map[string]string{"CLOUDFLARE_ACCOUNT_ID": "env-account", "OTHER": "kept"})

	t.Run("should answer the account variable with the profile's account_id", func(t *testing.T) {
		t.Parallel()

		run, err := profileRun("cf", creds.Profile{Provider: "cloudflare", APIKey: "SECRET-CF", AccountID: testAccountID}, "--profile")
		if err != nil {
			t.Fatal(err)
		}

		env := run.env(lookup)

		if got, _ := env("CLOUDFLARE_ACCOUNT_ID"); got != testAccountID {
			t.Errorf("CLOUDFLARE_ACCOUNT_ID = %q, want the profile's", got)
		}

		if got, _ := env("OTHER"); got != "kept" {
			t.Errorf("OTHER = %q, want the process environment's", got)
		}
	})

	t.Run("should pass the environment through without an account_id", func(t *testing.T) {
		t.Parallel()

		run, err := profileRun("cf", creds.Profile{Provider: "cloudflare", APIKey: "SECRET-CF"}, "--profile")
		if err != nil {
			t.Fatal(err)
		}

		if got, _ := run.env(lookup)("CLOUDFLARE_ACCOUNT_ID"); got != "env-account" {
			t.Errorf("CLOUDFLARE_ACCOUNT_ID = %q, want the environment's", got)
		}
	})

	t.Run("should keep a profile run with a blank key off the provider's key variable", func(t *testing.T) {
		t.Parallel()

		run, err := profileRun("or", creds.Profile{Provider: "openrouter", APIKey: "SECRET-OR"}, "--profile")
		if err != nil {
			t.Fatal(err)
		}

		lookupWithKey := lookupFrom(map[string]string{"OPENROUTER_API_KEY": "SECRET-ENV"})
		opts := []onesie.Option{onesie.WithProvider(run.provider), onesie.WithEnv(run.env(lookupWithKey))}

		_, err = onesie.New(append(opts, run.options("", "  ", lookupWithKey)...)...)
		if !errors.Is(err, onesie.ErrValidation) || !strings.Contains(err.Error(), "no API key") {
			t.Errorf("error = %v, want onesie-go's missing key error", err)
		}
	})

	t.Run("should pass the provider's key variable through on a run without a profile", func(t *testing.T) {
		t.Parallel()

		bare, err := providerRun("openrouter", "--provider", lookupFrom(nil))
		if err != nil {
			t.Fatal(err)
		}

		if got, _ := bare.env(lookupFrom(map[string]string{"OPENROUTER_API_KEY": "SECRET-ENV"}))("OPENROUTER_API_KEY"); got != "SECRET-ENV" {
			t.Error("the provider's key variable did not reach a run without a profile")
		}
	})
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

func TestResolutionBaseURLSource(t *testing.T) {
	t.Parallel()

	profileOf := func(profile creds.Profile) resolution {
		run, err := profileRun("p", profile, "--profile")
		if err != nil {
			t.Fatal(err)
		}

		return run
	}

	bare, err := providerRun("typesafe", "nothing", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  resolution
		flag string
		env  map[string]string
		want string
	}{
		{
			name: "should name --base-url first",
			run:  profileOf(creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", BaseURL: "https://p.example"}),
			flag: "https://flag.example", want: "--base-url",
		},
		{
			name: "should name the profile's base_url",
			run:  profileOf(creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", BaseURL: "https://p.example"}),
			env:  map[string]string{onesie.EnvBaseURL: "https://env.example"}, want: "the profile's base_url",
		},
		{
			name: "should name the profile's account_id",
			run:  profileOf(creds.Profile{Provider: "cloudflare", APIKey: "SECRET-P", AccountID: testAccountID}),
			want: "the profile's account_id",
		},
		{
			name: "should name TYPESAFE_BASE_URL", run: bare,
			env: map[string]string{onesie.EnvBaseURL: "https://env.example"}, want: onesie.EnvBaseURL,
		},
		{
			name: "should name CLOUDFLARE_ACCOUNT_ID",
			run:  profileOf(creds.Profile{Provider: "cloudflare", APIKey: "SECRET-P"}),
			env:  map[string]string{"CLOUDFLARE_ACCOUNT_ID": testAccountID}, want: "CLOUDFLARE_ACCOUNT_ID",
		},
		{name: "should name the provider's base URL last", run: bare, want: "the provider's base URL"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.run.baseURLSource(tc.flag, lookupFrom(tc.env)); got != tc.want {
				t.Errorf("baseURLSource = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolutionKeyLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  resolution
		want string
	}{
		{name: "should name the keychain and its ending", run: resolution{keyFrom: sourceKeychain, keyEnd: "3f9a"}, want: "keychain, ends in 3f9a"},
		{name: "should name the keychain alone with no ending", run: resolution{keyFrom: sourceKeychain}, want: "keychain"},
		{name: "should name the file and its ending", run: resolution{keyFrom: sourceFile, key: "SECRET-012345678", keyEnd: "5678"}, want: "file, ends in 5678"},
		{name: "should name the file alone for a short key", run: resolution{keyFrom: sourceFile, key: "SECRET-0"}, want: "file"},
		{name: "should name the provider's variable", run: resolution{keyFrom: sourceEnv, provider: onesie.OpenRouter()}, want: "env OPENROUTER_API_KEY"},
		{name: "should name none", run: resolution{keyFrom: sourceNone}, want: "none"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.run.keyLine(); got != tc.want {
				t.Errorf("keyLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolutionAccountSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  resolution
		env  map[string]string
		want string
	}{
		{name: "should name nothing off cloudflare", run: resolution{provider: onesie.OpenRouter()}},
		{name: "should name account_id", run: resolution{provider: onesie.Cloudflare(), accountID: testAccountID}, want: "account_id"},
		{name: "should name base_url", run: resolution{provider: onesie.Cloudflare(), baseURL: "https://proxy.example"}, want: "base_url"},
		{
			name: "should name CLOUDFLARE_ACCOUNT_ID", run: resolution{provider: onesie.Cloudflare()},
			env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": testAccountID}, want: "CLOUDFLARE_ACCOUNT_ID",
		},
		{name: "should name none", run: resolution{provider: onesie.Cloudflare()}, want: "none"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.run.accountSource(lookupFrom(tc.env)); got != tc.want {
				t.Errorf("accountSource = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolutionString(t *testing.T) {
	t.Parallel()

	t.Run("should never format the key", func(t *testing.T) {
		t.Parallel()

		run := resolution{provider: onesie.TypeSafe(), profile: "work", keyFrom: sourceFile, key: "SECRET-FILE"}

		for _, verb := range []string{"%v", "%+v", "%s"} {
			if got := fmt.Sprintf(verb, run); strings.Contains(got, "SECRET-") {
				t.Errorf("%s formats the key", verb)
			}
		}
	})
}

func TestResolutionGoString(t *testing.T) {
	t.Parallel()

	t.Run("should leave the key out of %#v", func(t *testing.T) {
		t.Parallel()

		run := resolution{provider: onesie.TypeSafe(), profile: "work", keyFrom: sourceFile, key: "SECRET-FILE"}

		got := fmt.Sprintf("%#v", run)
		if strings.Contains(got, "SECRET-") {
			t.Error("GoString formats the key")
		}

		if got != run.String() {
			t.Errorf("%%#v = %q, want the String form %q", got, run.String())
		}
	})
}

func TestMissingKeyHint(t *testing.T) {
	t.Parallel()

	missing := func(variable string) error {
		return &onesie.ValidationError{Message: "no API key. Set " + variable}
	}

	profiles := creds.Profiles{Profiles: map[string]creds.Profile{
		"work":     {Provider: "openrouter", APIKey: "SECRET-WORK"},
		"personal": {Provider: "openrouter", APIKey: "SECRET-HOME"},
		"ts":       {Provider: "typesafe", APIKey: "SECRET-TS"},
	}}

	bareOf := func(provider, by string, env map[string]string) resolution {
		run, err := providerRun(provider, by, lookupFrom(env))
		if err != nil {
			t.Fatal(err)
		}

		return run
	}

	tests := []struct {
		name string
		err  error
		run  resolution
		want string
	}{
		{
			name: "should name the first profile with the provider under ONESIE_PROVIDER",
			err:  missing("OPENROUTER_API_KEY"), run: bareOf("openrouter", "ONESIE_PROVIDER", nil),
			want: "onesie: no API key. Set OPENROUTER_API_KEY, or unset ONESIE_PROVIDER and run " +
				"onesie profile use 'personal', or run with --profile 'personal'",
		},
		{
			name: "should say a stored profile is not used under ONESIE_PROVIDER when none has the provider",
			err:  missing("LEVANTO_API_KEY"), run: bareOf("levanto", "ONESIE_PROVIDER", nil),
			want: "onesie: no API key. Set LEVANTO_API_KEY. ONESIE_PROVIDER is set, so a stored profile is not " +
				"used. To use one instead, unset ONESIE_PROVIDER and run onesie profile create NAME --provider levanto",
		},
		{
			name: "should say to drop --provider under --provider",
			err:  missing("TYPESAFE_API_KEY"), run: bareOf("typesafe", "--provider", nil),
			want: "onesie: no API key. Set TYPESAFE_API_KEY, or drop --provider and pass --profile 'ts'",
		},
		{
			name: "should name profile create when no profile has the provider",
			err:  missing("BERGET_API_KEY"), run: bareOf("berget", "--provider", nil),
			want: "onesie: no API key. Set BERGET_API_KEY, or store one with onesie profile create NAME --provider berget",
		},
		{
			name: "should leave the error alone when the variable holds a key",
			err:  missing("OPENROUTER_API_KEY"),
			run:  bareOf("openrouter", "--provider", map[string]string{"OPENROUTER_API_KEY": "SECRET-OR"}),
			want: "onesie: no API key. Set OPENROUTER_API_KEY",
		},
		{
			name: "should leave an unrelated validation error alone",
			err:  &onesie.ValidationError{Message: "base URL must be an absolute http or https URL, got proxy.example"},
			run:  bareOf("openrouter", "ONESIE_PROVIDER", nil),
			want: "onesie: base URL must be an absolute http or https URL, got proxy.example",
		},
		{
			name: "should leave an error that is not a validation error alone",
			err:  errors.New("onesie: the server is down"), run: bareOf("openrouter", "--provider", nil),
			want: "onesie: the server is down",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := missingKeyHint(tc.err, tc.run, profiles)
			if got.Error() != tc.want {
				t.Errorf("error = %q, want %q", got.Error(), tc.want)
			}

			if !errors.Is(got, tc.err) {
				t.Error("the hint lost the error it wraps")
			}
		})
	}

	t.Run("should leave a profile run alone", func(t *testing.T) {
		t.Parallel()

		run, err := profileRun("work", profiles.Profiles["work"], "--profile")
		if err != nil {
			t.Fatal(err)
		}

		if got := missingKeyHint(missing("OPENROUTER_API_KEY"), run, profiles); got.Error() != "onesie: no API key. Set OPENROUTER_API_KEY" {
			t.Errorf("error = %q, want it unchanged", got.Error())
		}
	})
}

func TestAccountNote(t *testing.T) {
	t.Parallel()

	noAccount := &onesie.ValidationError{Message: "no cloudflare account id. Set CLOUDFLARE_ACCOUNT_ID"}

	cf, err := profileRun("cf", creds.Profile{Provider: "cloudflare", APIKey: "SECRET-CF"}, "the default")
	if err != nil {
		t.Fatal(err)
	}

	bare, err := providerRun("cloudflare", "--provider", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  resolution
		flag string
		env  map[string]string
		want string
	}{
		{
			name: "should name profile update for a cloudflare profile with no account", run: cf,
			want: "onesie: no cloudflare account id. Set CLOUDFLARE_ACCOUNT_ID, " +
				"or run onesie profile update 'cf' --account-id ACCOUNT_ID",
		},
		{
			name: "should leave the error alone when CLOUDFLARE_ACCOUNT_ID is set", run: cf,
			env:  map[string]string{"CLOUDFLARE_ACCOUNT_ID": testAccountID},
			want: "onesie: no cloudflare account id. Set CLOUDFLARE_ACCOUNT_ID",
		},
		{
			name: "should leave the error alone beside --base-url", run: cf, flag: "https://proxy.example",
			want: "onesie: no cloudflare account id. Set CLOUDFLARE_ACCOUNT_ID",
		},
		{
			name: "should leave a run without a profile alone", run: bare,
			want: "onesie: no cloudflare account id. Set CLOUDFLARE_ACCOUNT_ID",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := accountNote(noAccount, tc.run, tc.flag, lookupFrom(tc.env)); got.Error() != tc.want {
				t.Errorf("error = %q, want %q", got.Error(), tc.want)
			}
		})
	}

	t.Run("should leave an unrelated validation error alone", func(t *testing.T) {
		t.Parallel()

		other := &onesie.ValidationError{Message: "base URL must be an absolute http or https URL, got proxy.example"}

		if got := accountNote(other, cf, "", lookupFrom(nil)); got.Error() != other.Error() {
			t.Errorf("error = %q, want it unchanged", got.Error())
		}
	})
}
