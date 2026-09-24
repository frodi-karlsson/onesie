package creds

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

const maxCredentialBytes = 1 << 20

// NewStore builds a Store over the real filesystem. A test overrides only what it must.
func NewStore(opts ...StoreOption) Store {
	store := Store{chmod: os.Chmod, createTemp: os.CreateTemp, sync: (*os.File).Sync, goos: runtime.GOOS}

	for _, opt := range opts {
		opt(&store)
	}

	return store
}

// Store reads and writes credential files. Build one with NewStore, since the zero value has no
// chmod to call. Its zero value also assumes a unix filesystem.
type Store struct {
	chmod      func(string, os.FileMode) error
	createTemp func(dir, pattern string) (*os.File, error)
	sync       func(*os.File) error
	goos       string
}

// StoreOption customises a Store. It exists so a test can fail a chmod without finding a
// filesystem that cannot set modes.
type StoreOption func(*Store)

// WithChmod replaces how a Store sets a file's mode.
func WithChmod(chmod func(string, os.FileMode) error) StoreOption {
	return func(s *Store) {
		s.chmod = chmod
	}
}

// WithCreateTemp replaces how a Store opens the temporary file it writes before the rename.
func WithCreateTemp(createTemp func(dir, pattern string) (*os.File, error)) StoreOption {
	return func(s *Store) {
		s.createTemp = createTemp
	}
}

// WithSync replaces how a Store flushes the temporary file before the rename.
func WithSync(sync func(*os.File) error) StoreOption {
	return func(s *Store) {
		s.sync = sync
	}
}

// WithGOOS replaces the operating system a Store assumes.
func WithGOOS(goos string) StoreOption {
	return func(s *Store) {
		s.goos = goos
	}
}

// Load reads the credential file. The second result is false when the file does not exist, which is
// not an error. A file any other user can reach is refused rather than read. A caller has to check
// the error before found, since a failing Close on an otherwise good read returns a populated File
// beside a non nil error, and a key from a read that reported failure must not be used.
func (s Store) Load(path string) (file File, found bool, err error) {
	// A named pipe at this path blocks the open until something writes to it. Known, and out of
	// scope: the fix needs syscall.O_NONBLOCK behind a unix build tag, and planting the pipe needs
	// write access to a 0700 directory, which buys an attacker worse than a hang.
	//
	// Opened before the mode is checked, and read from the same handle, so the file cannot be
	// replaced between the check and the read. A stat by path and a later read by path are two
	// different files on a filesystem someone else can write to, which is the case this check
	// exists for. No test catches a regression here, since the swap needs a second process between
	// the two calls. The reasoning above is the only guard.
	handle, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, false, nil
	}

	if err != nil {
		// A file whose owner cannot read it fails the open before the mode is ever looked at, so
		// the mode is named here too. Otherwise the same file refuses with a bare permission denied
		// for an ordinary user and with the mode for root.
		if errors.Is(err, fs.ErrPermission) {
			// os.Stat rather than os.Lstat. Open follows a symlink, so the refusal came from the
			// target, and Lstat would name the link's own mode instead. Measured on a link to an
			// 0o060 file: Lstat reports 755, Stat reports 60.
			if info, statErr := os.Stat(path); statErr == nil {
				if modeErr := s.checkMode(path, info.Mode()); modeErr != nil {
					return File{}, false, modeErr
				}
			}
		}

		return File{}, false, fmt.Errorf("onesie: reading %s: %w", path, err)
	}

	// Joined into the named return rather than discarded. An empty branch would trip staticcheck
	// and a blank assignment would trip errcheck.
	defer func() {
		err = errors.Join(err, handle.Close())
	}()

	info, err := handle.Stat()
	if err != nil {
		return File{}, false, fmt.Errorf("onesie: reading %s: %w", path, err)
	}

	// Ahead of the mode check, since a directory or a device node is not a credential file at a bad
	// mode. Checking the mode first would call a directory a credential file, drop the type bit from
	// the message, and tell the reader to chmod 600 something no chmod will fix.
	if !info.Mode().IsRegular() {
		return File{}, false, fmt.Errorf("onesie: credential file %s is not a regular file", path)
	}

	if modeErr := s.checkMode(path, info.Mode()); modeErr != nil {
		return File{}, false, modeErr
	}

	// One byte past the cap, so a file at exactly the cap still loads and the first byte beyond it
	// is refused. A plain limit would truncate instead, and a truncated file that still parsed would
	// hand back half a credential as if it were whole.
	data, err := io.ReadAll(io.LimitReader(handle, maxCredentialBytes+1))
	if err != nil {
		return File{}, false, fmt.Errorf("onesie: reading %s: %w", path, err)
	}

	if len(data) > maxCredentialBytes {
		return File{}, false, fmt.Errorf(
			"onesie: credential file %s is larger than %d bytes", path, maxCredentialBytes)
	}

	if decodeErr := json.Unmarshal(data, &file); decodeErr != nil || !file.complete() {
		// An empty or absent api_key is the same failure as a malformed file. Returning it as
		// found would make auth status report a source for a file holding nothing, and would send
		// an empty key to the client.
		return File{}, false, fmt.Errorf(
			"onesie: credential file %s is not a JSON object with a 'providers' map of 'api_key' strings",
			path)
	}

	return file, true, nil
}

