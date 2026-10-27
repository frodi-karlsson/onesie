//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly || windows || aix || solaris)

package answersfile

import (
	"errors"
	"os"
)

func lockHandle(_ *os.File) error {
	return ErrNoLock
}

func unlockHandle(file *os.File, path string, owned bool, remove func(string) error) error {
	return errors.Join(removeOwned(path, owned, remove), file.Close())
}
