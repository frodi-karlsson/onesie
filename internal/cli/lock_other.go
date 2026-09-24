//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly || windows)

package cli

import (
	"errors"
	"os"
)

func lockHandle(_ *os.File) error {
	return nil
}

func unlockHandle(file *os.File, path string, owned bool) error {
	var removeErr error
	if owned {
		removeErr = os.Remove(path)
		if errors.Is(removeErr, os.ErrNotExist) {
			removeErr = nil
		}
	}

	return errors.Join(removeErr, file.Close())
}
