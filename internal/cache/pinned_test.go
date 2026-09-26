package cache_test

import (
	"testing"

	"github.com/frodi-karlsson/onesie/internal/cache"
)

func TestPinned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider string
		model    string
		want     bool
	}{
		{name: "should pin a three part version on typesafe", provider: "typesafe", model: "jev-1.13.0", want: true},
		{name: "should pin a two part version on typesafe", provider: "typesafe", model: "jev-2.0", want: true},
		{name: "should not pin the latest alias", provider: "typesafe", model: "jev-latest"},
		{name: "should not pin a one part version", provider: "typesafe", model: "jev-1"},
		{name: "should not pin a prerelease", provider: "typesafe", model: "jev-1.13.0-rc.1"},
		{name: "should not pin a four part version", provider: "typesafe", model: "jev-1.2.3.4"},
		{name: "should not pin a bare version", provider: "typesafe", model: "-1.2"},
		{name: "should not pin the mock model", provider: "typesafe", model: "mock"},
		{name: "should not pin an empty name", provider: "typesafe", model: ""},
		{name: "should not pin a version on openrouter", provider: "openrouter", model: "typesafe/jev-1.13"},
		{name: "should not pin an alias on openrouter", provider: "openrouter", model: "typesafe/jev-latest"},
		{name: "should not pin a typesafe name on another provider", provider: "openrouter", model: "jev-1.13.0"},
		{name: "should not pin the default alias on berget", provider: "berget", model: "systemone"},
		{name: "should not pin a canonical id on berget", provider: "berget", model: "Qwen/Qwen3.5-2B"},
		{name: "should not pin a versioned name on berget", provider: "berget", model: "jev-1.13.0"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := cache.Pinned(tc.provider, tc.model); got != tc.want {
				t.Errorf("Pinned(%q, %q) = %v, want %v", tc.provider, tc.model, got, tc.want)
			}
		})
	}
}
