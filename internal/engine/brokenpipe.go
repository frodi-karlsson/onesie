package engine

import (
	"errors"
	"io"
	"syscall"
)

// BrokenPipe reports whether a write failed because the consumer stopped reading, which every
// onesie writer treats as a successful end.
func BrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe) ||
		platformBrokenPipe(err)
}
