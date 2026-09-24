//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package cli

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockHandle(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return errLocked
	}

	return err
}

func unlockHandle(file *os.File, path string, owned bool) error {
	// Removed while the lock is still held, so no run can take a lock on the old file after this one
	// lets go of it.
	var removeErr error
	if owned {
		removeErr = os.Remove(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
	}

	return errors.Join(removeErr, file.Close())
}
