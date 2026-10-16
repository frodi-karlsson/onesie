package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWrite(t *testing.T) {
	t.Parallel()

	t.Run("should match the committed example image", func(t *testing.T) {
		t.Parallel()

		want, err := square()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got, err := os.ReadFile(filepath.Join("..", "..", "examples", "images", "square.png"))
		if err != nil {
			t.Fatalf("reading the committed image: %v", err)
		}

		if !bytes.Equal(got, want) {
			t.Error("examples/images/square.png is stale. Run make examples-image")
		}
	})
}
