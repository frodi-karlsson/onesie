//go:build integration

package jev_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func openRouterKey(t *testing.T) string {
	t.Helper()

	if key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); key != "" {
		return key
	}

	if key := fromDotEnv(t, "OPENROUTER_API_KEY"); key != "" {
		return key
	}

	t.Skip("no OPENROUTER_API_KEY in the environment or .env, skipping the OpenRouter live cases")

	return ""
}

func fromDotEnv(t *testing.T, name string) string {
	t.Helper()

	// Deliberately minimal: KEY=VALUE per line, with no quoting, interpolation or export keyword.

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