// Save writes the credential file atomically, creating its directory. The file is written to a
// temporary name in the same directory and renamed, so a reader never sees a half written key. A
// non nil first result means the file was written but its mode could not be set, which is a warning
// for the caller to print rather than a failure. A symlink at the credential path is replaced rather
// than followed, but a symlink at its directory is resolved, since creating the directory and the
// temporary file inside it both have to walk that path. Whoever can replace the directory already
// owns the config location.
func (s Store) Save(path string, file File) (*ModeWarning, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("onesie: creating %s: %w", dir, err)
	}

	// Unreachable while File is plain strings, since json.Marshal cannot fail on those. A future
	// field of any other type makes it reachable, and json.UnsupportedValueError.Error embeds the
	// offending value verbatim, so such a field must keep the key out of this %w.
	data, err := json.Marshal(file)
	if err != nil {
		return nil, fmt.Errorf("onesie: encoding the credential file: %w", err)
	}

	// Created in the same directory as the target, because a rename across filesystems is not
	// atomic and os.TempDir may be on another one. os.CreateTemp also opens with O_CREATE and
	// O_EXCL at 0600, which is what keeps a planted symlink in the config directory from
	// redirecting the key. Never write to path itself.
	temp, err := s.createTemp(dir, ".credentials-*")
	if err != nil {
		return nil, fmt.Errorf("onesie: creating a temporary file in %s: %w", dir, err)
	}

	// Named now so every later failure can remove it. A temporary file holding a key must not
	// outlive this function.
	name := temp.Name()

	if writeErr := s.writeAndClose(temp, data); writeErr != nil {
		return nil, errors.Join(writeErr, os.Remove(name))
	}

	// os.CreateTemp already creates at 0600 before umask, so this only does work on a filesystem
	// that ignored that, which is the same filesystem that will refuse here. Section 16.2 says such
	// a filesystem gets a warning and a written file, not a failure.
	var warning *ModeWarning
	if chmodErr := s.chmod(name, 0o600); chmodErr != nil {
		warning = &ModeWarning{Path: path}
	}

	if renameErr := os.Rename(name, path); renameErr != nil {
		return nil, errors.Join(fmt.Errorf("onesie: renaming %s to %s: %w", name, path, renameErr),
			os.Remove(name))
	}

	// The rename only survives a power loss once the directory entry itself is on the disk.
	s.syncDir(dir)

	return warning, nil
}

func (s Store) writeAndClose(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return errors.Join(fmt.Errorf("onesie: writing %s: %w", file.Name(), err), file.Close())
	}

	// Ahead of the close and of the rename, so a power loss cannot leave a renamed file whose
	// contents never reached the disk. Without it the credential file can come back zero length on
	// a filesystem that delays data behind metadata, which reads as a corrupt file rather than an
	// absent one.
	if err := s.sync(file); err != nil {
		return errors.Join(fmt.Errorf("onesie: flushing %s: %w", file.Name(), err), file.Close())
	}

	if err := file.Close(); err != nil {
		return fmt.Errorf("onesie: closing %s: %w", file.Name(), err)
	}

	return nil
}

