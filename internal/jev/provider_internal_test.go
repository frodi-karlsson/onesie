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

func TestDecodeBergetModels(t *testing.T) {
	t.Parallel()

	t.Run("should keep only the system-one models from the captured listing", func(t *testing.T) {
		t.Parallel()

		body, err := os.ReadFile(filepath.Join("testdata", "berget-models.json"))
		if err != nil {
			t.Fatalf("reading the fixture: %v", err)
		}

		cards, err := decodeBergetModels(body)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []ModelCard{
			{
				Name:        "Qwen/Qwen3.5-2B",
				Description: "aliases systemone, systemone-qwen3.5-2b",
				ReleaseDate: "2026-09-23",
			},
			{
				Name:        "convaiinnovations/laya",
				Description: "aliases systemone-laya, laya-latest",
				ReleaseDate: "2026-09-24",
			},
		}

		if len(cards) != len(want) {
			t.Fatalf("cards = %+v, want %+v", cards, want)
		}

		for i := range want {
			if cards[i] != want[i] {
				t.Errorf("card %d = %+v, want %+v", i, cards[i], want[i])
			}
		}
	})

	t.Run("should leave the description empty for a model with no aliases", func(t *testing.T) {
		t.Parallel()

		cards, err := decodeBergetModels([]byte(`{"data":[{"id":"x","model_type":"system-one","aliases":[]}]}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(cards) != 1 || cards[0] != (ModelCard{Name: "x"}) {
			t.Errorf("cards = %+v, want one card named x", cards)
		}
	})

	t.Run("should fail when data is absent", func(t *testing.T) {
		t.Parallel()

		_, err := decodeBergetModels([]byte(`{"models":[]}`))
		if err == nil || !strings.Contains(err.Error(), "expected a models list") {
			t.Errorf("error = %v, want expected a models list", err)
		}
	})

	t.Run("should return an empty list when no model is system-one", func(t *testing.T) {
		t.Parallel()

		cards, err := decodeBergetModels([]byte(`{"data":[{"id":"mistral","model_type":"text"}]}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if cards == nil || len(cards) != 0 {
			t.Errorf("cards = %#v, want an empty slice", cards)
		}
	})
}
