package resolve

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

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
