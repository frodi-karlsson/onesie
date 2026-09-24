package engine

import (
	"errors"
	"io"
	"syscall"
)

// BrokenPipe reports whether a write failed because the consumer stopped reading. Every onesie
// writer treats that as a successful end rather than a failure, so it is exported for the ones
// outside this package.
func BrokenPipe(err error) bool {
	return errors.Is(err, syscall.EPIPE) || errors.Is(err, io.ErrClosedPipe) ||
		platformBrokenPipe(err)
}
