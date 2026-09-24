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

func newLocker() *locker {
	return &locker{openFile: os.OpenFile, stat: os.Stat}
}

type locker struct {
	openFile func(name string, flag int, perm os.FileMode) (*os.File, error)
	stat     func(name string) (os.FileInfo, error)
}

func (l *locker) lockAnswers(answers string) (release func() error, err error) {
	// An OS lock rather than a file created with O_EXCL, since the kernel lets go of an OS lock when
	// the process dies, and a second Ctrl-C or a kill would otherwise leave a lock that refuses
	// every later resume.
	release, err = l.lockAt(answers+lockSuffix, os.O_RDONLY|os.O_CREATE, true)
	if !errors.Is(err, fs.ErrPermission) {
		return release, err
	}

	// A directory that refuses the lock file refuses the compaction rename too, so the answers file
	// itself is locked instead. With no answers file there is nothing another run could lose.
	release, err = l.lockAt(answers, os.O_RDONLY, false)
	if errors.Is(err, fs.ErrNotExist) {
		return func() error { return nil }, nil
	}

	return release, err
}

func (l *locker) lockAt(path string, flag int, owned bool) (func() error, error) {
	for range lockAttempts {
		file, err := l.openFile(path, flag, 0o600)
		if err != nil {
			return nil, err
		}

		if lockErr := lockHandle(file); lockErr != nil {
			return nil, errors.Join(lockErr, file.Close())
		}

		// The run that held the lock removes the file as it lets go, so a lock taken on a file
		// already removed guards nothing and is taken again on the new one.
		held, err := l.stillAt(file, path)
		if err != nil {
			return nil, errors.Join(err, file.Close())
		}

		if held {
			return func() error { return unlockHandle(file, path, owned) }, nil
		}

		if closeErr := file.Close(); closeErr != nil {
			return nil, closeErr
		}
	}

	return nil, errLocked
}

func (l *locker) stillAt(file *os.File, path string) (bool, error) {
	held, err := file.Stat()
	if err != nil {
		return false, err
	}

	named, err := l.stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return os.SameFile(held, named), nil
}
