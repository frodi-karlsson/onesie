package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/patherr"
)

const (
	entryVersion = 1
	tagName      = "CACHEDIR.TAG"
	tempPrefix   = ".onesie-tmp-"
	pinnedSuffix = ".json"
	aliasSuffix  = ".alias.json"
	// An hour is far longer than any write takes, so a walk never removes the file of a write
	// another run has in progress. The cost is that a crashed write leaves its file that long.
	tempMaxAge = time.Hour

	tagContent = "Signature: 8a477f597d28d172789f06886806bc55\n" +
		"# This file is a cache directory tag created by onesie.\n" +
		"# For information about cache directory tags, see https://bford.info/cachedir/\n"
)

const (
	taggedDir dirState = iota
	untaggedDir
	missingDir
)

// NoAliasTTL as Options.AliasTTL means an entry for a model alias is neither read nor stored.
const NoAliasTTL time.Duration = -1

// Open opens the cache in dir, creating it with mode 700 and a CACHEDIR.TAG when it is missing. It
// refuses a directory others can reach, except on windows, and an untagged one holding foreign files.
func Open(dir string, opts Options) (*Store, error) {
	opts = opts.withDefaults()

	state, err := inspect(opts.FS, dir, opts.GOOS)
	if err != nil {
		return nil, err
	}

	switch state {
	case missingDir:
		if err := opts.FS.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating the cache dir %s: %w", dir, err)
		}

		if err := writeTag(opts.FS, dir); err != nil {
			return nil, err
		}
	case untaggedDir:
		if err := writeTag(opts.FS, dir); err != nil {
			return nil, err
		}
	}

	return &Store{dir: dir, opts: opts}, nil
}

// Check reports why Open would refuse dir, deciding as Open does but creating and tagging nothing.
func Check(dir string, opts Options) error {
	opts = opts.withDefaults()

	_, err := inspect(opts.FS, dir, opts.GOOS)

	return err
}

func inspect(fsys FS, dir, goos string) (dirState, error) {
	info, err := fsys.Stat(dir)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return missingDir, nil
	case err != nil:
		return 0, fmt.Errorf("reading the cache dir %s: %w", dir, patherr.Inner(err))
	case !info.IsDir():
		return 0, fmt.Errorf("the cache dir %s is not a directory", dir)
	case goos != "windows" && info.Mode().Perm()&0o077 != 0:
		return 0, &ModeError{Dir: dir, Mode: info.Mode().Perm()}
	}

	return tagState(fsys, dir)
}

type dirState int

func tagState(fsys FS, dir string) (dirState, error) {
	// A directory holding nothing but what onesie writes gets its tag, so clear can empty it later.
	// One holding anything else may not be onesie's. A tag there would hide the foreign files from
	// backup tools, and storing without one would leave entries clear refuses to remove.
	if _, err := fsys.Stat(filepath.Join(dir, tagName)); !errors.Is(err, fs.ErrNotExist) {
		return taggedDir, nil
	}

	names, err := fsys.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("reading the cache dir %s: %w", dir, patherr.Inner(err))
	}

	for _, named := range names {
		ours := (named.IsDir() && isFanout(named.Name())) ||
			(named.Type().IsRegular() && (strings.HasPrefix(named.Name(), tempPrefix) || systemFile(named.Name())))
		if !ours {
			return 0, &ForeignError{Dir: dir, File: named.Name()}
		}
	}

	return untaggedDir, nil
}

func systemFile(name string) bool {
	// What macOS Finder and Windows Explorer leave in a folder that is only looked at.
	switch name {
	case ".DS_Store", "Thumbs.db", "desktop.ini":
		return true
	}

	return strings.HasPrefix(name, "._")
}

func writeTag(fsys FS, dir string) error {
	if err := fsys.WriteFile(filepath.Join(dir, tagName), []byte(tagContent), 0o600); err != nil {
		return fmt.Errorf("writing %s in the cache dir %s: %w", tagName, dir, err)
	}

	return nil
}

