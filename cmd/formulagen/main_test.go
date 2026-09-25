package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	t.Run("should write the formula to the out path, creating its directory", func(t *testing.T) {
		t.Parallel()

		out := filepath.Join(t.TempDir(), "Formula", "onesie.rb")
		checksums := filepath.Join("..", "..", "internal", "formula", "testdata", "checksums.txt")

		if err := run([]string{"-version", "v1.2.3", "-checksums", checksums, "-out", out}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}

		if !strings.Contains(string(data), `version "1.2.3"`) {
			t.Errorf("formula = %q, want version 1.2.3", data)
		}
	})

	t.Run("should fail without a version", func(t *testing.T) {
		t.Parallel()

		err := run([]string{"-out", filepath.Join(t.TempDir(), "onesie.rb")})
		if err == nil || !strings.Contains(err.Error(), "-version is required") {
			t.Fatalf("err = %v, want -version is required", err)
		}
	})
}
