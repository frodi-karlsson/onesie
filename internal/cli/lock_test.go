package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLocker_LockAnswers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		goos       string
		existing   bool
		readOnly   bool
		lockFile   bool
		refuseLock bool
		wantErr    error
		wantLocked bool
	}{
		{
			name:       "should refuse a second lock while the first is held",
			wantLocked: true,
		},
		{
			name:       "should lock the answers file itself when the directory refuses the lock file",
			existing:   true,
			readOnly:   true,
			wantLocked: true,
		},
		{
			name:     "should lock nothing when the directory refuses the lock file and there is no answers file",
			readOnly: true,
		},
		{
			name:       "should report locked on windows when a lock file there refuses to open",
			goos:       "windows",
			existing:   true,
			lockFile:   true,
			refuseLock: true,
			wantErr:    errLocked,
		},
		{
			name:       "should not lock the answers file on windows when the directory refuses the lock file",
			goos:       "windows",
			existing:   true,
			refuseLock: true,
			wantErr:    fs.ErrPermission,
		},
		{
			name:       "should not lock the answers file under fcntl when the directory refuses the lock file",
			goos:       "illumos",
			existing:   true,
			refuseLock: true,
			wantErr:    fs.ErrPermission,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.readOnly && runtime.GOOS == "windows" {
				t.Skip("a read only directory on windows still lets a file be created in it")
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "answers.jsonl")

			if tc.existing {
				if err := os.WriteFile(path, []byte("answer\n"), 0o600); err != nil {
					t.Fatalf("writing the answers file: %v", err)
				}
			}

			if tc.lockFile {
				if err := os.WriteFile(path+lockSuffix, nil, 0o600); err != nil {
					t.Fatalf("writing the lock file: %v", err)
				}
			}

			if tc.readOnly {
				lockDir(t, dir)
			}

			goos := tc.goos
			if goos == "" {
				goos = runtime.GOOS
			}

			var openedAnswers bool

			l := newLocker(goos)
			l.openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
				if name == path {
					openedAnswers = true
				}

				if tc.refuseLock && name == path+lockSuffix {
					return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
				}

				return os.OpenFile(name, flag, perm)
			}

			release, err := l.lockAnswers(path)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("lock error = %v, want %v", err, tc.wantErr)
				}

				if openedAnswers {
					t.Errorf("opened the answers file to lock it, want it left alone")
				}

				return
			}

			if err != nil {
				t.Fatalf("first lock: %v", err)
			}

			_, secondErr := l.lockAnswers(path)
			if errors.Is(secondErr, errLocked) != tc.wantLocked {
				t.Fatalf("second lock error = %v, want locked %v", secondErr, tc.wantLocked)
			}

			if releaseErr := release(); releaseErr != nil {
				t.Fatalf("release: %v", releaseErr)
			}

			again, err := l.lockAnswers(path)
			if err != nil {
				t.Fatalf("lock after the release: %v", err)
			}

			if err := again(); err != nil {
				t.Fatalf("second release: %v", err)
			}

			if _, statErr := os.Stat(path + lockSuffix); !os.IsNotExist(statErr) {
				t.Errorf("lock file left behind: %v", statErr)
			}
		})
	}

	t.Run("should remove the lock file it created through the injected remove as it lets go", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("windows ignores a refused removal, since another run may hold the file open")
		}

		path := filepath.Join(t.TempDir(), "answers.jsonl")
		refused := errors.New("remove refused")

		var removed []string

		l := newLocker(runtime.GOOS)
		l.remove = func(name string) error {
			removed = append(removed, name)

			return refused
		}

		release, err := l.lockAnswers(path)
		if err != nil {
			t.Fatalf("lock: %v", err)
		}

		if err := release(); !errors.Is(err, refused) {
			t.Errorf("release error = %v, want the injected removal's error", err)
		}

		if len(removed) != 1 || removed[0] != path+lockSuffix {
			t.Errorf("removed = %v, want only the lock file", removed)
		}
	})
}

func TestLocker_LockAt(t *testing.T) {
	t.Parallel()

	failedStat := errors.New("input/output error")

	other, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatalf("looking at another file: %v", err)
	}

	tests := []struct {
		name      string
		stat      func(call int, name string) (os.FileInfo, error)
		wantErr   error
		wantOpens int
	}{
		{
			name:      "should take the lock on the first file it opens",
			wantOpens: 1,
		},
		{
			name: "should take the lock again when the file it locked was removed",
			stat: func(call int, name string) (os.FileInfo, error) {
				if call == 1 {
					return nil, fs.ErrNotExist
				}

				return os.Stat(name)
			},
			wantOpens: 2,
		},
		{
			name: "should take the lock again when the path names a different file",
			stat: func(call int, name string) (os.FileInfo, error) {
				if call == 1 {
					return other, nil
				}

				return os.Stat(name)
			},
			wantOpens: 2,
		},
		{
			name:      "should report locked when every file it locked was removed",
			stat:      func(int, string) (os.FileInfo, error) { return nil, fs.ErrNotExist },
			wantErr:   errLocked,
			wantOpens: lockAttempts,
		},
		{
			name:      "should return a failure to look at the path as it is",
			stat:      func(int, string) (os.FileInfo, error) { return nil, failedStat },
			wantErr:   failedStat,
			wantOpens: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "answers.jsonl"+lockSuffix)

			var opens, stats int

			l := newLocker(runtime.GOOS)
			l.openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
				opens++

				return os.OpenFile(name, flag, perm)
			}

			if tc.stat != nil {
				l.stat = func(name string) (os.FileInfo, error) {
					stats++

					return tc.stat(stats, name)
				}
			}

			release, err := l.lockAt(path, os.O_RDWR|os.O_CREATE, true)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil) != (err == nil) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}

			if errors.Is(tc.wantErr, failedStat) && errors.Is(err, errLocked) {
				t.Errorf("error = %v, want it not reported as locked", err)
			}

			if opens != tc.wantOpens {
				t.Errorf("opens = %d, want %d", opens, tc.wantOpens)
			}

			if err != nil {
				return
			}

			if _, statErr := os.Stat(path); statErr != nil {
				t.Errorf("lock file while held: %v", statErr)
			}

			if releaseErr := release(); releaseErr != nil {
				t.Fatalf("release: %v", releaseErr)
			}

			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				t.Errorf("lock file left behind: %v", statErr)
			}
		})
	}
}