func (o Options) withDefaults() Options {
	if o.Now == nil {
		o.Now = time.Now
	}

	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}

	if o.MaxBytes <= 0 {
		o.MaxBytes = limits.MaxCacheBytes
	}

	if o.AliasTTL == 0 {
		o.AliasTTL = limits.DefaultCacheTTL
	}

	if o.FS == nil {
		o.FS = osFS{}
	}

	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}

	if o.Random == nil {
		o.Random = rand.Float64
	}

	return o
}

// Options adjusts a Store. Every zero field takes its default.
type Options struct {
	// Now reads the clock. time.Now by default.
	Now func() time.Time
	// GOOS names the operating system, which decides whether modes are checked. runtime.GOOS by default.
	GOOS string
	// MaxBytes is about the most the cache holds. limits.MaxCacheBytes by default.
	MaxBytes int64
	// AliasTTL is how long an entry for a model alias lives. limits.DefaultCacheTTL by default, and
	// NoAliasTTL turns alias entries off.
	AliasTTL time.Duration
	// FS is the filesystem the store works through. The real one by default.
	FS FS
	// Sleep waits between retries of a file another run holds on windows. time.Sleep by default.
	Sleep func(time.Duration)
	// Random spreads those retries, and returns a value from 0 up to but excluding 1. rand.Float64 by
	// default.
	Random func() float64
}

// FS is every filesystem call the store makes, so a test can make one fail.
type FS interface {
	MkdirAll(path string, perm os.FileMode) error
	Stat(path string) (os.FileInfo, error)
	CreateTemp(dir, pattern string) (TempFile, error)
	Rename(oldpath, newpath string) error
	Remove(path string) error
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	ReadDir(path string) ([]os.DirEntry, error)
	Chtimes(path string, atime, mtime time.Time) error
}

// TempFile is the part of *os.File a store writes an entry through.
type TempFile interface {
	Write(p []byte) (int, error)
	Sync() error
	Close() error
	Name() string
}

// ModeError means the cache directory's mode lets someone other than its owner reach it.
type ModeError struct {
	Dir  string
	Mode os.FileMode
}

// Error names the directory, its mode and the fix.
func (e *ModeError) Error() string {
	return fmt.Sprintf("the cache dir %s has mode %o, so others can read cached answers. Run chmod 700 %s",
		e.Dir, e.Mode, e.Dir)
}

// ForeignError means the cache directory holds files onesie did not write and no CACHEDIR.TAG, so it
// may not be onesie's to fill. File is the first such file found.
type ForeignError struct {
	Dir  string
	File string
}

// Error names the directory, the file found in it and the fix.
func (e *ForeignError) Error() string {
	return fmt.Sprintf("the cache dir %s holds '%s', which onesie did not write, and no %s, "+
		"so onesie will not store answers there. Point ONESIE_CACHE_DIR at a new or empty directory",
		e.Dir, e.File, tagName)
}

// Key names one cached response, a sha256 of everything that decides it.
type Key [sha256.Size]byte

// Store is a cache directory, safe for concurrent use and shared with other runs.
type Store struct {
	dir  string
	opts Options

	mu      sync.Mutex
	counted bool
	total   int64
}

// Dir is the directory the store keeps its entries in.
func (s *Store) Dir() string {
	return s.dir
}

