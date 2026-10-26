package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain runs every test in this directory, including the cli_test package, in a temporary home
// and config dir, so no test reads or converts the developer's real configuration.
func TestMain(m *testing.M) {
	os.Exit(runIsolated(m))
}

func runIsolated(m *testing.M) int {
	dir, err := os.MkdirTemp("", "onesie-cli-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "onesie: isolating the tests:", err)

		return 1
	}

	defer func() {
		if removeErr := os.RemoveAll(dir); removeErr != nil {
			fmt.Fprintln(os.Stderr, "onesie: removing", dir, removeErr)
		}
	}()

	if err := isolate(dir); err != nil {
		fmt.Fprintln(os.Stderr, "onesie: isolating the tests:", err)

		return 1
	}

	return m.Run()
}

func isolate(dir string) error {
	home := filepath.Join(dir, "home")

	set := map[string]string{
		"HOME":              home,
		"USERPROFILE":       home,
		"XDG_CONFIG_HOME":   filepath.Join(dir, "xdg-config"),
		"XDG_CACHE_HOME":    filepath.Join(dir, "xdg-cache"),
		"APPDATA":           filepath.Join(dir, "appdata"),
		"LOCALAPPDATA":      filepath.Join(dir, "localappdata"),
		"ONESIE_CONFIG_DIR": filepath.Join(dir, "config"),
	}

	for name, value := range set {
		if err := os.Setenv(name, value); err != nil {
			return err
		}
	}

	// The key variables stay, so the live suite still finds its keys.
	for _, name := range []string{"ONESIE_PROFILE", "ONESIE_PROVIDER", "ONESIE_MOCK"} {
		if err := os.Unsetenv(name); err != nil {
			return err
		}
	}

	return nil
}

func TestIsolate(t *testing.T) {
	t.Run("should run every test under one temporary home and config dir", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		config := os.Getenv("ONESIE_CONFIG_DIR")
		if config == "" || filepath.Dir(config) != filepath.Dir(home) || !strings.HasPrefix(home, os.TempDir()) {
			t.Errorf("home %s and ONESIE_CONFIG_DIR %s do not share the temporary directory", home, config)
		}

		for _, name := range []string{"ONESIE_PROFILE", "ONESIE_PROVIDER", "ONESIE_MOCK"} {
			if value, set := os.LookupEnv(name); set {
				t.Errorf("%s=%s reached the tests", name, value)
			}
		}
	})
}