func (s Store) syncDir(dir string) {
	// Windows has no durable directory flush. FlushFileBuffers refuses a directory handle, so this
	// would fail on every save there rather than only where the filesystem cannot do it.
	if s.goos == "windows" {
		return
	}

	handle, err := os.Open(dir)
	if err != nil {
		return
	}

	// Returned by neither this function nor Save. The file is written and renamed by the time this
	// runs, so the save has succeeded, and a filesystem that refuses to sync a directory handle,
	// as some network and FUSE mounts do, would otherwise turn a completed save into a failure the
	// user cannot act on. The asymmetry with the file sync is deliberate: skipping that one leaves
	// a present but zero length credential file, which reads as corruption, while skipping this
	// one leaves the previous consistent state.
	if err := errors.Join(handle.Sync(), handle.Close()); err != nil {
		return
	}
}

// ModeWarning means the file was written but its mode could not be set, which happens on a
// filesystem that does not carry unix permission bits. The key is on disk and readable by anyone
// who can reach the path.
type ModeWarning struct {
	Path string
}

// Error names the path so the caller can tell the user where the unprotected file is.
func (e *ModeWarning) Error() string {
	return fmt.Sprintf(
		"warning: could not set mode 600 on %s. The key is not protected by the filesystem", e.Path)
}

// Clear deletes the credential file. An absent file is not an error, since auth clear is defined to
// exit 0 either way.
func (s Store) Clear(path string) error {
	// os.Remove calls rmdir on a directory, so without this Clear would delete a directory it was
	// never asked to touch. os.Lstat rather than os.Stat, since a symlink at this path is the link
	// to remove and not the thing it points at.
	//
	// Only a directory is refused, so Clear is deliberately laxer than Load. A symlink, a FIFO, a
	// socket or a device node at the credential path is removed rather than reported, since auth
	// clear is asked to leave nothing there and the alternative is a user who cannot clear a path
	// onesie itself will not read.
	if info, statErr := os.Lstat(path); statErr == nil && info.IsDir() {
		return fmt.Errorf("onesie: credential file %s is not a regular file", path)
	}

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("onesie: removing %s: %w", path, err)
	}

	return nil
}

// File is the contents of a credential file, one entry per provider name.
type File struct {
	Providers map[string]Entry `json:"providers"`
}

// Entry is one provider's key. BaseURL is empty when the entry carries none.
type Entry struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url,omitempty"`
}

func (f File) complete() bool {
	if len(f.Providers) == 0 {
		return false
	}

	for _, entry := range f.Providers {
		if entry.APIKey == "" {
			return false
		}
	}

	return true
}

func (s Store) checkMode(path string, mode os.FileMode) error {
	// Windows does not carry unix permission bits, so the check would reject every file or accept
	// every file depending on how the bits happen to be reported. Section 16.2 scopes the rule to
	// platforms that support modes.
	if s.goos == "windows" {
		return nil
	}

	// 0o077 rather than 0o044. A group writable credential file is as bad as a readable one, since
	// whoever can replace it can substitute a key whose traffic they see. Section 16.2 records the
	// reasoning and the message says accessible rather than readable because of it.
	if mode.Perm()&0o077 == 0 {
		return nil
	}

	return &ReadableError{Path: path, Mode: mode.Perm()}
}

// ReadableError means the credential file's mode lets someone other than its owner reach it.
type ReadableError struct {
	Path string
	Mode os.FileMode
}

// Error names the path and the mode, never the contents.
func (e *ReadableError) Error() string {
	return fmt.Sprintf(
		"onesie: credential file %s is accessible by others, mode %o. Run chmod 600 on it or onesie auth set to rewrite it",
		e.Path, e.Mode)
}