// Get returns the value stored under key for model, or a miss when there is none, it expired, or it
// cannot be read as an entry. A pinned entry never expires, and a hit counts as a use for eviction.
func (s *Store) Get(key Key, model string, pinned bool) ([]byte, bool, error) {
	if !pinned && s.opts.AliasTTL < 0 {
		return nil, false, nil
	}

	path := s.entryPath(key, pinned)

	var data []byte

	err := s.retry(func() error {
		var readErr error
		data, readErr = s.opts.FS.ReadFile(path)

		return readErr
	}, contended)
	if err != nil {
		// A file another run still holds once the retries run out is only a miss.
		if s.absent(path, err) || s.stillHeld(err, contended) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("reading %s: %w", path, patherr.Inner(err))
	}

	stored, ok := readEntry(data, model)
	if !ok {
		return nil, false, nil
	}

	now := s.opts.Now()
	age := now.Sub(stored.Stored)

	// An expired entry is only a miss, and the Put after it overwrites it. Another run with a longer
	// lifetime may still read it. A stored time in the future comes from a clock that cannot be
	// trusted, so it is a miss too.
	if age < 0 || (!pinned && age >= s.opts.AliasTTL) {
		return nil, false, nil
	}

	s.markUsed(path, now)

	return stored.Value, true, nil
}

func (s *Store) markUsed(path string, now time.Time) {
	// A failed mark only makes the entry look older to eviction, so the hit stands and the error goes
	// nowhere.
	if err := s.opts.FS.Chtimes(path, now, now); err != nil {
		return
	}
}

func readEntry(data []byte, model string) (entry, bool) {
	stored, err := decodeEntry(data)

	return stored, err == nil && stored.Model == model
}

func (s *Store) absent(path string, readErr error) bool {
	if errors.Is(readErr, fs.ErrNotExist) {
		return true
	}

	// A directory in the entry's place is a miss, and the Put after it reports the failure.
	info, err := s.opts.FS.Stat(path)

	return errors.Is(err, fs.ErrNotExist) || (err == nil && info.IsDir())
}

// Put stores value, which must be JSON, under key. It writes a temporary file and renames it over the
// entry, so a reader sees the old entry or the new one and never half of one.
func (s *Store) Put(key Key, model string, pinned bool, value []byte) error {
	if !pinned && s.opts.AliasTTL < 0 {
		return nil
	}

	now := s.opts.Now()

	data, err := encodeEntry(entry{Model: model, Stored: now, Value: value})
	if err != nil {
		return err
	}

	path := s.entryPath(key, pinned)
	if err := s.opts.FS.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	var replaced int64
	if info, err := s.opts.FS.Stat(path); err == nil && info.Mode().IsRegular() {
		replaced = info.Size()
	}

	stored, writeErr := s.write(path, data, now)
	if writeErr != nil || !stored {
		return writeErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.counted {
		// The walk sees the entry just written, so its size is not added again.
		total, err := s.walk()
		if err != nil {
			return err
		}

		s.total, s.counted = total, true
	} else {
		s.total += int64(len(data)) - replaced
	}

	if s.total > s.opts.MaxBytes {
		return s.evict()
	}

	return nil
}

func (s *Store) write(path string, data []byte, now time.Time) (bool, error) {
	temp, err := s.opts.FS.CreateTemp(filepath.Dir(path), tempPrefix+"*")
	if err != nil {
		return false, fmt.Errorf("creating a temporary file beside %s: %w", path, err)
	}

	name := temp.Name()

	written := writeAll(temp, data)
	if written == nil {
		written = s.opts.FS.Chtimes(name, now, now)
	}

	if written == nil {
		written = s.retry(func() error { return s.opts.FS.Rename(name, path) }, contendedOrMissing)
	}

	if written == nil {
		return true, nil
	}

	lost := s.lostRename(path, written)

	if err := s.remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, errors.Join(fmt.Errorf("writing %s: %w", path, written), err)
	}

	// Another writer of the same key held the entry through every retry, so its value stands and this
	// one is dropped rather than turning the cache off.
	if lost {
		return false, nil
	}

	return false, fmt.Errorf("writing %s: %w", path, written)
}

func (s *Store) lostRename(path string, renameErr error) bool {
	if !s.stillHeld(renameErr, contendedOrMissing) {
		return false
	}

	// Windows refuses a rename over a directory with the same error, and that one never clears.
	info, err := s.opts.FS.Stat(path)

	return err != nil || !info.IsDir()
}

