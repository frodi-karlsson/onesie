//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly || windows || aix || solaris)

package cli

import (
	"errors"
	"os"
)

var errNoLock = errors.New("onesie has no file lock on this platform, so it cannot keep another run " +
	"out of the file. Drop --resume to start over")

func lockHandle(_ *os.File) error {
	return errNoLock
}

func unlockHandle(file *os.File, path string, owned bool) error {
	return errors.Join(removeOwned(path, owned), file.Close())
}
