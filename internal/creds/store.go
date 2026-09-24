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

const (
	maxCredentialBytes = 1 << 20

	// StoreKeychain marks an entry whose key lives in the OS keychain rather than in the file.
	StoreKeychain = "keychain"
)

// NewStore builds a Store over the real filesystem. A test overrides only what it must.
func NewStore(opts ...StoreOption) Store {
	store := Store{
		open:       os.Open,
		stat:       os.Stat,
		lstat:      os.Lstat,
		mkdirAll:   os.MkdirAll,
		rename:     os.Rename,
		remove:     os.Remove,
		chmod:      os.Chmod,
		createTemp: os.CreateTemp,
		sync:       (*os.File).Sync,
		goos:       runtime.GOOS,
	}

	for _, opt := range opts {
		opt(&store)
	}

	return store
}

// Store reads and writes credential files. Build one with NewStore, since the zero value has no
// filesystem to call. Its zero value also assumes a unix filesystem.
type Store struct {
	open       func(string) (*os.File, error)
	stat       func(string) (fs.FileInfo, error)
	lstat      func(string) (fs.FileInfo, error)
	mkdirAll   func(string, os.FileMode) error
	rename     func(oldpath, newpath string) error
	remove     func(string) error
	chmod      func(string, os.FileMode) error
	createTemp func(dir, pattern string) (*os.File, error)
	sync       func(*os.File) error
	goos       string
}

// StoreOption customises a Store. It exists so a test can fail a filesystem call without finding a
// filesystem that fails it.
type StoreOption func(*Store)

// WithOpen replaces how a Store opens the credential file and its directory.
func WithOpen(open func(string) (*os.File, error)) StoreOption {
	return func(s *Store) {
		s.open = open
	}
}

// WithStat replaces how a Store reads the mode of a credential file it could not open.
func WithStat(stat func(string) (fs.FileInfo, error)) StoreOption {
	return func(s *Store) {
		s.stat = stat
	}
}

// WithLstat replaces how a Store inspects the credential path before removing it.
func WithLstat(lstat func(string) (fs.FileInfo, error)) StoreOption {
	return func(s *Store) {
		s.lstat = lstat
	}
}

// WithMkdirAll replaces how a Store creates the credential file's directory.
func WithMkdirAll(mkdirAll func(string, os.FileMode) error) StoreOption {
	return func(s *Store) {
		s.mkdirAll = mkdirAll
	}
}

// WithRename replaces how a Store moves the temporary file onto the credential path.
func WithRename(rename func(oldpath, newpath string) error) StoreOption {
	return func(s *Store) {
		s.rename = rename
	}
}

// WithRemove replaces how a Store deletes the credential file and a leftover temporary file.
func WithRemove(remove func(string) error) StoreOption {
	return func(s *Store) {
		s.remove = remove
	}
}

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

// Load reads the credential file, refusing one another user can reach, and found is false when it
// does not exist. Check err first, since a failed Close returns a File beside the error.
func (s Store) Load(path string) (file File, found bool, err error) {
	// A named pipe at this path blocks the open until something writes to it. Known and out of
	// scope: the fix needs O_NONBLOCK behind a unix build tag, and planting the pipe needs write
	// access to a 0700 directory.
	//
	// Opened before the mode is checked and read from the same handle, so the file cannot be
	// swapped between the check and the read. No test catches a regression here, since the swap
	// needs a second process between the two calls.
	handle, err := s.open(path)
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
			if info, statErr := s.stat(path); statErr == nil {
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
	// mode, and no chmod 600 would fix it.
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
			"onesie: credential file %s is not a JSON object with a 'providers' map of 'api_key' strings or keychain entries",
			path)
	}

	return file, true, nil
}

