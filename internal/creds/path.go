// Package creds resolves, reads and writes the profiles file that holds each profile's API key, and
// converts the credentials.json it replaces.
package creds

import (
	"errors"
	"fmt"
	"path/filepath"
)

const (
	appName      = "onesie"
	fileName     = "credentials.json"
	profilesName = "profiles.json"
)

// ProfilesPath reports where the profiles file lives.
func ProfilesPath(env Env) (string, error) {
	dir, err := Dir(env)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, profilesName), nil
}

// Dir reports onesie's config dir, which holds the credential and profiles files and the saved
// question files.
func Dir(env Env) (string, error) {
	if dir := lookup(env, "ONESIE_CONFIG_DIR"); dir != "" {
		return filepath.Clean(dir), nil
	}

	if dir := lookup(env, "XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, appName), nil
	}

	if env.GOOS == "windows" {
		if dir := lookup(env, "APPDATA"); dir != "" {
			return filepath.Join(dir, appName), nil
		}
	}

	home, err := env.Home()
	if err != nil {
		return "", fmt.Errorf(
			"finding the home directory for the config dir, where the profiles file lives: %w", err)
	}

	if home == "" {
		return "", errors.New(
			"cannot find a home directory for the config dir, where the profiles file lives")
	}

	return filepath.Join(home, ".config", appName), nil
}

func lookup(env Env, name string) string {
	value, _ := env.Lookup(name)

	return value
}

// Env is everything Path needs from outside the process, so a test needs no real home directory and
// no environment mutation.
type Env struct {
	// Lookup reads an environment variable. os.LookupEnv in production.
	Lookup func(string) (string, bool)
	// GOOS names the operating system. runtime.GOOS in production.
	GOOS string
	// Home returns the user's home directory. os.UserHomeDir in production.
	Home func() (string, error)
}
