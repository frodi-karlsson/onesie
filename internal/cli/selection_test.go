package cli

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/creds"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

const testAccountID = "76a0a6ac260b32ec3c8703ba592e769f"

func TestCheckChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr string
	}{
		{name: "should pass a known provider", args: []string{"--provider", "berget"}},
		{
			name:    "should refuse an unknown provider from ONESIE_PROVIDER",
			env:     map[string]string{"ONESIE_PROVIDER": "nope"},
			wantErr: "onesie: unknown provider 'nope'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chosen, err := setting.Choose(settingrow.Table(), setting.Sources{Given: flagsFrom(t, tc.args...).given, LookupEnv: lookupFrom(tc.env)})
			if err == nil {
				err = checkChoice(chosen)
			}
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

func TestProfileOf(t *testing.T) {
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
		chosen       setting.Choice
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
			chosen:      setting.Choice{Profile: "cf", ProfileBy: setting.SourceFlag},
			wantProfile: "cf", wantProvider: "cloudflare", wantBy: "--profile", wantKeyFrom: sourceFile,
		},
		{
			name: "should take the profile ONESIE_PROFILE names over the default", profiles: withDefault,
			chosen:      setting.Choice{Profile: "cf", ProfileBy: setting.SourceEnv},
			wantProfile: "cf", wantProvider: "cloudflare", wantBy: "ONESIE_PROFILE", wantKeyFrom: sourceFile,
		},
		{
			name: "should name the profiles there are for a missing one", profiles: withDefault,
			chosen:  setting.Choice{Profile: "nope", ProfileBy: setting.SourceEnv},
			wantErr: "ONESIE_PROFILE names profile 'nope', which does not exist. The profiles are 'cf', 'work'",
		},
		{
			name: "should hint --provider for a --profile that names a provider", profiles: withDefault,
			chosen: setting.Choice{Profile: "levanto", ProfileBy: setting.SourceFlag},
			wantErr: "--profile names profile 'levanto', which does not exist. The profiles are 'cf', 'work'. " +
				"'levanto' is a provider, so pass --provider levanto in place of --profile",
		},
		{
			name: "should hint ONESIE_PROVIDER for an ONESIE_PROFILE that names a provider", profiles: withDefault,
			chosen: setting.Choice{Profile: "berget", ProfileBy: setting.SourceEnv},
			wantErr: "ONESIE_PROFILE names profile 'berget', which does not exist. The profiles are 'cf', 'work'. " +
				"'berget' is a provider, so set ONESIE_PROVIDER=berget in place of ONESIE_PROFILE",
		},
		{
			name: "should give no provider hint for a name that differs from one only in case", profiles: withDefault,
			chosen:  setting.Choice{Profile: "Levanto", ProfileBy: setting.SourceFlag},
			wantErr: "--profile names profile 'Levanto', which does not exist. The profiles are 'cf', 'work'",
		},
		{
			name: "should say there are no profiles for a missing one", profiles: empty,
			chosen: setting.Choice{Profile: "nope", ProfileBy: setting.SourceFlag},
			wantErr: "--profile names profile 'nope', which does not exist. There are no profiles. " +
				"Run onesie profile create NAME --provider PROVIDER to make one",
		},
		{
			name: "should run typesafe without a profile when there are none", profiles: empty,
			wantProvider: "typesafe", wantBy: "nothing", wantKeyFrom: sourceNone,
		},
		{
			name: "should run typesafe without a profile when there is no file", profiles: creds.Profiles{},
			env:          map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
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

			run, err := profileOf(tc.chosen, tc.profiles)

			var got resolution
			if err == nil {
				got, err = resolveRun(run, lookupFrom(tc.env), nil)
			}

			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("profileOf returned an error: %v", err)
			}

			if got.profile != tc.wantProfile || got.provider.Name() != tc.wantProvider || got.by != tc.wantBy ||
				got.keyFrom != tc.wantKeyFrom {
				t.Errorf("resolution = profile %q, provider %q, by %q, key from %q, want %q, %q, %q, %q",
					got.profile, got.provider.Name(), got.by, got.keyFrom,
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
			name:        "should set the key source to none for a keyless profile",
			profile:     creds.Profile{Provider: "typesafe", BaseURL: "http://localhost:11435"},
			wantKeyFrom: sourceNone,
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

func TestRunResolution(t *testing.T) {
	t.Parallel()

	t.Run("should build the connection only from merged values and the provider", func(t *testing.T) {
		merged := &setting.Merged{}
		merged.Set(setting.Profile.Name(), "work", setting.SourceFlag)
		merged.Set(setting.APIKey.Name(), setting.HeldKey{Keychain: true, Account: "work@abc", KeyEnd: "3f9a"}, setting.SourceChosenProfile)
		merged.Set(setting.Model.Name(), "merged-model", setting.SourceFile)
		merged.Set(setting.APIEndpoint.Name(), settingrow.EndpointValue{BaseURL: "https://proxy.example"}, setting.SourceChosenProfile)
		got := runResolution(merged, onesie.OpenRouter())
		if got.profile != "work" || got.by != "--profile" || got.keyFrom != sourceKeychain || got.account != "work@abc" || got.keyEnd != "3f9a" || got.model != "merged-model" || got.baseURL != "https://proxy.example" {
			t.Errorf("connection metadata or merged values were lost: profile %s by %s key source %s account %s model %s endpoint %s", got.profile, got.by, got.keyFrom, got.account, got.model, got.baseURL)
		}
	})

	const otherAccount = "0123456789abcdef0123456789abcdef"

	cloudflareURL := func(account string) string {
		return "https://api.cloudflare.com/client/v4/accounts/" + account + "/ai"
	}

	tests := []struct {
		name      string
		args      []string
		env       map[string]string
		profile   *creds.Profile
		chosen    bool
		wantURL   string
		wantFrom  string
		wantModel string
		wantKey   string
		modelFrom string
	}{
		{
			name: "should keep --base-url ahead of a chosen profile's base_url", chosen: true,
			profile: &creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", BaseURL: "https://profile.example"},
			args:    []string{"--base-url", "https://flag.example"}, wantURL: "https://flag.example", wantFrom: "--base-url",
			wantModel: onesie.TypeSafe().DefaultModel(), wantKey: "SECRET-KEY",
		},
		{
			name: "should keep a chosen profile's base_url ahead of TYPESAFE_BASE_URL", chosen: true,
			profile: &creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", BaseURL: "https://profile.example"},
			env:     map[string]string{onesie.TypeSafe().EnvBaseURL(): "https://env.example"},
			wantURL: "https://profile.example", wantFrom: "the profile's base_url",
			wantModel: onesie.TypeSafe().DefaultModel(), wantKey: "SECRET-KEY",
		},
		{
			name:    "should let TYPESAFE_BASE_URL beat the default profile's base_url, with its key variable",
			profile: &creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", BaseURL: "https://profile.example"},
			env: map[string]string{
				onesie.TypeSafe().EnvBaseURL(): "https://env.example", onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV",
			},
			wantURL: "https://env.example", wantFrom: "TYPESAFE_BASE_URL",
			wantModel: onesie.TypeSafe().DefaultModel(), wantKey: "SECRET-ENV",
		},
		{
			name: "should keep a chosen profile's account_id ahead of CLOUDFLARE_ACCOUNT_ID", chosen: true,
			profile: &creds.Profile{Provider: "cloudflare", APIKey: "SECRET-P", AccountID: testAccountID},
			env:     map[string]string{"CLOUDFLARE_ACCOUNT_ID": otherAccount},
			wantURL: cloudflareURL(testAccountID), wantFrom: "the profile's account_id",
			wantModel: onesie.Cloudflare().DefaultModel(), wantKey: "SECRET-KEY",
		},
		{
			name: "should take CLOUDFLARE_ACCOUNT_ID with the profile's key", chosen: true,
			profile: &creds.Profile{Provider: "cloudflare", APIKey: "SECRET-P"},
			env:     map[string]string{"CLOUDFLARE_ACCOUNT_ID": otherAccount, "CLOUDFLARE_API_TOKEN": "SECRET-ENV"},
			wantURL: cloudflareURL(otherAccount), wantFrom: "CLOUDFLARE_ACCOUNT_ID",
			wantModel: onesie.Cloudflare().DefaultModel(), wantKey: "SECRET-KEY",
		},
		{
			name: "should fall back to the built in URL and model", chosen: true,
			profile: &creds.Profile{Provider: "openrouter", APIKey: "SECRET-P"},
			wantURL: onesie.OpenRouter().DefaultBaseURL(), wantModel: onesie.OpenRouter().DefaultModel(),
			wantKey: "SECRET-KEY", modelFrom: "the provider's default",
		},
		{
			name: "should pass ONESIE_BASE_URL ahead of TYPESAFE_BASE_URL on a run without a profile",
			env: map[string]string{
				onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV", "ONESIE_BASE_URL": "https://local.example",
				onesie.TypeSafe().EnvBaseURL(): "https://env.example",
			},
			wantURL: "https://local.example", wantFrom: "ONESIE_BASE_URL", wantModel: onesie.TypeSafe().DefaultModel(),
			wantKey: "SECRET-ENV",
		},
		{
			name: "should name -m as where the model came from", args: []string{"-m", "flag-model"},
			profile: &creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", Model: "jev-1.13.0"},
			wantURL: onesie.TypeSafe().DefaultBaseURL(), wantModel: "flag-model", wantKey: "SECRET-KEY",
			modelFrom: "--model",
		},
		{
			name:    "should send the provider key variable over the default profile's key",
			profile: &creds.Profile{Provider: "typesafe", APIKey: "SECRET-P"},
			env:     map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			wantURL: onesie.TypeSafe().DefaultBaseURL(), wantModel: onesie.TypeSafe().DefaultModel(),
			wantKey: "SECRET-ENV",
		},
		{
			name: "should send a chosen profile's key over the provider key variable", chosen: true,
			profile: &creds.Profile{Provider: "typesafe", APIKey: "SECRET-P"},
			env:     map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			wantURL: onesie.TypeSafe().DefaultBaseURL(), wantModel: onesie.TypeSafe().DefaultModel(),
			wantKey: "SECRET-KEY",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			run, merged := mergedRun(t, tc.args, tc.env, tc.profile, tc.chosen)
			if run.endpointFrom != tc.wantFrom {
				t.Errorf("endpointFrom = %q, want %q", run.endpointFrom, tc.wantFrom)
			}

			if got := run.modelFor(setting.Get(merged, setting.Model)); got != tc.wantModel {
				t.Errorf("model = %q, want %q", got, tc.wantModel)
			}

			if tc.modelFrom != "" && run.modelFrom != tc.modelFrom {
				t.Errorf("modelFrom = %q, want %q", run.modelFrom, tc.modelFrom)
			}

			client, err := onesie.New(append([]onesie.Option{
				onesie.WithProvider(run.provider), onesie.WithEnv(noEnvironment),
			}, run.options(staticKey("SECRET-KEY"))...)...)
			if err != nil {
				t.Fatalf("building the client: %v", err)
			}

			if got := client.BaseURL(); got != tc.wantURL {
				t.Errorf("base url = %q, want %q", got, tc.wantURL)
			}

			if got := client.DefaultModel(); got != tc.wantModel {
				t.Errorf("default model = %q, want %q", got, tc.wantModel)
			}

			sent := "SECRET-KEY"
			if run.keyFrom == sourceEnv {
				sent = run.key
			}

			if !client.SendsKey() || sent != tc.wantKey {
				t.Errorf("key = %q sent %v, want %q", sent, client.SendsKey(), tc.wantKey)
			}
		})
	}

	t.Run("should send no key from a keyless profile", func(t *testing.T) {
		t.Parallel()

		run, _ := mergedRun(t, nil, nil, &creds.Profile{Provider: "openrouter"}, true)

		_, err := onesie.New(append([]onesie.Option{onesie.WithProvider(run.provider), onesie.WithEnv(noEnvironment)},
			run.options(staticKey("SECRET-KEY"))...)...)
		if !errors.Is(err, onesie.ErrNoAPIKey) {
			t.Errorf("error = %v, want onesie.ErrNoAPIKey", err)
		}
	})
}

func mergedRun(
	t *testing.T, args []string, env map[string]string, profile *creds.Profile, chosen bool,
) (resolution, *setting.Merged) {
	t.Helper()

	lookup := maps.Clone(env)
	if lookup == nil {
		lookup = map[string]string{}
	}

	run := loadedRun{}
	if profile != nil {
		run = loadedRun{name: "p", by: byDefault, profile: *profile, fallback: "p"}
	}

	if chosen {
		args = append(args, "--profile", "p")
		run.by = "--profile"
	}

	flags := flagsFrom(t, args...)

	src := setting.Sources{
		Given: flags.given, LookupEnv: lookupFrom(lookup), Profile: run.values(), DefaultProfile: run.fallback,
		Command: settingrow.CommandRoot,
	}
	chosenSource, err := setting.Choose(settingrow.Table(), src)
	if err != nil {
		t.Fatal(err)
	}
	src.Choice = &chosenSource
	layers, err := setting.Read(settingrow.Table(), src)
	if err != nil {
		t.Fatal(err)
	}
	merged, _ := setting.Merge(settingrow.Table(), layers, chosenSource)

	resolved, err := resolveRun(run, lookupFrom(lookup), nil)
	if err != nil {
		t.Fatal(err)
	}

	return runResolution(merged, resolved.provider), merged
}

func flagsFrom(t *testing.T, args ...string) *runFlags {
	t.Helper()

	flags := &runFlags{}
	cmd := &cobra.Command{Use: "onesie"}
	flags.given = setting.Register(cmd.Flags(), cmd.PersistentFlags(), settingrow.Table(), argv.New(),
		settingrow.CommandRoot, runTargets(flags))
	cmd.Flags().AddFlagSet(cmd.PersistentFlags())

	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatal(err)
	}

	return flags
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
		name   string
		run    resolution
		custom bool
		want   string
	}{
		{name: "should name the keychain and its ending", run: resolution{keyFrom: sourceKeychain, keyEnd: "3f9a"}, want: "keychain, ends in 3f9a"},
		{name: "should name the keychain alone with no ending", run: resolution{keyFrom: sourceKeychain}, want: "keychain"},
		{name: "should name the file and its ending", run: resolution{keyFrom: sourceFile, key: "SECRET-012345678", keyEnd: "5678"}, want: "file, ends in 5678"},
		{name: "should name the file alone for a short key", run: resolution{keyFrom: sourceFile, key: "SECRET-0"}, want: "file"},
		{name: "should name the provider's variable", run: resolution{keyFrom: sourceEnv, provider: onesie.OpenRouter()}, want: "env OPENROUTER_API_KEY"},
		{name: "should name none", run: resolution{keyFrom: sourceNone}, want: "none"},
		{
			name: "should print none, not needed for a custom base URL", run: resolution{keyFrom: sourceNone},
			custom: true, want: "none, not needed for a custom base URL",
		},
		{
			name: "should name the provider's variable on a custom base URL",
			run:  resolution{keyFrom: sourceEnv, provider: onesie.OpenRouter()}, custom: true, want: "env OPENROUTER_API_KEY",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.run.keyLine(tc.custom); got != tc.want {
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
		{
			name: "should name account_id",
			run:  resolution{provider: onesie.Cloudflare(), accountID: testAccountID, endpointFrom: profileAccountID},
			want: "account_id",
		},
		{
			name: "should name CLOUDFLARE_ACCOUNT_ID for an account the merge took from it",
			run:  resolution{provider: onesie.Cloudflare(), accountID: testAccountID, endpointFrom: "CLOUDFLARE_ACCOUNT_ID"},
			want: "CLOUDFLARE_ACCOUNT_ID",
		},
		{name: "should name base_url", run: resolution{provider: onesie.Cloudflare(), baseURL: "https://proxy.example"}, want: "base_url"},
		{
			name: "should name CLOUDFLARE_ACCOUNT_ID", run: resolution{provider: onesie.Cloudflare()},
			env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": testAccountID}, want: "CLOUDFLARE_ACCOUNT_ID",
		},
		{name: "should name none", run: resolution{provider: onesie.Cloudflare()}, want: "none"},
		{
			name: "should name CLOUDFLARE_ACCOUNT_ID when it is malformed", run: resolution{provider: onesie.Cloudflare()},
			env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "not-hex"}, want: "CLOUDFLARE_ACCOUNT_ID",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, source, resolveErr := tc.run.provider.ResolveBaseURL(tc.run.baseURL, tc.run.accountID, lookupFrom(tc.env))

			if got := tc.run.accountSource(source, resolveErr); got != tc.want {
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

	missing := func(provider onesie.Provider) error {
		_, err := onesie.New(onesie.WithProvider(provider), onesie.WithEnv(lookupFrom(nil)))

		return err
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

		run, err := profileRun("work", profiles.Profiles["work"], "--profile")
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
		err  error
		run  resolution
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
