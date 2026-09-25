package interrupt_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/interrupt"
)

func TestWait(t *testing.T) {
	t.Parallel()

	t.Run("should hand back what the read returned", func(t *testing.T) {
		t.Parallel()

		got, err := interrupt.Wait(t.Context(), func() ([]byte, error) {
			return io.ReadAll(strings.NewReader("a ticket"))
		})
		if err != nil || string(got) != "a ticket" {
			t.Errorf("got %q, %v, want \"a ticket\" and no error", got, err)
		}
	})

	t.Run("should return the context error while the read is still blocked", func(t *testing.T) {
		t.Parallel()

		held, write := io.Pipe()
		t.Cleanup(func() { _ = write.Close() })

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := interrupt.Wait(ctx, func() ([]byte, error) { return io.ReadAll(held) })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	})

	t.Run("should return the context error when the context ends mid read", func(t *testing.T) {
		t.Parallel()

		held, write := io.Pipe()
		t.Cleanup(func() { _ = write.Close() })

		ctx, cancel := context.WithCancel(t.Context())
		started := make(chan struct{})

		go func() {
			<-started
			cancel()
		}()

		_, err := interrupt.Wait(ctx, func() ([]byte, error) {
			close(started)

			return io.ReadAll(held)
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("error = %v, want context.Canceled", err)
		}
	})
}
