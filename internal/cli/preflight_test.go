package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestNewProfilePreflightCmd(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, file string
		env        map[string]string
		want       string
	}{
		{
			name: "should preserve environment endpoint key advice",
			env:  map[string]string{"TYPESAFE_API_KEY": "sentinel-secret", "ONESIE_BASE_URL": "http://localhost:1234/path"},
			want: "onesie is on PATH and a key resolves, profile: none, provider: typesafe.\n" +
				"Its base URL comes from ONESIE_BASE_URL, so the key goes to http://localhost:1234.",
		},
		{
			name: "should report both missing Cloudflare settings in one message",
			env:  map[string]string{"ONESIE_PROVIDER": "cloudflare"},
			want: "onesie is on PATH, but no API key resolves for provider cloudflare, and no account does either. Only " +
				"--print-request, --print-questions, --print-schema and --mock work until the user sets " +
				"CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID, or stores a profile with onesie profile create NAME " +
				"--provider cloudflare --account-id ACCOUNT_ID. ONESIE_PROVIDER is set, so no stored profile is " +
				"used. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should advise profile create for a key with no account under ONESIE_PROVIDER",
			env:  map[string]string{"ONESIE_PROVIDER": "cloudflare", "CLOUDFLARE_API_TOKEN": "sentinel-secret"},
			want: "onesie is on PATH and a key resolves for provider cloudflare, but no account does. Every request fails " +
				"until the user sets CLOUDFLARE_ACCOUNT_ID or stores a profile with onesie profile create NAME --provider " +
				"cloudflare --account-id ACCOUNT_ID. ONESIE_PROVIDER is set, so no stored profile is " +
				"used. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should name the default profile for a key with no account",
			file: `{"default":"work","profiles":{"work":{"provider":"cloudflare","api_key":"sentinel-secret"}}}`,
			want: "onesie is on PATH and a key resolves for profile work, but no account does. Every request fails until " +
				"the user runs onesie profile update work --account-id ACCOUNT_ID or sets CLOUDFLARE_ACCOUNT_ID. The " +
				"onesie-setup skill walks the user through it.",
		},
		{
			name: "should name a profile chosen by ONESIE_PROFILE for a key with no account",
			file: `{"profiles":{"ci":{"provider":"cloudflare","api_key":"sentinel-secret"}}}`,
			env:  map[string]string{"ONESIE_PROFILE": "ci"},
			want: "onesie is on PATH and a key resolves for profile ci, but no account does. Every request fails until " +
				"the user runs onesie profile update ci --account-id ACCOUNT_ID or sets CLOUDFLARE_ACCOUNT_ID. The " +
				"onesie-setup skill walks the user through it.",
		},
		{
			name: "should list what works without a key",
			want: "onesie is on PATH, but no API key resolves for provider typesafe. Only --print-request, --print-questions, " +
				"--print-schema and --mock work until the user sets TYPESAFE_API_KEY or stores a profile with onesie profile " +
				"create NAME --provider typesafe. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should explain a missing default profile",
			file: `{"profiles":{"work":{"provider":"typesafe","api_key":"sentinel-secret"}}}`,
			want: "onesie is on PATH, but configuration is not ready. No profile is the default. Run onesie profile use " +
				"NAME. onesie profile list names them. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should name the actual provider key variable",
			env:  map[string]string{"ONESIE_PROVIDER": "berget"},
			want: "onesie is on PATH, but no API key resolves for provider berget. Only --print-request, --print-questions, " +
				"--print-schema and --mock work until the user sets BERGET_API_KEY or stores a profile with onesie profile " +
				"create NAME --provider berget. ONESIE_PROVIDER is set, so no stored profile is " +
				"used. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should safely refuse a multiline setting",
			env:  map[string]string{"ONESIE_CACHE": "bad\nsentinel-secret"},
			want: "onesie is on PATH, but configuration is not ready. Unset or correct ONESIE_CACHE. Run onesie profile " +
				"show for manual inspection. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should drop the ONESIE_PROVIDER line beside a configuration problem",
			env:  map[string]string{"ONESIE_PROVIDER": "cloudflare", "CLOUDFLARE_API_TOKEN": "sentinel-secret", "ONESIE_CACHE": "bad"},
			want: "onesie is on PATH, but configuration is not ready. Unset or correct ONESIE_CACHE. Run onesie profile " +
				"show for manual inspection. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should safely refuse a malformed profile name",
			env:  map[string]string{"ONESIE_PROFILE": "bad\nsentinel-secret"},
			want: "onesie is on PATH, but configuration is not ready. Run onesie profile show for manual inspection, then " +
				"correct the selected profile, provider or endpoint. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should omit a valid endpoint path",
			env:  map[string]string{"ONESIE_BASE_URL": "http://localhost:1234/sentinel-secret"},
			want: "onesie is on PATH and needs no key, since its base URL is custom, profile: none, provider: typesafe.\n" +
				"Its base URL uses http://localhost:1234.",
		},
		{
			name: "should describe the configured keychain without reading it",
			file: `{"default":"work","profiles":{"work":{"provider":"typesafe","store":"keychain","account":"work@abc","key_end":"SENT"}}}`,
			want: "onesie is on PATH and a keychain entry is configured, retrieval is not verified, profile: work, provider: typesafe.",
		},
		{
			name: "should warn about remote HTTP",
			env:  map[string]string{"ONESIE_BASE_URL": "http://example.invalid/sentinel-secret"},
			want: "onesie is on PATH and needs no key, since its base URL is custom, profile: none, provider: typesafe.\n" +
				"Its base URL uses http://example.invalid.\n" +
				"Its base URL is plain http to another host, so every run exits 2 unless it passes --allow-http.",
		},
		{
			name: "should note ONESIE_PROVIDER when the setup is ready",
			env:  map[string]string{"ONESIE_PROVIDER": "openrouter", "OPENROUTER_API_KEY": "sentinel-secret"},
			want: "onesie is on PATH and a key resolves, profile: none, provider: openrouter.\n" +
				"ONESIE_PROVIDER is set, so a stored profile is not used. Unset ONESIE_PROVIDER to use a stored profile.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeConfig(t, dir, "profiles.json", tc.file, 0)
			chain := &preflightKeychain{t: t}
			out, errOut, code := runInConfig(t, dir, []string{"profile", "preflight"}, "", tc.env, withKeychain(chain))
			if code != 0 || errOut != "" || out != tc.want+"\n" {
				t.Fatalf("code %d, stderr %q\ngot\n%s\nwant\n%s", code, errOut, out, tc.want)
			}
		})
	}
	t.Run("should refuse mock mode before opening configuration", func(t *testing.T) {
		t.Parallel()

		store := creds.NewStore(creds.WithOpen(func(string) (*os.File, error) { t.Fatal("configuration opened"); return nil, nil }))
		_, _, code := runInConfig(t, t.TempDir(), []string{"profile", "preflight"}, "", map[string]string{"ONESIE_MOCK": "1"}, withCredentialStore(store))
		if code == 0 {
			t.Fatal("mock invocation succeeded")
		}
	})
	t.Run("should return unavailable for operational storage failures", func(t *testing.T) {
		t.Parallel()

		store := creds.NewStore(creds.WithOpen(func(string) (*os.File, error) { return nil, errors.New("sentinel-secret") }))
		out, errOut, code := runInConfig(t, t.TempDir(), []string{"profile", "preflight"}, "", nil, withCredentialStore(store))
		if code == 0 || out != "" || strings.Contains(errOut, "sentinel-secret") {
			t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("should report invalid configuration safely", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		writeConfig(t, dir, "profiles.json", `{"sentinel-secret":1}`, 0)
		out, errOut, code := runInConfig(t, dir, []string{"profile", "preflight"}, "", nil)
		want := "onesie is on PATH, but configuration is not ready. Run onesie profile show for manual inspection, then " +
			"correct the selected profile, provider or endpoint. The onesie-setup skill walks the user through it.\n"
		if code != 0 || errOut != "" || out != want {
			t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("should retain conversion permission and removed link warnings together", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("the fixture requires POSIX links")
		}
		dir := t.TempDir()
		target := filepath.Join(t.TempDir(), "legacy.json")
		if err := os.WriteFile(target, []byte(`{"providers":{"typesafe":{"api_key":"sentinel-secret"}}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "credentials.json")); err != nil {
			t.Fatal(err)
		}
		store := creds.NewStore(creds.WithChmod(func(string, os.FileMode) error { return errors.New("synthetic chmod failure") }))
		out, errOut, code := runInConfig(t, dir, []string{"profile", "preflight"}, "", nil, withCredentialStore(store))
		if code != 0 || errOut != "" || strings.Contains(out, "sentinel-secret") {
			t.Fatalf("code %d stdout %q stderr %q", code, out, errOut)
		}
		for _, want := range []string{"converted credentials.json", "could not set mode 600", "target still holds the key"} {
			if strings.Count(out, want) != 1 {
				t.Fatalf("missing %s: %s", want, out)
			}
		}
	})
	t.Run("should stay hidden from help and completion", func(t *testing.T) {
		t.Parallel()

		for _, args := range [][]string{{"--help"}, {"profile", "--help"}, {"__complete", "profile", ""}} {
			out, _, code := runInConfig(t, t.TempDir(), args, "", nil)
			if code != 0 || strings.Contains(out, "preflight") {
				t.Fatalf("code %d output %q", code, out)
			}
		}
	})
	t.Run("should give safe advice for legacy file permissions", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("the fixture requires POSIX permissions")
		}
		dir := t.TempDir()
		writeConfig(t, dir, "credentials.json", `{"providers":{"typesafe":{"api_key":"sentinel-secret"}}}`, 0o644)
		out, errOut, code := runInConfig(t, dir, []string{"profile", "preflight"}, "", nil)
		want := "onesie is on PATH, but configuration is not ready. Configuration is readable or writable by its group or " +
			"others. Set mode 600 on the file named by onesie profile show. The onesie-setup skill walks the user through it.\n"
		if code != 0 || errOut != "" || out != want {
			t.Fatalf("code %d stdout %q stderr %q", code, out, errOut)
		}
	})
}

type preflightKeychain struct{ t *testing.T }

func (k *preflightKeychain) Get(string, string) (string, error) {
	k.t.Fatal("keychain was read")
	return "", nil
}

func (k *preflightKeychain) Set(string, string, string) error {
	k.t.Fatal("keychain was written")
	return nil
}

func (k *preflightKeychain) Delete(string, string) error {
	k.t.Fatal("keychain was changed")
	return nil
}

func TestRenderReadiness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		facts readinessFacts
		want  string
	}{
		{
			name:  "should report a ready profile with a key in the file",
			facts: readinessFacts{provider: "typesafe", profile: "work", keySource: sourceFile, keyVariable: "TYPESAFE_API_KEY"},
			want:  "onesie is on PATH and a key resolves, profile: work, provider: typesafe.",
		},
		{
			name:  "should report a configured keychain entry without claiming it was read",
			facts: readinessFacts{provider: "openrouter", profile: "work", keySource: sourceKeychain, keyVariable: "OPENROUTER_API_KEY"},
			want:  "onesie is on PATH and a keychain entry is configured, retrieval is not verified, profile: work, provider: openrouter.",
		},
		{
			name:  "should report a keyless custom base URL as ready",
			facts: readinessFacts{provider: "typesafe", profile: "local", keySource: sourceNone, keyVariable: "TYPESAFE_API_KEY", custom: true, endpoint: "http://localhost:11435"},
			want: "onesie is on PATH and needs no key, since its base URL is custom, profile: local, provider: typesafe.\n" +
				"Its base URL uses http://localhost:11435.",
		},
		{
			name:  "should note ONESIE_PROVIDER after a ready verdict",
			facts: readinessFacts{provider: "openrouter", keySource: sourceEnv, keyVariable: "OPENROUTER_API_KEY", providerEnv: true},
			want: "onesie is on PATH and a key resolves, profile: none, provider: openrouter.\n" +
				"ONESIE_PROVIDER is set, so a stored profile is not used. Unset ONESIE_PROVIDER to use a stored profile.",
		},
		{
			name:  "should advise a key or a new profile when nothing chose a provider",
			facts: readinessFacts{provider: "typesafe", keySource: sourceNone, keyVariable: "TYPESAFE_API_KEY"},
			want: "onesie is on PATH, but no API key resolves for provider typesafe. Only --print-request, --print-questions, " +
				"--print-schema and --mock work until the user sets TYPESAFE_API_KEY or stores a profile with onesie profile " +
				"create NAME --provider typesafe. The onesie-setup skill walks the user through it.",
		},
		{
			name:  "should fold ONESIE_PROVIDER into the advice when no key resolves",
			facts: readinessFacts{provider: "berget", keySource: sourceNone, keyVariable: "BERGET_API_KEY", providerEnv: true},
			want: "onesie is on PATH, but no API key resolves for provider berget. Only --print-request, --print-questions, " +
				"--print-schema and --mock work until the user sets BERGET_API_KEY or stores a profile with onesie profile " +
				"create NAME --provider berget. ONESIE_PROVIDER is set, so no stored profile is " +
				"used. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should name the account variable and profile create when ONESIE_PROVIDER runs without an account",
			facts: readinessFacts{
				provider: "cloudflare", keySource: sourceEnv, keyVariable: "CLOUDFLARE_API_TOKEN",
				accountVariable: "CLOUDFLARE_ACCOUNT_ID", missingAccount: true, providerEnv: true,
			},
			want: "onesie is on PATH and a key resolves for provider cloudflare, but no account does. Every request fails " +
				"until the user sets CLOUDFLARE_ACCOUNT_ID or stores a profile with onesie profile create NAME --provider " +
				"cloudflare --account-id ACCOUNT_ID. ONESIE_PROVIDER is set, so no stored profile is " +
				"used. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should name profile create when --provider runs without an account",
			facts: readinessFacts{
				provider: "cloudflare", keySource: sourceEnv, keyVariable: "CLOUDFLARE_API_TOKEN",
				accountVariable: "CLOUDFLARE_ACCOUNT_ID", missingAccount: true,
			},
			want: "onesie is on PATH and a key resolves for provider cloudflare, but no account does. Every request fails " +
				"until the user sets CLOUDFLARE_ACCOUNT_ID or stores a profile with onesie profile create NAME --provider " +
				"cloudflare --account-id ACCOUNT_ID. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should print the bare default profile name in the account hint",
			facts: readinessFacts{
				provider: "cloudflare", profile: "cfk", keySource: sourceKeychain, keyVariable: "CLOUDFLARE_API_TOKEN",
				accountVariable: "CLOUDFLARE_ACCOUNT_ID", missingAccount: true,
			},
			want: "onesie is on PATH and a key resolves for profile cfk, but no account does. Every request fails until " +
				"the user runs onesie profile update cfk --account-id ACCOUNT_ID or sets CLOUDFLARE_ACCOUNT_ID. The " +
				"onesie-setup skill walks the user through it.",
		},
		{
			name: "should name profile update when a chosen profile has no account",
			facts: readinessFacts{
				provider: "cloudflare", profile: "cf", keySource: sourceFile, keyVariable: "CLOUDFLARE_API_TOKEN",
				accountVariable: "CLOUDFLARE_ACCOUNT_ID", missingAccount: true,
			},
			want: "onesie is on PATH and a key resolves for profile cf, but no account does. Every request fails until " +
				"the user runs onesie profile update cf --account-id ACCOUNT_ID or sets CLOUDFLARE_ACCOUNT_ID. The " +
				"onesie-setup skill walks the user through it.",
		},
		{
			name: "should cover a missing key and a missing account in one message",
			facts: readinessFacts{
				provider: "cloudflare", keySource: sourceNone, keyVariable: "CLOUDFLARE_API_TOKEN",
				accountVariable: "CLOUDFLARE_ACCOUNT_ID", missingAccount: true, providerEnv: true,
			},
			want: "onesie is on PATH, but no API key resolves for provider cloudflare, and no account does either. Only " +
				"--print-request, --print-questions, --print-schema and --mock work until the user sets " +
				"CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID, or stores a profile with onesie profile create NAME " +
				"--provider cloudflare --account-id ACCOUNT_ID. ONESIE_PROVIDER is set, so no stored profile is " +
				"used. The onesie-setup skill walks the user through it.",
		},
		{
			name:  "should name profile update with --key when a profile holds no key",
			facts: readinessFacts{provider: "typesafe", profile: "work", keySource: sourceNone, keyVariable: "TYPESAFE_API_KEY"},
			want: "onesie is on PATH, but no API key resolves for provider typesafe. Only --print-request, --print-questions, " +
				"--print-schema and --mock work until the user runs onesie profile update work --key. The onesie-setup " +
				"skill walks the user through it.",
		},
		{
			name: "should name profile update with --key and --account-id when a profile holds neither",
			facts: readinessFacts{
				provider: "cloudflare", profile: "cf", keySource: sourceNone, keyVariable: "CLOUDFLARE_API_TOKEN",
				accountVariable: "CLOUDFLARE_ACCOUNT_ID", missingAccount: true,
			},
			want: "onesie is on PATH, but no API key resolves for provider cloudflare, and no account does either. Only " +
				"--print-request, --print-questions, --print-schema and --mock work until the user runs onesie profile " +
				"update cf --key --account-id ACCOUNT_ID. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should drop the ONESIE_PROVIDER line beside a configuration problem",
			facts: readinessFacts{
				provider: "cloudflare", keySource: sourceEnv, keyVariable: "CLOUDFLARE_API_TOKEN",
				accountVariable: "CLOUDFLARE_ACCOUNT_ID", missingAccount: true, providerEnv: true,
				problem: "Unset or correct ONESIE_CACHE. Run onesie profile show for manual inspection.",
			},
			want: "onesie is on PATH, but configuration is not ready. Unset or correct ONESIE_CACHE. Run onesie profile " +
				"show for manual inspection. The onesie-setup skill walks the user through it.",
		},
		{
			name:  "should point a missing default profile at the setup skill",
			facts: readinessFacts{problem: "No profile is the default. Run onesie profile use NAME. onesie profile list names them."},
			want: "onesie is on PATH, but configuration is not ready. No profile is the default. Run onesie profile use " +
				"NAME. onesie profile list names them. The onesie-setup skill walks the user through it.",
		},
		{
			name: "should say where the key goes when ONESIE_BASE_URL sets the endpoint",
			facts: readinessFacts{
				provider: "typesafe", keySource: sourceEnv, keyVariable: "TYPESAFE_API_KEY",
				endpoint: "http://localhost:1234", endpointEnv: true, custom: true,
			},
			want: "onesie is on PATH and a key resolves, profile: none, provider: typesafe.\n" +
				"Its base URL comes from ONESIE_BASE_URL, so the key goes to http://localhost:1234.",
		},
		{
			name: "should warn about plain http to another host",
			facts: readinessFacts{
				provider: "typesafe", profile: "work", keySource: sourceFile, keyVariable: "TYPESAFE_API_KEY",
				endpoint: "http://example.invalid", custom: true, remoteHTTP: true,
			},
			want: "onesie is on PATH and a key resolves, profile: work, provider: typesafe.\n" +
				"Its base URL uses http://example.invalid.\n" +
				"Its base URL is plain http to another host, so every run exits 2 unless it passes --allow-http.",
		},
		{
			name: "should put migration notes before the verdict",
			facts: readinessFacts{
				provider: "typesafe", profile: "typesafe", keySource: sourceFile, keyVariable: "TYPESAFE_API_KEY",
				migrationFacts: migrationFacts{converted: true},
			},
			want: "onesie: converted credentials.json into profiles.json.\n" +
				"onesie is on PATH and a key resolves, profile: typesafe, provider: typesafe.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := renderReadiness(tc.facts); got != tc.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tc.want)
			}
		})
	}

	t.Run("should use the onesie-setup trigger phrase whenever no key resolves", func(t *testing.T) {
		t.Parallel()

		data, err := os.ReadFile(filepath.Join("..", "..", "skills", "onesie-setup", "skill.json"))
		if err != nil {
			t.Fatal(err)
		}

		var skill struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(data, &skill); err != nil {
			t.Fatal(err)
		}

		const phrase = "no API key resolves"
		if !strings.Contains(skill.Description, phrase) {
			t.Fatalf("the onesie-setup description no longer says %q: %s", phrase, skill.Description)
		}

		for _, tc := range tests {
			missingKey := tc.facts.problem == "" && tc.facts.keySource == sourceNone && !tc.facts.custom
			if missingKey && !strings.Contains(tc.want, phrase) {
				t.Errorf("%s: %q lacks %q", tc.name, tc.want, phrase)
			}
		}
	})

	t.Run("should point every verdict that is not ready at the onesie-setup skill", func(t *testing.T) {
		t.Parallel()

		for _, tc := range tests {
			missingKey := tc.facts.keySource == sourceNone && !tc.facts.custom
			notReady := tc.facts.problem != "" || missingKey || tc.facts.missingAccount
			if notReady && !strings.Contains(tc.want, "The onesie-setup skill walks the user through it.") {
				t.Errorf("%s: %q lacks the skill pointer", tc.name, tc.want)
			}
		}
	})

	t.Run("should escape untrusted control characters", func(t *testing.T) {
		t.Parallel()

		out := renderReadiness(readinessFacts{provider: "typesafe", profile: "work\n\t\x1bname", keySource: sourceFile})
		if strings.Count(out, "\n") != 0 || strings.ContainsAny(out, "\t\x1b") || !strings.Contains(out, `work\n\t\x1bname`) {
			t.Fatalf("unsafe report: %q", out)
		}
	})

	t.Run("should retain simultaneous migration and storage warnings", func(t *testing.T) {
		t.Parallel()

		out := renderReadiness(readinessFacts{problem: "Correct configuration.", migrationFacts: migrationFacts{converted: true, permissions: true, retained: true, linkRemoved: true}})
		for _, want := range []string{"converted credentials.json", "could not set mode 600", "target still holds the key"} {
			if !strings.Contains(out, want) {
				t.Fatal(out)
			}
		}
	})

	t.Run("should distinguish permission warnings", func(t *testing.T) {
		t.Parallel()

		out := renderReadiness(readinessFacts{problem: "Correct configuration.", migrationFacts: migrationFacts{permissions: true}})
		if !strings.Contains(out, "could not set mode 600") || !strings.Contains(out, "not protected by the filesystem") {
			t.Fatal(out)
		}
	})
}
