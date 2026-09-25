//go:build aix || solaris

package cli

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func lockHandle(file *os.File) error {
	whole := unix.Flock_t{Type: unix.F_WRLCK, Whence: io.SeekStart}

	err := unix.FcntlFlock(file.Fd(), unix.F_SETLK, &whole)
	if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EACCES) {
		return errLocked
	}

	return err
}

func unlockHandle(file *os.File, path string, owned bool, remove func(string) error) error {
	// Removed while the lock is still held, so no run can take a lock on the old file after this one
	// lets go of it. The close lets go of the lock.
	return errors.Join(removeOwned(path, owned, remove), file.Close())
}
