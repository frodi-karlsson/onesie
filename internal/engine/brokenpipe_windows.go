//go:build windows

package engine

import (
	"errors"
	"syscall"
)

func platformBrokenPipe(err error) bool {
	// Windows never reports a broken pipe as EPIPE. A write past a closed pipe surfaces as
	// ERROR_BROKEN_PIPE, and a write to a pipe the reader is in the middle of closing surfaces as
	// ERROR_NO_DATA, which has no named constant in syscall.
	const errorNoData = syscall.Errno(0xe8)

	return errors.Is(err, syscall.ERROR_BROKEN_PIPE) || errors.Is(err, errorNoData)
}
