//go:build windows

package cli

import (
	"errors"
	"math"
	"os"

	"golang.org/x/sys/windows"
)

func lockHandle(file *os.File) error {
	// A byte far past the end, since a Windows lock is mandatory and refuses a read or a write of any
	// byte it covers.
	err := windows.LockFileEx(windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, farByte())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLocked
	}

	return err
}

func unlockHandle(file *os.File, path string, owned bool) error {
	err := errors.Join(windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, farByte()), file.Close())

	// After the close, since Windows refuses to remove a file with a handle open on it. That same
	// refusal leaves the file in place for a run that opened it in the meantime.
	if owned && err == nil {
		removeUnlessOpen(path)
	}

	return err
}

func farByte() *windows.Overlapped {
	return &windows.Overlapped{Offset: math.MaxUint32, OffsetHigh: math.MaxInt32}
}

func removeUnlessOpen(path string) {
	if err := os.Remove(path); err != nil {
		return
	}
}