func writeAll(temp TempFile, data []byte) error {
	if _, err := temp.Write(data); err != nil {
		return errors.Join(err, temp.Close())
	}

	if err := temp.Sync(); err != nil {
		return errors.Join(err, temp.Close())
	}

	return temp.Close()
}

func (s *Store) evict() error {
	files, err := scan(s.opts.FS, s.dir)
	if err != nil {
		return err
	}

	entries := files[:0]
	s.total = 0

	for _, f := range files {
		if f.kind != tempFile {
			entries = append(entries, f)
			s.total += f.size
		}
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].mtime.Before(entries[j].mtime) })

	// Down to 90 percent of the cap, so the next few writes do not each evict again.
	floor := s.opts.MaxBytes / 10 * 9

	for _, f := range entries {
		if s.total <= floor {
			break
		}

		if err := s.remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("evicting %s: %w", f.path, patherr.Inner(err))
		}

		s.total -= f.size
	}

	return nil
}

func (s *Store) walk() (int64, error) {
	files, err := scan(s.opts.FS, s.dir)
	if err != nil {
		return 0, err
	}

	now := s.opts.Now()

	// A run's lifetime decides what it reads, not what it deletes, so a shorter one never removes an
	// entry other runs still read. Under NoAliasTTL the run reads no alias entry and deletes none.
	expiry := max(s.opts.AliasTTL, limits.DefaultCacheTTL)

	var total int64

	for _, f := range files {
		stale := (f.kind == tempFile && now.Sub(f.mtime) > tempMaxAge) ||
			(f.kind == aliasEntry && s.opts.AliasTTL > 0 && now.Sub(f.mtime) >= expiry)

		if stale {
			if err := s.remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return 0, fmt.Errorf("removing %s: %w", f.path, patherr.Inner(err))
			}

			continue
		}

		if f.kind != tempFile {
			total += f.size
		}
	}

	return total, nil
}

func (s *Store) entryPath(key Key, pinned bool) string {
	name := hex.EncodeToString(key[:])
	suffix := aliasSuffix

	if pinned {
		suffix = pinnedSuffix
	}

	return filepath.Join(s.dir, name[:2], name+suffix)
}

// Summarize counts the entries in dir and their size, without creating dir.
func Summarize(dir string, fsys FS) (Summary, error) {
	if fsys == nil {
		fsys = osFS{}
	}

	files, err := scan(fsys, dir)
	if err != nil {
		return Summary{}, err
	}

	var summary Summary

	for _, f := range files {
		if f.kind == tempFile {
			continue
		}

		summary.Entries++
		summary.Bytes += f.size

		if f.kind == aliasEntry {
			summary.Aliases++
		}
	}

	return summary, nil
}

// Summary is what a cache directory holds.
type Summary struct {
	Entries int
	Aliases int
	Bytes   int64
}

// Clear removes every entry and temporary file from dir and reports how many entries it removed. It
// refuses a directory with no CACHEDIR.TAG, so it never empties a directory onesie did not make.
func Clear(dir string, fsys FS) (int, error) {
	if fsys == nil {
		fsys = osFS{}
	}

	if _, err := fsys.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}

	if _, err := fsys.Stat(filepath.Join(dir, tagName)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf(
				"%s holds no %s, so onesie did not make it and will not empty it. Check ONESIE_CACHE_DIR",
				dir, tagName)
		}

		return 0, fmt.Errorf("reading the cache dir %s: %w", dir, err)
	}

	files, err := scan(fsys, dir)
	if err != nil {
		return 0, err
	}

	removed := 0

	for _, f := range files {
		if err := fsys.Remove(f.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return removed, fmt.Errorf("removing %s: %w", f.path, patherr.Inner(err))
		}

		if f.kind != tempFile {
			removed++
		}
	}

	return removed, nil
}