// Save writes the credential file atomically through a renamed temporary file, replacing a symlink
// at path. A non nil warning means the file was written but its mode could not be set.
func (s Store) Save(path string, file File) (*ModeWarning, error) {
	// A symlink at the directory is resolved, since creating it and the temporary file both walk
	// that path. Whoever can replace the directory already owns the config location.
	dir := filepath.Dir(path)
	if err := s.mkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("onesie: creating %s: %w", dir, err)
	}

	// Unreachable while File is plain strings, since json.Marshal cannot fail on those. A future
	// field of any other type makes it reachable, and json.UnsupportedValueError.Error embeds the
	// offending value verbatim, so such a field must keep the key out of this %w.
	data, err := json.Marshal(file)
	if err != nil {
		return nil, fmt.Errorf("onesie: encoding the credential file: %w", err)
	}

	// Created beside the target, because a rename across filesystems is not atomic and os.TempDir
	// may be on another one. os.CreateTemp opens with O_CREATE and O_EXCL at 0600, which keeps a
	// planted symlink from redirecting the key.
	temp, err := s.createTemp(dir, ".credentials-*")
	if err != nil {
		return nil, fmt.Errorf("onesie: creating a temporary file in %s: %w", dir, err)
	}

	// Named now so every later failure can remove it. A temporary file holding a key must not
	// outlive this function.
	name := temp.Name()

	if writeErr := s.writeAndClose(temp, data); writeErr != nil {
		return nil, errors.Join(writeErr, s.remove(name))
	}

	// os.CreateTemp already creates at 0600 before umask, so this only does work on a filesystem
	// that ignored that, which is the same filesystem that will refuse here. Section 16.2 says such
	// a filesystem gets a warning and a written file, not a failure.
	var warning *ModeWarning
	if chmodErr := s.chmod(name, 0o600); chmodErr != nil {
		warning = &ModeWarning{Path: path}
	}

	if renameErr := s.rename(name, path); renameErr != nil {
		return nil, errors.Join(fmt.Errorf("onesie: renaming %s to %s: %w", name, path, renameErr),
			s.remove(name))
	}

	// The rename only survives a power loss once the directory entry itself is on the disk.
	s.syncDir(dir)

	return warning, nil
}

func (s Store) writeAndClose(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return errors.Join(fmt.Errorf("onesie: writing %s: %w", file.Name(), err), file.Close())
	}

	// Ahead of the close and the rename, so a power loss cannot leave a renamed file whose contents
	// never reached the disk. That file would come back zero length and read as corrupt rather than
	// absent.
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

	handle, err := s.open(dir)
	if err != nil {
		return
	}

	// Not returned. The save has already succeeded, and a filesystem that refuses to sync a
	// directory, as some network and FUSE mounts do, would turn it into a failure the user cannot
	// act on. Unlike a skipped file sync, a skipped directory sync leaves the previous consistent
	// state.
	if err := errors.Join(handle.Sync(), handle.Close()); err != nil {
		return
	}
}

// ModeWarning means the file was written but its mode could not be set, as on a filesystem without
// unix permission bits. The key is readable by anyone who can reach the path.
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
	// never asked to touch. os.Lstat, since a symlink here is the link to remove.
	//
	// Only a directory is refused, so Clear is deliberately laxer than Load. auth clear is asked to
	// leave nothing at the path, even something onesie itself will not read.
	if info, statErr := s.lstat(path); statErr == nil && info.IsDir() {
		return fmt.Errorf("onesie: credential file %s is not a regular file", path)
	}

	if err := s.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("onesie: removing %s: %w", path, err)
	}

	return nil
}

// File is the contents of a credential file, one entry per provider name.
type File struct {
	Providers map[string]Entry `json:"providers"`
}

// Entry is one provider's key, or a pointer to it in the keychain named by Account when Store is
// StoreKeychain. BaseURL is empty when the entry carries none.
type Entry struct {
	APIKey  string `json:"api_key,omitempty"`
	Store   string `json:"store,omitempty"`
	Account string `json:"account,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
}

func (f File) complete() bool {
	if len(f.Providers) == 0 {
		return false
	}

	for _, entry := range f.Providers {
		inFile := entry.APIKey != "" && entry.Store == "" && entry.Account == ""
		inKeychain := entry.APIKey == "" && entry.Store == StoreKeychain

		if !inFile && !inKeychain {
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
		"onesie: credential file %s is accessible by others, mode %o. Run chmod 600 on it",
		e.Path, e.Mode)
}
