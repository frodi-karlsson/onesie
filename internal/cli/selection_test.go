package cli

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const testAccountID = "76a0a6ac260b32ec3c8703ba592e769f"

func TestChoose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		given   Given
		speller plan.Speller
		env     map[string]string
		want    Chosen
		wantErr string
	}{
		{name: "should choose nothing when nothing is given", want: Chosen{}},
		{
			name:  "should choose the profile --profile names",
			given: Given{Profile: "work", ProfileSet: true},
			want:  Chosen{Profile: "work", By: "--profile"},
		},
		{
			name:  "should choose the provider --provider names",
			given: Given{Provider: "openrouter", ProviderSet: true},
			want:  Chosen{Provider: "openrouter", By: "--provider"},
		},
		{
			name:  "should trim the name a flag gives",
			given: Given{Profile: " work ", ProfileSet: true},
			want:  Chosen{Profile: "work", By: "--profile"},
		},
		{
			name: "should choose the profile ONESIE_PROFILE names",
			env:  map[string]string{"ONESIE_PROFILE": "work"},
			want: Chosen{Profile: "work", By: "ONESIE_PROFILE"},
		},
		{
			name: "should choose the provider ONESIE_PROVIDER names",
			env:  map[string]string{"ONESIE_PROVIDER": "berget"},
			want: Chosen{Provider: "berget", By: "ONESIE_PROVIDER"},
		},
		{
			name:  "should let --profile beat ONESIE_PROVIDER",
			given: Given{Profile: "work", ProfileSet: true},
			env:   map[string]string{"ONESIE_PROVIDER": "berget"},
			want:  Chosen{Profile: "work", By: "--profile"},
		},
		{
			name:  "should let --provider beat ONESIE_PROFILE",
			given: Given{Provider: "levanto", ProviderSet: true},
			env:   map[string]string{"ONESIE_PROFILE": "work"},
			want:  Chosen{Provider: "levanto", By: "--provider"},
		},
		{
			name:  "should let --profile beat ONESIE_PROFILE",
			given: Given{Profile: "home", ProfileSet: true},
			env:   map[string]string{"ONESIE_PROFILE": "work"},
			want:  Chosen{Profile: "home", By: "--profile"},
		},
		{
			name:  "should let --provider beat ONESIE_PROVIDER",
			given: Given{Provider: "typesafe", ProviderSet: true},
			env:   map[string]string{"ONESIE_PROVIDER": "berget"},
			want:  Chosen{Provider: "typesafe", By: "--provider"},
		},
		{
			name:  "should let --profile win over both variables",
			given: Given{Profile: "work", ProfileSet: true},
			env:   map[string]string{"ONESIE_PROFILE": "home", "ONESIE_PROVIDER": "berget"},
			want:  Chosen{Profile: "work", By: "--profile"},
		},
		{
			name:    "should refuse --profile beside --provider",
			given:   Given{Profile: "work", ProfileSet: true, Provider: "typesafe", ProviderSet: true},
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
			want: Chosen{Provider: "berget", By: "ONESIE_PROVIDER"},
		},
		{
			name: "should count a whitespace only ONESIE_PROVIDER as unset",
			env:  map[string]string{"ONESIE_PROVIDER": "  "},
			want: Chosen{},
		},
		{
			name:    "should name the flags as tool arguments for that speller",
			given:   Given{Profile: "work", ProfileSet: true, Provider: "typesafe", ProviderSet: true},
			speller: plan.Arguments,
			wantErr: "profile and provider both choose what the run uses. Pass one",
		},
		{
			name:    "should set By to the setting that chose, as the speller names it",
			given:   Given{Provider: "typesafe", ProviderSet: true},
			speller: plan.Arguments,
			want:    Chosen{Provider: "typesafe", By: "provider"},
		},
		{
			name:    "should set By from the environment variable that chose",
			env:     map[string]string{"ONESIE_PROFILE": "work"},
			speller: plan.Arguments,
			want:    Chosen{Profile: "work", By: "ONESIE_PROFILE"},
		},
		{
			name:  "should leave an unknown provider for the resolution to refuse",
			given: Given{Provider: "nope", ProviderSet: true},
			want:  Chosen{Provider: "nope", By: "--provider"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Choose(tc.given, lookupFrom(tc.env), tc.speller)
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

func TestProfile(t *testing.T) {
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
		chosen       Chosen
		profiles     creds.Profiles
		env          map[string]string
		wantProfile  string
		wantProvider string
		wantBy       string
		wantKeyFrom  string
		wantErr      string
		wantIs       error
		wantNotFound *ProfileNotFoundError
	}{
		{
			name: "should take the default when nothing chose", profiles: withDefault,
			wantProfile: "work", wantProvider: "openrouter", wantBy: "the default", wantKeyFrom: KeyFromKeychain,
		},
		{
			name: "should take the profile --profile names over the default", profiles: withDefault,
			chosen:      Chosen{Profile: "cf", By: "--profile"},
			wantProfile: "cf", wantProvider: "cloudflare", wantBy: "--profile", wantKeyFrom: KeyFromFile,
		},
		{
			name: "should take the profile ONESIE_PROFILE names over the default", profiles: withDefault,
			chosen:      Chosen{Profile: "cf", By: "ONESIE_PROFILE"},
			wantProfile: "cf", wantProvider: "cloudflare", wantBy: "ONESIE_PROFILE", wantKeyFrom: KeyFromFile,
		},
		{
			name: "should run typesafe without a profile when there are none", profiles: empty,
			wantProvider: "typesafe", wantBy: "nothing", wantKeyFrom: KeyFromNone,
		},
		{
			name: "should run typesafe without a profile when there is no file", profiles: creds.Profiles{},
			env:          map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			wantProvider: "typesafe", wantBy: "nothing", wantKeyFrom: KeyFromEnv,
		},
		{
			name: "should return ErrNoDefaultProfile", profiles: noDefault,
			wantErr: "no profile is the default and nothing chose one", wantIs: ErrNoDefaultProfile,
		},
		{
			name: "should return a ProfileNotFoundError carrying the profiles", profiles: withDefault,
			chosen:       Chosen{Profile: "nope", By: "ONESIE_PROFILE"},
			wantErr:      "ONESIE_PROFILE names profile 'nope', which does not exist",
			wantNotFound: &ProfileNotFoundError{By: "ONESIE_PROFILE", Name: "nope", Profiles: withDefault},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := Profile(tc.chosen, tc.profiles, lookupFrom(tc.env))
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr || (tc.wantIs != nil && !errors.Is(err, tc.wantIs)) {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}

				var notFound *ProfileNotFoundError
				if tc.wantNotFound != nil && (!errors.As(err, &notFound) || !reflect.DeepEqual(notFound, tc.wantNotFound)) {
					t.Errorf("error = %#v, want %#v", err, tc.wantNotFound)
				}

				return
			}

			if err != nil {
				t.Fatalf("Profile returned an error: %v", err)
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
		chosen   Chosen
		profiles creds.Profiles
		openErr  error
		wantErr  string
	}{
		{
			name: "should point --provider at --profile when it names a profile", profiles: withDefault,
			chosen:  Chosen{Provider: "work", By: "--provider"},
			wantErr: unknownWork + ". 'work' is a profile, so pass --profile 'work' in place of --provider",
		},
		{
			name: "should point ONESIE_PROVIDER at ONESIE_PROFILE when it names a profile", profiles: withDefault,
			chosen: Chosen{Provider: "work", By: "ONESIE_PROVIDER"},
			wantErr: unknownWork + ". 'work' is a profile, so set ONESIE_PROFILE='work' in place of " +
				"ONESIE_PROVIDER",
		},
		{
			name:    "should keep the provider's error when the file cannot be opened for the hint",
			chosen:  Chosen{Provider: "work", By: "--provider"},
			openErr: errors.New("profiles.json is readable by others"),
			wantErr: unknownWork,
		},
		{
			name: "should name the profiles there are for a missing one", profiles: withDefault,
			chosen:  Chosen{Profile: "nope", By: "ONESIE_PROFILE"},
			wantErr: "ONESIE_PROFILE names profile 'nope', which does not exist. The profiles are 'cf', 'work'",
		},
		{
			name: "should hint --provider for a --profile that names a provider", profiles: withDefault,
			chosen: Chosen{Profile: "levanto", By: "--profile"},
			wantErr: "--profile names profile 'levanto', which does not exist. The profiles are 'cf', 'work'. " +
				"'levanto' is a provider, so pass --provider levanto in place of --profile",
		},
		{
			name: "should hint ONESIE_PROVIDER for an ONESIE_PROFILE that names a provider", profiles: withDefault,
			chosen: Chosen{Profile: "berget", By: "ONESIE_PROFILE"},
			wantErr: "ONESIE_PROFILE names profile 'berget', which does not exist. The profiles are 'cf', 'work'. " +
				"'berget' is a provider, so set ONESIE_PROVIDER=berget in place of ONESIE_PROFILE",
		},
		{
			name: "should give no provider hint for a name that differs from one only in case", profiles: withDefault,
			chosen:  Chosen{Profile: "Levanto", By: "--profile"},
			wantErr: "--profile names profile 'Levanto', which does not exist. The profiles are 'cf', 'work'",
		},
		{
			name: "should say there are no profiles for a missing one", profiles: empty,
			chosen: Chosen{Profile: "nope", By: "--profile"},
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

func refusal(chosen Chosen, profiles creds.Profiles) error {
	if chosen.Provider != "" {
		_, err := ProviderRun(chosen.Provider, chosen.By, lookupFrom(nil))

		return &ProviderError{Given: chosen, Err: err}
	}

	_, err := Profile(chosen, profiles, lookupFrom(nil))

	return err
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
			wantKeyFrom: KeyFromKeychain, wantAccount: "work@abc", wantKeyEnd: "3f9a",
		},
		{
			name:        "should carry a file key and its ending at 16 characters",
			profile:     creds.Profile{Provider: "typesafe", APIKey: "SECRET-012345678"},
			wantKeyFrom: KeyFromFile, wantKey: "SECRET-012345678", wantKeyEnd: "5678",
		},
		{
			name:        "should give a file key under 16 characters no ending",
			profile:     creds.Profile{Provider: "typesafe", APIKey: "SECRET-01234567"},
			wantKeyFrom: KeyFromFile, wantKey: "SECRET-01234567",
		},
		{
			name:        "should end a padded file key on its last real characters",
			profile:     creds.Profile{Provider: "typesafe", APIKey: "  SECRET-012345678\n"},
			wantKeyFrom: KeyFromFile, wantKey: "  SECRET-012345678\n", wantKeyEnd: "5678",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := ProfileRun("work", tc.profile, "--profile")
			if err != nil {
				t.Fatalf("profileRun returned an error: %v", err)
			}

			if got.KeyFrom != tc.wantKeyFrom || got.Account != tc.wantAccount || got.KeyEnd != tc.wantKeyEnd {
				t.Errorf("key from %q, account %q, ending %q, want %q, %q, %q",
					got.KeyFrom, got.Account, got.KeyEnd, tc.wantKeyFrom, tc.wantAccount, tc.wantKeyEnd)
			}

			// Compared, never printed, since it is the key.
			if got.key != tc.wantKey {
				t.Error("the resolved key is not the profile's")
			}
		})
	}

	t.Run("should carry the model, base url and account id", func(t *testing.T) {
		t.Parallel()

		got, err := ProfileRun("cf", creds.Profile{
			Provider: "cloudflare", APIKey: "SECRET-CF", AccountID: testAccountID, Model: "clef-flash",
		}, "the default")
		if err != nil {
			t.Fatalf("profileRun returned an error: %v", err)
		}

		if got.Model != "clef-flash" || got.AccountID != testAccountID || got.By != "the default" {
			t.Errorf("resolution = %v with model %q and account %q", got, got.Model, got.AccountID)
		}
	})
}

func TestResolutionModelFor(t *testing.T) {
	t.Parallel()

	pinned, err := ProfileRun("work", creds.Profile{Provider: "typesafe", APIKey: "SECRET-W", Model: "jev-1.13.0"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	unpinned, err := ProfileRun("work", creds.Profile{Provider: "typesafe", APIKey: "SECRET-W"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	berget, err := ProviderRun("berget", "--provider", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		run      Resolution
		explicit string
		env      map[string]string
		want     string
	}{
		{name: "should let -m or the question file beat the profile's model", run: pinned, explicit: "other", want: "other"},
		{name: "should take the profile's model over a blank explicit one", run: pinned, explicit: "  ", want: "jev-1.13.0"},
		{
			name: "should let the profile's model beat TYPESAFE_DEFAULT_MODEL", run: pinned,
			env: map[string]string{onesie.TypeSafe().EnvDefaultModel(): "env-model"}, want: "jev-1.13.0",
		},
		{
			name: "should take TYPESAFE_DEFAULT_MODEL when the profile names none", run: unpinned,
			env: map[string]string{onesie.TypeSafe().EnvDefaultModel(): "env-model"}, want: "env-model",
		},
		{name: "should fall back to the built in default", run: unpinned, want: onesie.TypeSafe().DefaultModel()},
		{name: "should fall back to the provider's own default", run: berget, want: onesie.Berget().DefaultModel()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.run.ModelFor(tc.explicit, lookupFrom(tc.env)); got != tc.want {
				t.Errorf("modelFor = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolutionOptions(t *testing.T) {
	t.Parallel()

	profileOf := func(profile creds.Profile) Resolution {
		run, err := ProfileRun("p", profile, "--profile")
		if err != nil {
			t.Fatal(err)
		}

		return run
	}

	cloudflareURL := func(account string) string {
		return "https://api.cloudflare.com/client/v4/accounts/" + account + "/ai"
	}

	const otherAccount = "0123456789abcdef0123456789abcdef"

	bareRun, err := ProviderRun("typesafe", "nothing", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		run  Resolution
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
			env:  map[string]string{onesie.TypeSafe().EnvBaseURL(): "https://env.example"}, want: "https://profile.example",
		},
		{
			name: "should take TYPESAFE_BASE_URL when the profile has no base_url",
			run:  profileOf(creds.Profile{Provider: "typesafe", APIKey: "SECRET-P"}),
			env:  map[string]string{onesie.TypeSafe().EnvBaseURL(): "https://env.example"}, want: "https://env.example",
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
			want: onesie.OpenRouter().DefaultBaseURL(),
		},
		{
			name: "should take --base-url on a run without a profile",
			run:  bareRun,
			flag: "https://flag.example",
			env:  map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			want: "https://flag.example",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lookup := lookupFrom(tc.env)

			// The options clientBuilder adds ahead of these, so the order is the one a real run builds.
			opts := []onesie.Option{onesie.WithProvider(tc.run.Provider), onesie.WithEnv(lookup)}

			client, err := onesie.New(append(opts, tc.run.Options(tc.flag, staticKey("SECRET-KEY"), lookup)...)...)
			if err != nil {
				t.Fatalf("building the client: %v", err)
			}

			if got := client.BaseURL(); got != tc.want {
				t.Errorf("base url = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("should add nothing for a run without a profile or --base-url", func(t *testing.T) {
		t.Parallel()

		if got := bareRun.Options("", staticKey(""), lookupFrom(nil)); len(got) != 0 {
			t.Errorf("options = %d, want none", len(got))
		}
	})

	t.Run("should keep a profile run with an empty key off the provider's key variable", func(t *testing.T) {
		t.Parallel()

		run := profileOf(creds.Profile{Provider: "openrouter", APIKey: "SECRET-OR"})
		lookup := lookupFrom(map[string]string{"OPENROUTER_API_KEY": "SECRET-ENV"})

		_, err := onesie.New(append([]onesie.Option{onesie.WithProvider(run.Provider), onesie.WithEnv(lookup)},
			run.Options("", staticKey(""), lookup)...)...)
		if !errors.Is(err, onesie.ErrNoAPIKey) {
			t.Errorf("error = %v, want onesie.ErrNoAPIKey", err)
		}
	})
}

func TestResolutionGivenBaseURL(t *testing.T) {
	t.Parallel()

	profile := Resolution{Profile: "work", BaseURL: "https://profile.example"}

	tests := []struct {
		name       string
		run        Resolution
		flag       string
		wantURL    string
		wantSource string
	}{
		{
			name: "should take --base-url ahead of the profile's base_url", run: profile,
			flag: "https://flag.example", wantURL: "https://flag.example", wantSource: "--base-url",
		},
		{
			name: "should trim the space around --base-url", run: profile,
			flag: "  https://flag.example ", wantURL: "https://flag.example", wantSource: "--base-url",
		},
		{
			name: "should take the profile's base_url when --base-url is blank", run: profile,
			flag: " ", wantURL: "https://profile.example", wantSource: "the profile's base_url",
		},
		{
			name: "should give no URL when neither is set", run: Resolution{Profile: "work"},
			wantSource: "the profile's base_url",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			source, url := tc.run.GivenBaseURL(tc.flag)
			if source != tc.wantSource || url != tc.wantURL {
				t.Errorf("givenBaseURL = %q, %q, want %q, %q", source, url, tc.wantSource, tc.wantURL)
			}
		})
	}
}

func staticKey(key string) func() (string, error) {
	return func() (string, error) {
		return key, nil
	}
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

func TestResolutionKeyLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  Resolution
		want string
	}{
		{name: "should name the keychain and its ending", run: Resolution{KeyFrom: KeyFromKeychain, KeyEnd: "3f9a"}, want: "keychain, ends in 3f9a"},
		{name: "should name the keychain alone with no ending", run: Resolution{KeyFrom: KeyFromKeychain}, want: "keychain"},
		{name: "should name the file and its ending", run: Resolution{KeyFrom: KeyFromFile, key: "SECRET-012345678", KeyEnd: "5678"}, want: "file, ends in 5678"},
		{name: "should name the file alone for a short key", run: Resolution{KeyFrom: KeyFromFile, key: "SECRET-0"}, want: "file"},
		{name: "should name the provider's variable", run: Resolution{KeyFrom: KeyFromEnv, Provider: onesie.OpenRouter()}, want: "env OPENROUTER_API_KEY"},
		{name: "should name none", run: Resolution{KeyFrom: KeyFromNone}, want: "none"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.run.KeyLine(); got != tc.want {
				t.Errorf("keyLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolutionAccountSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  Resolution
		env  map[string]string
		want string
	}{
		{name: "should name nothing off cloudflare", run: Resolution{Provider: onesie.OpenRouter()}},
		{name: "should name account_id", run: Resolution{Provider: onesie.Cloudflare(), AccountID: testAccountID}, want: "account_id"},
		{name: "should name base_url", run: Resolution{Provider: onesie.Cloudflare(), BaseURL: "https://proxy.example"}, want: "base_url"},
		{
			name: "should name CLOUDFLARE_ACCOUNT_ID", run: Resolution{Provider: onesie.Cloudflare()},
			env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": testAccountID}, want: "CLOUDFLARE_ACCOUNT_ID",
		},
		{name: "should name none", run: Resolution{Provider: onesie.Cloudflare()}, want: "none"},
		{
			name: "should name CLOUDFLARE_ACCOUNT_ID when it is malformed", run: Resolution{Provider: onesie.Cloudflare()},
			env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "not-hex"}, want: "CLOUDFLARE_ACCOUNT_ID",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, source, resolveErr := tc.run.Provider.ResolveBaseURL(tc.run.BaseURL, tc.run.AccountID, lookupFrom(tc.env))

			if got := tc.run.AccountSource(source, resolveErr); got != tc.want {
				t.Errorf("accountSource = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolutionString(t *testing.T) {
	t.Parallel()

	t.Run("should never format the key", func(t *testing.T) {
		t.Parallel()

		run := Resolution{Provider: onesie.TypeSafe(), Profile: "work", KeyFrom: KeyFromFile, key: "SECRET-FILE"}

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

		run := Resolution{Provider: onesie.TypeSafe(), Profile: "work", KeyFrom: KeyFromFile, key: "SECRET-FILE"}

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

	missing := func(provider onesie.Provider) error {
		_, err := onesie.New(onesie.WithProvider(provider), onesie.WithEnv(lookupFrom(nil)))

		return err
	}

	profiles := creds.Profiles{Profiles: map[string]creds.Profile{
		"work":     {Provider: "openrouter", APIKey: "SECRET-WORK"},
		"personal": {Provider: "openrouter", APIKey: "SECRET-HOME"},
		"ts":       {Provider: "typesafe", APIKey: "SECRET-TS"},
	}}

	bareOf := func(provider, by string, env map[string]string) Resolution {
		run, err := ProviderRun(provider, by, lookupFrom(env))
		if err != nil {
			t.Fatal(err)
		}

		return run
	}

	tests := []struct {
		name string
		err  error
		run  Resolution
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

		run, err := ProfileRun("work", profiles.Profiles["work"], "--profile")
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

	cf, err := ProfileRun("cf", creds.Profile{Provider: "cloudflare", APIKey: "SECRET-CF"}, "the default")
	if err != nil {
		t.Fatal(err)
	}

	bare, err := ProviderRun("cloudflare", "--provider", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		err  error
		run  Resolution
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
