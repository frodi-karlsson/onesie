package cache

import (
	"errors"
	"syscall"
	"time"
)

// The windows error codes a file another run holds open or is renaming onto comes back with. They are
// named here rather than taken from golang.org/x/sys/windows, which builds only for windows, so the
// retries are tested on every platform.
const (
	errorFileNotFound     syscall.Errno = 2
	errorAccessDenied     syscall.Errno = 5
	errorSharingViolation syscall.Errno = 32

	retryBudget  = 500 * time.Millisecond
	firstBackoff = time.Millisecond
)

func (s *Store) remove(path string) error {
	return s.retry(func() error { return s.opts.FS.Remove(path) }, contended)
}

func (s *Store) retry(op func() error, transient func(error) bool) error {
	// Only windows refuses to open, rename onto or remove a file another run holds, until it lets go.
	// The backoff follows cmd/internal/robustio in the Go tree.
	if s.opts.GOOS != "windows" {
		return op()
	}

	var slept time.Duration

	wait := firstBackoff

	for {
		err := op()
		if err == nil || !transient(err) || slept+wait > retryBudget {
			return err
		}

		s.opts.Sleep(wait)
		slept += wait
		wait += time.Duration(s.opts.Random() * float64(wait))
	}
}

func (s *Store) stillHeld(err error, transient func(error) bool) bool {
	return s.opts.GOOS == "windows" && transient(err)
}

func contended(err error) bool {
	return errors.Is(err, errorAccessDenied) || errors.Is(err, errorSharingViolation)
}

func contendedOrMissing(err error) bool {
	// Windows reports a missing file for a rename that races another rename onto the same name.
	return contended(err) || errors.Is(err, errorFileNotFound)
}
