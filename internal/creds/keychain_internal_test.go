package creds

import (
	"strings"
	"testing"
)

func TestKeychainAccount(t *testing.T) {
	t.Parallel()

	t.Run("should start with the profile name and differ between profiles files", func(t *testing.T) {
		t.Parallel()

		first := keychainAccount("work", "/a/profiles.json")
		second := keychainAccount("work", "/b/profiles.json")

		if !strings.HasPrefix(first, "work@") {
			t.Errorf("account = %q, want it to start with work@", first)
		}

		if first == second {
			t.Errorf("two profiles files share the account %q", first)
		}

		if again := keychainAccount("work", "/a/profiles.json"); again != first {
			t.Errorf("account = %q then %q, want it stable", first, again)
		}
	})
}
