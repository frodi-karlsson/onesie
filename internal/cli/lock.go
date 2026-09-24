package cli

import (
	"errors"
	"io/fs"
	"os"
)

const (
	lockSuffix   = ".onesie.lock"
	lockAttempts = 3
)

var errLocked = errors.New("held by another process")

func lockAnswers(answers string) (release func() error, err error) {
	// An OS lock rather than a file created with O_EXCL, since the kernel lets go of an OS lock when
	// the process dies, and a second Ctrl-C or a kill would otherwise leave a lock that refuses
	// every later resume.
	release, err = lockAt(answers+lockSuffix, os.O_RDONLY|os.O_CREATE, true)
	if !errors.Is(err, fs.ErrPermission) {
		return release, err
	}

	// A directory that refuses the lock file refuses the compaction rename too, so the answers file
	// itself is locked instead. With no answers file there is nothing another run could lose.
	release, err = lockAt(answers, os.O_RDONLY, false)
	if errors.Is(err, fs.ErrNotExist) {
		return func() error { return nil }, nil
	}

	return release, err
}

func lockAt(path string, flag int, owned bool) (func() error, error) {
	for range lockAttempts {
		file, err := os.OpenFile(path, flag, 0o600)
		if err != nil {
			return nil, err
		}

		if err := lockHandle(file); err != nil {
			return nil, errors.Join(err, file.Close())
		}

		// The run that held the lock removes the file as it lets go, so a lock taken on a file
		// already removed guards nothing and is taken again on the new one.
		if stillAt(file, path) {
			return func() error { return unlockHandle(file, path, owned) }, nil
		}

		if err := file.Close(); err != nil {
			return nil, err
		}
	}

	return nil, errLocked
}

func stillAt(file *os.File, path string) bool {
	held, err := file.Stat()
	if err != nil {
		return false
	}

	named, err := os.Stat(path)
	if err != nil {
		return false
	}

	return os.SameFile(held, named)
}
