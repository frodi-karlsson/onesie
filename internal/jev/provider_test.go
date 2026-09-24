package jev_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

func TestProviderNamed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "should return typesafe for typesafe", input: "typesafe", want: "typesafe"},
		{name: "should return typesafe for an empty name", input: "", want: "typesafe"},
		{name: "should return openrouter for openrouter", input: "openrouter", want: "openrouter"},
		{name: "should trim the name", input: " openrouter ", want: "openrouter"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider, err := jev.ProviderNamed(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if provider.Name != tc.want {
				t.Errorf("Name = %q, want %q", provider.Name, tc.want)
			}
		})
	}

	t.Run("should reject an unknown name and list the valid ones", func(t *testing.T) {
		t.Parallel()

		_, err := jev.ProviderNamed("nope")
		if !errors.Is(err, jev.ErrValidation) {
			t.Fatalf("error got %v, want ErrValidation", err)
		}

		for _, want := range []string{"nope", "typesafe", "openrouter"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to name %q", err.Error(), want)
			}
		}
	})
}
