//go:build integration

package jev_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// apiKey returns the key for the live API, or skips the test. It never logs the value.
func apiKey(t *testing.T) string {
	t.Helper()

	if key := strings.TrimSpace(os.Getenv("TYPESAFE_API_KEY")); key != "" {
		return key
	}

	if key := fromDotEnv(t, "TYPESAFE_API_KEY"); key != "" {
		return key
	}

	t.Skip("no TYPESAFE_API_KEY in the environment or .env, skipping the live API suite")

	return ""
}

// fromDotEnv reads one value out of the repo's gitignored .env. It is deliberately minimal:
// KEY=VALUE per line, no quoting, no interpolation, no export keyword.
func fromDotEnv(t *testing.T, name string) string {
	t.Helper()

	file, err := os.Open(filepath.Join("..", "..", ".env"))
	if err != nil {
		return ""
	}

	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Logf("closing .env: %v", closeErr)
		}
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if found && strings.TrimSpace(key) == name {
			return strings.TrimSpace(value)
		}
	}

	return ""
}
