package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestNewProfilePreflightCmd(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		env        map[string]string
		want       string
	}{
		{name: "should preserve environment endpoint key advice", env: map[string]string{"TYPESAFE_API_KEY": "sentinel-secret", "ONESIE_BASE_URL": "http://localhost:1234/path"}, want: "so the key goes to http://localhost:1234."},
		{name: "should report both missing Cloudflare settings", env: map[string]string{"ONESIE_PROVIDER": "cloudflare"}, want: "No API key resolves. Set CLOUDFLARE_API_TOKEN."},
		{name: "should explain a missing default profile", file: `{"profiles":{"work":{"provider":"typesafe","api_key":"sentinel-secret"}}}`, want: "Run onesie profile use NAME"},
		{name: "should name the actual provider key variable", env: map[string]string{"ONESIE_PROVIDER": "berget"}, want: "Set BERGET_API_KEY"},
		{name: "should safely refuse a multiline setting", env: map[string]string{"ONESIE_CACHE": "bad\nsentinel-secret"}, want: "Unset or correct ONESIE_CACHE"},
		{name: "should safely refuse a malformed profile name", env: map[string]string{"ONESIE_PROFILE": "bad\nsentinel-secret"}, want: "configuration is not ready"},
		{name: "should omit a valid endpoint path", env: map[string]string{"ONESIE_BASE_URL": "http://localhost:1234/sentinel-secret"}, want: "http://localhost:1234."},
		{name: "should describe the configured keychain without reading it", file: `{"default":"work","profiles":{"work":{"provider":"typesafe","store":"keychain","account":"work@abc","key_end":"SENT"}}}`, want: "a keychain entry is configured, retrieval is not verified"},
		{name: "should warn about remote HTTP", env: map[string]string{"ONESIE_BASE_URL": "http://example.invalid/sentinel-secret"}, want: "every run exits 2 unless it passes --allow-http"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfig(t, dir, "profiles.json", tc.file, 0)
			chain := &preflightKeychain{t: t}
			out, errOut, code := runInConfig(t, dir, []string{"profile", "preflight"}, "", tc.env, withKeychain(chain))
			if code != 0 || errOut != "" || !strings.Contains(out, tc.want) || strings.Contains(out, "sentinel-secret") || strings.Contains(out, "SENT") {
				t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
			}
		})
	}
	t.Run("should refuse mock mode before opening configuration", func(t *testing.T) {
		store := creds.NewStore(creds.WithOpen(func(string) (*os.File, error) { t.Fatal("configuration opened"); return nil, nil }))
		_, _, code := runInConfig(t, t.TempDir(), []string{"profile", "preflight"}, "", map[string]string{"ONESIE_MOCK": "1"}, withCredentialStore(store))
		if code == 0 {
			t.Fatal("mock invocation succeeded")
		}
	})
	t.Run("should return unavailable for operational storage failures", func(t *testing.T) {
		store := creds.NewStore(creds.WithOpen(func(string) (*os.File, error) { return nil, errors.New("sentinel-secret") }))
		out, errOut, code := runInConfig(t, t.TempDir(), []string{"profile", "preflight"}, "", nil, withCredentialStore(store))
		if code == 0 || out != "" || strings.Contains(errOut, "sentinel-secret") {
			t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})
	t.Run("should report invalid configuration safely", func(t *testing.T) {
		dir := t.TempDir()
		writeConfig(t, dir, "profiles.json", `{"sentinel-secret":1}`, 0)
		out, errOut, code := runInConfig(t, dir, []string{"profile", "preflight"}, "", nil)
		if code != 0 || errOut != "" || !strings.Contains(out, "configuration is not ready") || strings.Contains(out, "sentinel-secret") {
			t.Fatalf("code %d, stdout %q, stderr %q", code, out, errOut)
		}
	})

	t.Run("should retain conversion permission and removed link warnings together", func(t *testing.T) {
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
		for _, args := range [][]string{{"--help"}, {"profile", "--help"}, {"__complete", "profile", ""}} {
			out, _, code := runInConfig(t, t.TempDir(), args, "", nil)
			if code != 0 || strings.Contains(out, "preflight") {
				t.Fatalf("code %d output %q", code, out)
			}
		}
	})
	t.Run("should give safe advice for legacy file permissions", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the fixture requires POSIX permissions")
		}
		dir := t.TempDir()
		writeConfig(t, dir, "credentials.json", `{"providers":{"typesafe":{"api_key":"sentinel-secret"}}}`, 0o644)
		out, errOut, code := runInConfig(t, dir, []string{"profile", "preflight"}, "", nil)
		if code != 0 || errOut != "" || !strings.Contains(out, "Set mode 600 on the file named by onesie profile show") || strings.Contains(out, "sentinel-secret") {
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
	t.Run("should escape untrusted control characters", func(t *testing.T) {
		out := renderReadiness(readinessFacts{provider: "typesafe", profile: "work\n\t\x1bname", keySource: sourceFile})
		if strings.Count(out, "\n") != 0 || strings.ContainsAny(out, "\t\x1b") || !strings.Contains(out, `work\n\t\x1bname`) {
			t.Fatalf("unsafe report: %q", out)
		}
	})
	t.Run("should retain simultaneous migration and storage warnings", func(t *testing.T) {
		out := renderReadiness(readinessFacts{problem: "Correct configuration.", migrationFacts: migrationFacts{converted: true, permissions: true, retained: true, linkRemoved: true}})
		for _, want := range []string{"converted credentials.json", "could not set mode 600", "target still holds the key"} {
			if !strings.Contains(out, want) {
				t.Fatal(out)
			}
		}
	})
	t.Run("should distinguish permission warnings", func(t *testing.T) {
		out := renderReadiness(readinessFacts{problem: "Correct configuration.", migrationFacts: migrationFacts{permissions: true}})
		if !strings.Contains(out, "could not set mode 600") || !strings.Contains(out, "not protected by the filesystem") {
			t.Fatal(out)
		}
	})
}