func scan(fsys FS, dir string) ([]file, error) {
	top, err := fsys.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading the cache dir %s: %w", dir, err)
	}

	var files []file

	for _, fanout := range top {
		if !fanout.IsDir() || !isFanout(fanout.Name()) {
			continue
		}

		sub := filepath.Join(dir, fanout.Name())

		names, err := fsys.ReadDir(sub)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", sub, patherr.Inner(err))
		}

		for _, named := range names {
			kind, ours := kindOf(fanout.Name(), named.Name())
			if !ours || !named.Type().IsRegular() {
				continue
			}

			info, err := named.Info()
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}

			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", filepath.Join(sub, named.Name()), patherr.Inner(err))
			}

			files = append(files, file{
				path:  filepath.Join(sub, named.Name()),
				kind:  kind,
				size:  info.Size(),
				mtime: info.ModTime(),
			})
		}
	}

	return files, nil
}

func isFanout(name string) bool {
	return len(name) == 2 && isLowerHex(name)
}

func kindOf(fanout, name string) (fileKind, bool) {
	if strings.HasPrefix(name, tempPrefix) {
		return tempFile, true
	}

	if stem, found := strings.CutSuffix(name, aliasSuffix); found && isKey(fanout, stem) {
		return aliasEntry, true
	}

	if stem, found := strings.CutSuffix(name, pinnedSuffix); found && isKey(fanout, stem) {
		return pinnedEntry, true
	}

	return 0, false
}

func isKey(fanout, stem string) bool {
	return len(stem) == 2*sha256.Size && isLowerHex(stem) && stem[:2] == fanout
}

func isLowerHex(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}

	return true
}

type file struct {
	path  string
	kind  fileKind
	size  int64
	mtime time.Time
}

type fileKind int

const (
	pinnedEntry fileKind = iota + 1
	aliasEntry
	tempFile
)

func encodeEntry(e entry) ([]byte, error) {
	if !json.Valid(e.Value) {
		return nil, errors.New("a cache value must be JSON")
	}

	model, err := json.Marshal(e.Model)
	if err != nil {
		return nil, err
	}

	stored, err := e.Stored.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("encoding the stored time: %w", err)
	}

	// The value goes in as it is, since encoding/json would compact it and a value must come back
	// byte for byte.
	var out bytes.Buffer

	fmt.Fprintf(&out, `{"version":%d,"model":%s,"stored":%s,"value":`, entryVersion, model, stored)
	out.Write(e.Value)
	out.WriteByte('}')

	return out.Bytes(), nil
}

func decodeEntry(data []byte) (entry, error) {
	var wire struct {
		Version *int            `json:"version"`
		Model   *string         `json:"model"`
		Stored  *time.Time      `json:"stored"`
		Value   json.RawMessage `json:"value"`
	}

	if err := json.Unmarshal(data, &wire); err != nil {
		return entry{}, err
	}

	if wire.Version == nil || *wire.Version != entryVersion {
		return entry{}, errors.New("an entry of another version")
	}

	if wire.Model == nil || wire.Stored == nil || len(wire.Value) == 0 {
		return entry{}, errors.New("an entry missing a field")
	}

	return entry{Model: *wire.Model, Stored: *wire.Stored, Value: wire.Value}, nil
}

type entry struct {
	Model  string
	Stored time.Time
	Value  json.RawMessage
}

func (osFS) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (osFS) Stat(path string) (os.FileInfo, error)        { return os.Stat(path) }
func (osFS) Rename(from, to string) error                 { return os.Rename(from, to) }
func (osFS) Remove(path string) error                     { return os.Remove(path) }
func (osFS) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (osFS) ReadDir(path string) ([]os.DirEntry, error)   { return os.ReadDir(path) }

func (osFS) CreateTemp(dir, pattern string) (TempFile, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}

	return file, nil
}

func (osFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (osFS) Chtimes(path string, atime, mtime time.Time) error {
	return os.Chtimes(path, atime, mtime)
}

type osFS struct{}
