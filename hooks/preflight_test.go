package hooks_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPreflight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook requires POSIX sh")
	}
	for _, tc := range []struct{ name, script, want string }{
		{"should report a missing binary", "", "onesie is not on PATH"},
		{"should display the complete helper report", "printf '%s\\n' 'safe report'", "safe report\n"},
		{"should suppress old binary usage", "echo 'usage sentinel' >&2; exit 1", "automatic readiness is unavailable"},
		{"should handle an empty helper response", "exit 0", "automatic readiness is unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			if tc.script != "" {
				script := "#!/bin/sh\nif [ \"$1 $2\" = 'profile preflight' ]; then\n" + tc.script + "\nfi\n"
				if err := os.WriteFile(filepath.Join(bin, "onesie"), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			out := runPreflight(t, bin, nil)
			if !strings.Contains(out, tc.want) || strings.Contains(out, "sentinel") {
				t.Fatalf("unexpected output: %s", out)
			}
		})
	}
}

func TestPreflightCompatibility(t *testing.T) {
	t.Run("should retain version mismatch and bounded question discovery with an old binary", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("the hook requires POSIX sh")
		}
		bin := t.TempDir()
		script := `#!/bin/sh
case "$1 $2" in
 'profile preflight') echo 'legacy usage sentinel' >&2; exit 1 ;;
 '--version ') echo 'onesie v999.0.0' ;;
 'questions ') i=1; while [ "$i" -le 25 ]; do echo "saved$i built in"; i=$((i + 1)); done ;;
esac
`
		if err := os.WriteFile(filepath.Join(bin, "onesie"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		out := runPreflight(t, bin, nil)
		for _, want := range []string{"automatic readiness is unavailable", "onesie --version reports 999.0.0", "saved20 built in", "and 5 more"} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in %s", want, out)
			}
		}
		if strings.Contains(out, "sentinel") || strings.Contains(out, "saved21") {
			t.Fatalf("unexpected output: %s", out)
		}
	})
}

func TestPreflightBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook requires POSIX sh")
	}
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	t.Cleanup(func() {
		if requests.Load() != 0 {
			t.Errorf("provider received %d requests", requests.Load())
		}
	})
	bin := t.TempDir()
	build := exec.CommandContext(t.Context(), "go", "build", "-o", filepath.Join(bin, "onesie"), "../cmd/onesie")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"should report no key", nil, "no API key resolves"},
		{"should accept a keyless loopback endpoint", []string{"ONESIE_BASE_URL=" + server.URL + "/sentinel-secret"}, "needs no key"},
		{"should require the Cloudflare account", []string{"ONESIE_PROVIDER=cloudflare", "CLOUDFLARE_API_TOKEN=sentinel-secret"}, "no account resolves"},
		{"should safely refuse multiline switches", []string{"ONESIE_CACHE=bad\nsentinel-secret"}, "Unset or correct ONESIE_CACHE"},
		{"should safely refuse invalid long context", []string{"ONESIE_LONG_CONTEXT=sentinel-secret"}, "Unset or correct ONESIE_LONG_CONTEXT"},
		{"should refuse mock mode", []string{"ONESIE_MOCK=1"}, "automatic readiness is unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runPreflight(t, bin, tc.env)
			if strings.Count(out, tc.want) != 1 || strings.Contains(out, "sentinel-secret") || strings.Contains(out, "secret") {
				t.Fatalf("unexpected output: %s", out)
			}
			if !strings.Contains(out, "Question files -f NAME loads:") {
				t.Fatalf("missing questions: %s", out)
			}
		})
	}
}

func runPreflight(t *testing.T, bin string, env []string) string {
	t.Helper()
	home := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "/bin/sh", "preflight.sh")
	cmd.Env = append([]string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "ONESIE_CONFIG_DIR=" + filepath.Join(home, "config")}, env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hook: %v: %s", err, out)
	}
	return string(out)
}
