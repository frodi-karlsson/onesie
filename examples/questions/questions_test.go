package questions_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/examples/questions"
)

func TestNames(t *testing.T) {
	t.Parallel()

	t.Run("should list the four starter sets in name order", func(t *testing.T) {
		t.Parallel()

		want := []string{"moderation", "personal-data", "prompt-injection", "shell-safety"}
		if got := questions.Names(); !slices.Equal(got, want) {
			t.Errorf("Names = %v, want %v", got, want)
		}
	})

	t.Run("should list every question file on disk", func(t *testing.T) {
		t.Parallel()

		if got, want := questions.Names(), namesOnDisk(t); !slices.Equal(got, want) {
			t.Errorf("Names = %v, want the files on disk %v", got, want)
		}
	})
}

func TestRead(t *testing.T) {
	t.Parallel()

	t.Run("should return the file on disk for a built-in name", func(t *testing.T) {
		t.Parallel()

		want, err := os.ReadFile("shell-safety.yaml")
		if err != nil {
			t.Fatal(err)
		}

		got, ok := questions.Read("shell-safety")
		if !ok || !bytes.Equal(got, want) {
			t.Errorf("Read = %q, %v, want the file on disk and true", got, ok)
		}
	})

	t.Run("should report false for a name that is not built in", func(t *testing.T) {
		t.Parallel()

		if got, ok := questions.Read("nope"); ok {
			t.Errorf("Read = %q, true, want false", got)
		}
	})

	t.Run("should embed every file on disk byte for byte", func(t *testing.T) {
		t.Parallel()

		for _, name := range namesOnDisk(t) {
			want, err := os.ReadFile(name + ".yaml")
			if err != nil {
				t.Fatal(err)
			}

			if got, ok := questions.Read(name); !ok || !bytes.Equal(got, want) {
				t.Errorf("Read(%q) differs from %s.yaml on disk", name, name)
			}
		}
	})
}

func namesOnDisk(t *testing.T) []string {
	t.Helper()

	paths, err := filepath.Glob("*.yaml")
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, strings.TrimSuffix(path, ".yaml"))
	}

	slices.Sort(names)

	return names
}
