// Package cache stores API responses on disk under a sha256 key, with private modes, atomic writes,
// a lifetime for model aliases and a size cap enforced by least recently used eviction.
package cache

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

const appName = "onesie"

// Dir reports where the response cache lives, from ONESIE_CACHE_DIR, XDG_CACHE_HOME or the
// system's own cache location.
func Dir(env creds.Env) (string, error) {
	if dir := lookup(env, "ONESIE_CACHE_DIR"); dir != "" {
		return filepath.Clean(dir), nil
	}

	if dir := lookup(env, "XDG_CACHE_HOME"); dir != "" {
		return filepath.Join(dir, appName), nil
	}

	if env.GOOS == "windows" {
		if dir := lookup(env, "LOCALAPPDATA"); dir != "" {
			return filepath.Join(dir, appName), nil
		}
	}

	home, err := env.Home()
	if err != nil {
		return "", fmt.Errorf("onesie: finding the home directory for the cache dir: %w", err)
	}

	if home == "" {
		return "", errors.New("onesie: cannot find a home directory for the cache dir. Set ONESIE_CACHE_DIR")
	}

	switch env.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Caches", appName), nil
	case "windows":
		return filepath.Join(home, "AppData", "Local", appName), nil
	default:
		return filepath.Join(home, ".cache", appName), nil
	}
}

func lookup(env creds.Env, name string) string {
	value, _ := env.Lookup(name)

	return value
}
