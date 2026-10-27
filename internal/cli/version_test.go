package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/limits"
)

func TestWriteLimits(t *testing.T) {
	t.Parallel()

	t.Run("should write each limit as its name and value on a line of its own", func(t *testing.T) {
		t.Parallel()

		var want strings.Builder

		for _, entry := range limits.Report() {
			want.WriteString(entry.Name + " " + entry.Value + "\n")
		}

		var got strings.Builder

		if err := writeLimits(&got); err != nil {
			t.Fatalf("writeLimits: %v", err)
		}

		if got.String() != want.String() {
			t.Errorf("writeLimits wrote\n%s\nwant\n%s", got.String(), want.String())
		}
	})

	t.Run("should return the error of a write that fails", func(t *testing.T) {
		t.Parallel()

		if err := writeLimits(brokenWriter{}); !errors.Is(err, errBroken) {
			t.Errorf("writeLimits = %v, want %v", err, errBroken)
		}
	})
}
