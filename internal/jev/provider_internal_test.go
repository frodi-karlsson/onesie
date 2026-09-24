package jev

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeOpenRouterModels(t *testing.T) {
	t.Parallel()

	t.Run("should map id, description and created from the captured listing", func(t *testing.T) {
		t.Parallel()

		body, err := os.ReadFile(filepath.Join("testdata", "openrouter-models.json"))
		if err != nil {
			t.Fatalf("reading the fixture: %v", err)
		}

		cards, err := decodeOpenRouterModels(body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(cards) != 2 {
			t.Fatalf("got %d models, want 2", len(cards))
		}

		latest := cards[0]
		if latest.Name != "~typesafe/jev-latest" {
			t.Errorf("Name = %q, want ~typesafe/jev-latest", latest.Name)
		}

		if !strings.HasPrefix(latest.Description, "This model always redirects") {
			t.Errorf("Description = %q, want the listing's description", latest.Description)
		}

		if latest.ReleaseDate != "2026-09-18" {
			t.Errorf("ReleaseDate = %q, want 2026-09-18", latest.ReleaseDate)
		}
	})

	t.Run("should fail when data is absent", func(t *testing.T) {
		t.Parallel()

		_, err := decodeOpenRouterModels([]byte(`{"models":[]}`))
		if err == nil || !strings.Contains(err.Error(), "expected a models list") {
			t.Errorf("error = %v, want expected a models list", err)
		}
	})

	t.Run("should return an empty list for empty data", func(t *testing.T) {
		t.Parallel()

		cards, err := decodeOpenRouterModels([]byte(`{"data":[]}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if cards == nil || len(cards) != 0 {
			t.Errorf("cards = %#v, want an empty slice", cards)
		}
	})
}
