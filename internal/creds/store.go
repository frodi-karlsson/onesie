package creds

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
)

const maxCredentialBytes = 1 << 20

// NewStore builds a Store over the real filesystem. A test overrides only what it must.
func NewStore(opts ...StoreOption) Store {
	store := Store{chmod: os.Chmod}

	for _, opt := range opts {
		opt(&store)
	}

	return store
}

// Store reads and writes credential files. Build one with NewStore, since the zero value has no
// chmod to call.
type Store struct {
	chmod func(string, os.FileMode) error
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

// Load reads the credential file. The second result is false when the file does not exist, which is
// not an error. A file any other user can reach is refused rather than read.
func (s Store) Load(path string) (file File, found bool, err error) {
	// Opened before the mode is checked, and read from the same handle, so the file cannot be
	// replaced between the check and the read. A stat by path and a later read by path are two
	// different files on a filesystem someone else can write to, which is the case this check
	// exists for.
	handle, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, false, nil
	}

	if err != nil {
		// A file whose owner cannot read it fails the open before the mode is ever looked at, so
		// the mode is named here too. Otherwise the same file refuses with a bare permission denied
		// for an ordinary user and with the mode for root.
		if errors.Is(err, fs.ErrPermission) {
			if info, statErr := os.Lstat(path); statErr == nil {
				if modeErr := checkMode(path, info.Mode()); modeErr != nil {
					return File{}, false, modeErr
				}
			}
		}

		return File{}, false, fmt.Errorf("jev: reading %s: %w", path, err)
	}

	// Joined into the named return rather than discarded. An empty branch would trip staticcheck
	// and a blank assignment would trip errcheck.
	defer func() {
		err = errors.Join(err, handle.Close())
	}()

	info, err := handle.Stat()
	if err != nil {
		return File{}, false, fmt.Errorf("jev: reading %s: %w", path, err)
	}

	// Ahead of the mode check, since a directory or a device node is not a credential file at a bad
	// mode. Checking the mode first would call a directory a credential file, drop the type bit from
	// the message, and tell the reader to chmod 600 something no chmod will fix.
	if !info.Mode().IsRegular() {
		return File{}, false, fmt.Errorf("jev: credential file %s is not a regular file", path)
	}

	if modeErr := checkMode(path, info.Mode()); modeErr != nil {
		return File{}, false, modeErr
	}

	// One byte past the cap, so a file at exactly the cap still loads and the first byte beyond it
	// is refused. A plain limit would truncate instead, and a truncated file that still parsed would
	// hand back half a credential as if it were whole.
	data, err := io.ReadAll(io.LimitReader(handle, maxCredentialBytes+1))
	if err != nil {
		return File{}, false, fmt.Errorf("jev: reading %s: %w", path, err)
	}

	if len(data) > maxCredentialBytes {
		return File{}, false, fmt.Errorf(
			"jev: credential file %s is larger than %d bytes", path, maxCredentialBytes)
	}

	if decodeErr := json.Unmarshal(data, &file); decodeErr != nil || file.APIKey == "" {
		// An empty or absent api_key is the same failure as a malformed file. Returning it as
		// found would make auth status report a source for a file holding nothing, and would send
		// an empty key to the client.
		return File{}, false, fmt.Errorf(
			"jev: credential file %s is not a JSON object with an 'api_key' string", path)
	}

	return file, true, nil
}

// File is the contents of a credential file. BaseURL is empty when the file carries none.
type File struct {
	APIKey  string `json:"api_key"`
	BaseURL string `json:"base_url,omitempty"`
}

func checkMode(path string, mode os.FileMode) error {
	// Windows does not carry unix permission bits, so the check would reject every file or accept
	// every file depending on how the bits happen to be reported. Section 16.2 scopes the rule to
	// platforms that support modes.
	if runtime.GOOS == "windows" {
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
		"jev: credential file %s is accessible by others, mode %o. Run chmod 600 on it or jev auth set to rewrite it",
		e.Path, e.Mode)
}
