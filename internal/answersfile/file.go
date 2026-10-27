// Package answersfile opens, locks, resumes and compacts the answers file a run writes with --out.
package answersfile

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/patherr"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	fingerprintSuffix  = ".onesie"
	fingerprintVersion = 2
	compactSuffix      = ".onesie.part"
	forwardGap         = 64 << 10
	maxLinks           = 40
)

var (
	errLinkLoop = errors.New("too many levels of symbolic links")
	// ErrStaleAnswers matches the refusal of an --out file whose fingerprint does not match the run,
	// so a caller of the root command can tell it apart without reading the message.
	ErrStaleAnswers = errors.New("stale answers")
)

// Open resolves and locks the answers file at config.Path and reads what a resume skips. When an
// error comes after the lock, the File comes back still locked for the caller to release.
func Open(opts Options, config Config) (*File, Resume, error) {
	if config.Path == "" {
		return nil, Resume{}, nil
	}

	opts = opts.withDefaults()
	if opts.Lock == nil {
		opts.Lock = NewLocker(opts).Lock
	}

	out := &File{
		path:     config.Path,
		open:     opts.OpenFile,
		rename:   opts.Rename,
		remove:   opts.Remove,
		goos:     opts.GOOS,
		readOnly: config.ReadOnly,
	}

	// Once, and ahead of the lock, so a run through a symlink and a run into its target take the
	// same lock, read the same fingerprint and compact beside the same file.
	target, err := resolveTarget(config.Path, opts.Resolve, opts.Readlink)
	if err != nil {
		return nil, Resume{}, err
	}

	out.target = target

	special, err := specialFile(target, opts.GOOS, opts.Stat)
	if err != nil {
		return nil, Resume{}, err
	}

	out.special = special

	// A device or a pipe has no answers another run could lose, so it takes no lock.
	if special {
		if config.Resume {
			return nil, Resume{}, fmt.Errorf(
				"--resume needs --out to name a regular file, and %s is not one", config.Path)
		}

		return out, Resume{}, nil
	}

	// Held for the whole run, since a second run appending to the file, truncating it or renaming
	// its own compaction over it would lose answers this one wrote.
	if err := out.lock(opts.Lock, config.Resume); err != nil {
		return nil, Resume{}, err
	}

	if !config.Resume {
		return out, Resume{}, nil
	}

	resume, resumeErr := resumeOut(out, config)
	if resumeErr != nil {
		return out, Resume{}, resumeErr
	}

	return out, resume, nil
}

// Options holds what Open reaches the filesystem through. A nil function or an empty GOOS takes
// the real one.
type Options struct {
	OpenFile func(name string, flag int, perm os.FileMode) (*os.File, error)
	Stat     func(name string) (fs.FileInfo, error)
	Rename   func(oldpath, newpath string) error
	Remove   func(name string) error
	Resolve  func(path string) (string, error)
	Readlink func(name string) (string, error)
	Lock     func(answers string) (release func() error, err error)
	GOOS     string
}

func (o Options) withDefaults() Options {
	if o.OpenFile == nil {
		o.OpenFile = os.OpenFile
	}

	if o.Stat == nil {
		o.Stat = os.Stat
	}

	if o.Rename == nil {
		o.Rename = os.Rename
	}

	if o.Remove == nil {
		o.Remove = os.Remove
	}

	if o.Resolve == nil {
		o.Resolve = filepath.EvalSymlinks
	}

	if o.Readlink == nil {
		o.Readlink = os.Readlink
	}

	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}

	return o
}

// Config names the answers file and says how a resume reads it.
type Config struct {
	Path     string
	Resume   bool
	Output   string
	ByID     bool
	ReadOnly bool
}

func resumeOut(out *File, config Config) (Resume, error) {
	if err := out.removeStalePart(); err != nil {
		return Resume{}, err
	}

	var resume Resume

	if config.Output == "csv" || config.Output == "tsv" {
		rows, length, err := out.completeRows(config.Output == "csv")
		if err != nil {
			return Resume{}, err
		}

		out.resumeAt(length)

		// The first row is the header, which is written once and answers no record.
		if rows > 0 {
			resume.Header = true
		}

		if rows > 0 && !config.ByID {
			resume.Skip = rows - 1
		}

		return resume, nil
	}

	lines, length, err := out.completeLines()
	if err != nil {
		return Resume{}, err
	}

	out.resumeAt(length)

	if !config.ByID {
		resume.Skip = lines
	}

	return resume, nil
}

// Resume says how many records a resume by position skips, the verdicts stored for them and whether
// the file has its header row.
type Resume struct {
	Skip   int
	Header bool
	Stored []Verdict
}

func specialFile(target, goos string, stat func(string) (fs.FileInfo, error)) (bool, error) {
	// macOS resolves /dev/stdout to /dev/fd/1, which stats as the regular file stdout was redirected
	// to, and nothing can be written beside it.
	if goos != "windows" && strings.HasPrefix(target, "/dev/") {
		return true, nil
	}

	// A path that cannot be stated is left to the write probe, which names the real failure.
	info, statErr := stat(target)
	if statErr == nil && info.IsDir() {
		return false, fmt.Errorf("--out %s is a directory", target)
	}

	return statErr == nil && !info.Mode().IsRegular(), nil
}

func resolveTarget(
	path string,
	resolve func(path string) (string, error),
	readlink func(name string) (string, error),
) (string, error) {
	at := path

	for range maxLinks {
		target, err := resolve(at)
		if err == nil {
			return target, nil
		}

		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("resolving %s: %w", path, err)
		}

		// A link to a file not written yet is followed by hand, since the resolver refuses it, and
		// the answers still have to land in its target for the link to stay one.
		link, isLink := linkAt(at, readlink)
		if !isLink {
			return at, nil
		}

		if !filepath.IsAbs(link) {
			link = filepath.Dir(at) + string(filepath.Separator) + link
		}

		at = link
	}

	return "", fmt.Errorf("resolving %s: %w", path, errLinkLoop)
}

func linkAt(path string, readlink func(name string) (string, error)) (string, bool) {
	link, err := readlink(path)

	return link, err == nil
}

// File is the answers file of one run, written through Write and compacted by Finish.
type File struct {
	path     string
	target   string
	open     func(name string, flag int, perm os.FileMode) (*os.File, error)
	rename   func(oldpath, newpath string) error
	remove   func(name string) error
	goos     string
	unlock   func() error
	special  bool
	resume   bool
	keep     int64
	bound    bool
	matched  bool
	readOnly bool

	fingerprint  string
	olderColumns string
	file         *os.File
	size         int64
	rewrite      *rewrite
}

type rewrite struct {
	lines     []Span
	delimited bool
	quoted    bool
}

func (o *File) lock(take func(answers string) (func() error, error), resume bool) error {
	unlock, err := take(o.target)
	if errors.Is(err, errLocked) {
		return fmt.Errorf("%s is in use by another onesie run. Wait for it to finish, "+
			"then run again", o.path)
	}

	// A fresh run locks only to stay clear of a resume, which could not have locked the file either.
	// So a path nothing can be locked beside is still written, without a lock.
	if err != nil && !resume {
		return nil
	}

	if err != nil {
		return fmt.Errorf("locking %s: %w", o.path, err)
	}

	o.unlock = unlock

	return nil
}

// Release lets go of the lock. It is safe on a nil File and after an earlier Release.
func (o *File) Release() error {
	if o == nil || o.unlock == nil {
		return nil
	}

	unlock := o.unlock
	o.unlock = nil

	if err := unlock(); err != nil {
		return fmt.Errorf("unlocking %s: %w", o.path, err)
	}

	return nil
}

func (o *File) resumeAt(length int64) {
	o.resume = true
	o.keep = length
	o.size = length
}

func (o *File) removeStalePart() error {
	if o.special || o.readOnly {
		return nil
	}

	part := o.target + compactSuffix
	if err := o.remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing %s, which an interrupted run left behind: %w", part, err)
	}

	return nil
}

// Offset is the length of the file as this run has written it.
func (o *File) Offset() int64 {
	return o.size
}

// CompactInto has Finish rewrite the file with these lines in this order.
func (o *File) CompactInto(lines []Span, mode output.Mode) {
	o.rewrite = &rewrite{
		lines:     lines,
		delimited: mode == output.CSV || mode == output.TSV,
		quoted:    mode == output.CSV,
	}
}

// Write appends to the file, opening it on the first write.
func (o *File) Write(p []byte) (int, error) {
	// Opened on the first write, so a command rejected before it writes leaves the file, and the
	// answers a resume needs, untouched.
	if o.file == nil {
		if err := o.create(); err != nil {
			return 0, err
		}
	}

	n, err := o.file.Write(p)
	o.size += int64(n)

	return n, err
}

// Finish closes the file once the run ended with runErr, and compacts it when CompactInto asked.
func (o *File) Finish(runErr error) error {
	// A run that failed before writing anything leaves the file as it was, so an interrupt or an
	// outage never costs the answers a resume needs.
	succeeded := runErr == nil
	if o.file == nil && !succeeded && o.rewrite == nil {
		return nil
	}

	if o.file == nil {
		if err := o.create(); err != nil {
			return err
		}
	}

	file := o.file
	o.file = nil

	if err := file.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", o.path, patherr.Inner(err))
	}

	if o.rewrite == nil {
		return nil
	}

	return o.compact()
}

func (o *File) compact() error {
	// Written beside the file and renamed over it, so an interrupt or a full disk during the rewrite
	// leaves every appended answer where it was. Beside the link's target rather than the link, so a
	// symlink at the path stays one.
	temporary := o.target + compactSuffix

	err := o.writeCompacted(temporary)
	if err == nil {
		err = o.rename(temporary, o.target)
	}

	if err == nil {
		o.syncDir(filepath.Dir(o.target))

		return nil
	}

	removeErr := o.remove(temporary)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}

	return errors.Join(fmt.Errorf("compacting %s: %w", o.path, err), removeErr)
}

func (o *File) writeCompacted(temporary string) (err error) {
	source, err := o.open(o.path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}

	defer func() {
		err = errors.Join(err, source.Close())
	}()

	info, err := source.Stat()
	if err != nil {
		return err
	}

	var header int64
	if o.rewrite.delimited {
		header, err = headerLength(io.NewSectionReader(source, 0, o.size), o.rewrite.quoted)
		if err != nil {
			return err
		}
	}

	target, err := o.open(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}

	// The mode is kept but the owner is not, so a file another user owned is left owned by the user
	// who resumed it.
	err = keepMode(target, info.Mode().Perm())
	if err == nil {
		err = copyLines(target, source, o.size, header, o.rewrite.lines)
	}

	if err == nil {
		err = target.Sync()
	}

	return errors.Join(err, target.Close())
}

func keepMode(file *os.File, want os.FileMode) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}

	// Only when the umask narrowed it, so a filesystem with fixed modes that refuses a chmod still
	// compacts.
	if info.Mode().Perm() == want {
		return nil
	}

	return file.Chmod(want)
}

func (o *File) syncDir(dir string) {
	// Windows has no durable directory flush. FlushFileBuffers refuses a directory handle.
	if o.goos == "windows" {
		return
	}

	handle, err := o.open(dir, os.O_RDONLY, 0)
	if err != nil {
		return
	}

	// Not returned. The rename has already succeeded, and a filesystem that refuses to sync a
	// directory, as some network and FUSE mounts do, would turn it into a failure the user cannot
	// act on.
	if err := errors.Join(handle.Sync(), handle.Close()); err != nil {
		return
	}
}

func copyLines(w io.Writer, source io.ReaderAt, size, header int64, lines []Span) error {
	buffered := bufio.NewWriter(w)
	reader := &forwardReader{source: source, size: size}

	if err := reader.copy(buffered, 0, header); err != nil {
		return err
	}

	for _, at := range lines {
		// The first row a fresh csv run wrote carries the header with it, and the header is
		// written once, above.
		from := max(at.Start, header)
		if from >= at.End {
			continue
		}

		if err := reader.copy(buffered, from, at.End); err != nil {
			return err
		}
	}

	return buffered.Flush()
}

type forwardReader struct {
	source   io.ReaderAt
	size     int64
	at       int64
	buffered *bufio.Reader
}

func (r *forwardReader) copy(w io.Writer, from, to int64) error {
	if from < r.at || from-r.at > forwardGap {
		r.buffered = nil
	}

	// A span that jumps back or far ahead is read on its own, since reading ahead of it would fetch
	// bytes the next span is unlikely to want.
	if r.buffered == nil && from != r.at {
		r.at = to

		_, err := io.CopyN(w, io.NewSectionReader(r.source, from, to-from), to-from)

		return err
	}

	if r.buffered == nil {
		r.buffered = bufio.NewReaderSize(io.NewSectionReader(r.source, from, r.size-from), forwardGap)
	} else if _, err := r.buffered.Discard(int(from - r.at)); err != nil {
		return err
	}

	r.at = to

	_, err := io.CopyN(w, r.buffered, to-from)

	return err
}

// Bind checks a resumed file against the run's fingerprint and probes that the file can be written.
func (o *File) Bind(questions []plan.Question, wired json.Marshaler, inputs FingerprintInputs) error {
	fingerprint, err := Fingerprint(questions, wired, inputs)
	if err != nil {
		return err
	}

	if delimitedOutput(inputs.Output) && hasFallback(questions) {
		older := inputs
		older.WithoutFallbackColumns = true

		if o.olderColumns, err = Fingerprint(questions, wired, older); err != nil {
			return err
		}
	}

	return o.bindFingerprint(fingerprint)
}

func (o *File) bindFingerprint(fingerprint string) error {
	if o.resume {
		matched, err := o.checkFingerprint(fingerprint)
		if err != nil {
			return err
		}

		o.matched = matched
	}

	o.fingerprint = fingerprint
	o.bound = true

	if o.readOnly {
		return nil
	}

	// Before any request, since a file found unwritable only at the first answer costs a request
	// and, on a fresh run, the answers the file held.
	return o.probe()
}

func (o *File) probe() error {
	if o.special {
		return nil
	}

	if err := o.probeAnswers(); err != nil {
		return fmt.Errorf("opening %s: %w", o.path, patherr.Inner(err))
	}

	if o.fingerprint == "" || o.matched {
		return nil
	}

	temporary := o.target + fingerprintSuffix + ".tmp"

	file, err := o.open(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", temporary, patherr.Inner(err))
	}

	return errors.Join(file.Close(), o.remove(temporary))
}

func (o *File) probeAnswers() error {
	file, err := o.open(o.target, os.O_WRONLY, 0)
	if err == nil {
		return file.Close()
	}

	if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	file, err = o.open(o.target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}

	return errors.Join(file.Close(), o.remove(o.target))
}

// BindWithoutFingerprint lets a fresh run write a file that keeps no fingerprint.
func (o *File) BindWithoutFingerprint() {
	if o == nil || o.resume {
		return
	}

	o.bound = true
}

// Resuming reports whether the run resumes into this file.
func (o *File) Resuming() bool {
	return o.resume
}

func (o *File) checkFingerprint(fingerprint string) (matched bool, err error) {
	written, err := o.holdsAnswers()
	if err != nil || !written {
		return false, err
	}

	stored, found, err := o.readFingerprint()
	if err != nil {
		return false, err
	}

	sidecar := o.target + fingerprintSuffix

	if !found {
		return false, staleAnswers("it has no fingerprint beside it",
			"%s has no fingerprint beside it, so onesie cannot tell which run wrote it. "+
				"Drop --resume to start over", o.path)
	}

	version, ok := fingerprintVersionOf(stored)

	switch {
	case !ok:
		return false, staleAnswers("onesie did not write its fingerprint",
			"%s does not hold a fingerprint onesie wrote, so onesie cannot tell which run "+
				"wrote %s. Drop --resume to start over", sidecar, o.path)
	case version < fingerprintVersion:
		return false, staleAnswers("an older onesie wrote its fingerprint",
			"an older onesie wrote %s, so this one cannot tell which run wrote %s. "+
				"Drop --resume to start over", sidecar, o.path)
	case version > fingerprintVersion:
		return false, staleAnswers("a newer onesie wrote its fingerprint",
			"a newer onesie wrote %s, so this one cannot tell which run wrote %s. "+
				"Drop --resume to start over", sidecar, o.path)
	case o.olderColumns != "" && stored == o.olderColumns:
		return false, staleAnswers("an older onesie wrote its columns",
			"%s was written by an older onesie, without the fallback columns this run adds. "+
				"Regenerate it without --resume, or write to a new --out", o.path)
	case stored != fingerprint:
		return false, staleAnswers("the questions, model or flags changed since it was written",
			"the questions, flags or gate changed since %s was written. "+
				"Drop --resume to start over", o.path)
	}

	return true, nil
}

// StoredFingerprint reads the fingerprint beside a file that holds answers. found is false when
// either is missing.
func (o *File) StoredFingerprint() (stored string, found bool, err error) {
	written, err := o.holdsAnswers()
	if err != nil || !written {
		return "", false, err
	}

	return o.readFingerprint()
}

func staleAnswers(cause, format string, args ...any) error {
	// Not %w, which would add the sentinel's own text to a message people already read.
	return &StaleAnswersError{Message: fmt.Sprintf(format, args...), Cause: cause}
}

// StaleAnswersError refuses a file whose fingerprint does not match the run. Cause says why, in words
// that follow since.
type StaleAnswersError struct {
	Message, Cause string
}

// Error is the message people read.
func (e *StaleAnswersError) Error() string {
	return e.Message
}

// Is matches ErrStaleAnswers.
func (e *StaleAnswersError) Is(target error) bool {
	return target == ErrStaleAnswers
}

func fingerprintVersionOf(stored string) (version int, ok bool) {
	tag, sum, found := strings.Cut(stored, ":")
	digits, tagged := strings.CutPrefix(tag, "v")

	if !found || !tagged || digits == "" || strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}

	version, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}

	if version != fingerprintVersion {
		return version, true
	}

	decoded, err := hex.DecodeString(sum)
	if err != nil || len(decoded) != sha256.Size {
		return 0, false
	}

	return version, true
}

func (o *File) holdsAnswers() (written bool, err error) {
	file, err := o.open(o.path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("reading %s to resume: %w", o.path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("reading %s to resume: %w", o.path, err)
	}

	return info.Size() > 0, nil
}

func (o *File) readFingerprint() (stored string, found bool, err error) {
	path := o.target + fingerprintSuffix

	file, err := o.open(path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("reading %s: %w", path, patherr.Inner(err))
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	data, err := io.ReadAll(io.LimitReader(file, 1024))
	if err != nil {
		return "", false, fmt.Errorf("reading %s: %w", path, patherr.Inner(err))
	}

	return string(bytes.TrimSpace(data)), true, nil
}

func (o *File) create() error {
	if o.resume && !o.bound {
		return fmt.Errorf("resuming %s, which was never checked against its fingerprint", o.path)
	}

	if err := o.openAnswers(); err != nil {
		return err
	}

	// After the answers file is opened, so a run cut off between the two never leaves an earlier
	// run's answers beside a fingerprint that vouches for this one.
	if err := o.writeFingerprint(); err != nil {
		file := o.file
		o.file = nil

		return errors.Join(err, file.Close())
	}

	return nil
}

func (o *File) openAnswers() error {
	if !o.resume {
		if err := o.removeStalePart(); err != nil {
			return err
		}

		file, err := o.open(o.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("opening %s: %w", o.path, patherr.Inner(err))
		}

		o.file = file

		return nil
	}

	file, err := o.open(o.path, os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", o.path, patherr.Inner(err))
	}

	// Drops a line the earlier run was cut off in the middle of, so the next answer starts clean.
	if err := file.Truncate(o.keep); err != nil {
		return errors.Join(fmt.Errorf("trimming %s: %w", o.path, patherr.Inner(err)), file.Close())
	}

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return errors.Join(fmt.Errorf("seeking in %s: %w", o.path, patherr.Inner(err)), file.Close())
	}

	o.file = file

	return nil
}

func (o *File) writeFingerprint() error {
	// Nothing can be written beside a device or a pipe, and nothing resumes from one.
	if o.special {
		return nil
	}

	path := o.target + fingerprintSuffix

	if o.fingerprint == "" {
		if err := o.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", path, patherr.Inner(err))
		}

		return nil
	}

	if o.matched {
		return nil
	}

	// Written beside the target and renamed over it, so a crash or a full disk leaves either the
	// old fingerprint or the new one, never an empty file that reads as a changed run.
	temporary := path + ".tmp"

	file, err := o.open(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("opening %s: %w", temporary, patherr.Inner(err))
	}

	_, err = io.WriteString(file, o.fingerprint+"\n")
	if err == nil {
		err = file.Sync()
	}

	err = errors.Join(err, file.Close())
	if err == nil {
		err = o.rename(temporary, path)
	}

	if err != nil {
		return errors.Join(fmt.Errorf("writing %s: %w", path, err), o.remove(temporary))
	}

	return nil
}

// Fingerprint sums the questions, the form they are sent in and the inputs that change an answer.
func Fingerprint(questions []plan.Question, wired json.Marshaler, inputs FingerprintInputs) (string, error) {
	encoded, err := json.Marshal(struct {
		Questions   json.Marshaler `json:"questions"`
		Policies    []policyPrint  `json:"policies"`
		Provider    string         `json:"provider"`
		Model       string         `json:"model"`
		Map         string         `json:"map"`
		ID          string         `json:"id"`
		Output      string         `json:"output"`
		Input       string         `json:"input"`
		MergeKey    string         `json:"merge_key"`
		Assert      string         `json:"assert"`
		AbstainIf   string         `json:"abstain_if"`
		SkipBlank   bool           `json:"skip_blank,omitempty"`
		Images      []string       `json:"images,omitempty"`
		LongContext bool           `json:"long_context,omitempty"`
		// A csv or tsv file written before the fallback columns existed has fewer columns, so a
		// resume into it is refused rather than appending longer rows.
		FallbackColumns bool `json:"fallback_columns,omitempty"`
		// A mock run and a real run never resume each other's file.
		Mock bool `json:"mock,omitempty"`
	}{
		Questions: wired,
		Policies:  policiesOf(questions),
		Provider:  inputs.Provider,
		Model:     inputs.Model,
		Map:       inputs.MapSource,
		ID:        inputs.IDSource,
		Output:    inputs.Output,
		Input:     inputs.Input,
		MergeKey:  inputs.MergeKey,
		Assert:    inputs.Assert,
		AbstainIf: inputs.AbstainIf,
		SkipBlank: inputs.SkipBlank,
		Images:    inputs.Images,

		LongContext: inputs.LongContext,

		FallbackColumns: !inputs.WithoutFallbackColumns && delimitedOutput(inputs.Output) && hasFallback(questions),
		Mock:            inputs.Mock,
	})
	if err != nil {
		return "", fmt.Errorf("fingerprinting the run: %w", err)
	}

	sum := sha256.Sum256(encoded)

	return fmt.Sprintf("v%d:%s", fingerprintVersion, hex.EncodeToString(sum[:])), nil
}

func delimitedOutput(name string) bool {
	return name == output.CSV.String() || name == output.TSV.String()
}

func hasFallback(questions []plan.Question) bool {
	return slices.ContainsFunc(questions, func(question plan.Question) bool {
		return question.Policy.Fallback != nil
	})
}

// FingerprintInputs are the choices beside the questions that Fingerprint covers.
type FingerprintInputs struct {
	Provider  string
	Model     string
	MapSource string
	IDSource  string
	Output    string
	Input     string
	MergeKey  string
	Assert    string
	AbstainIf string
	SkipBlank bool
	Images    []string

	LongContext bool
	Mock        bool

	WithoutFallbackColumns bool
}

func policiesOf(questions []plan.Question) []policyPrint {
	policies := make([]policyPrint, 0, len(questions))

	for _, question := range questions {
		policy := policyPrint{
			ID:            question.ID,
			Threshold:     question.Policy.Threshold,
			MinConfidence: question.Policy.MinConfidence,
		}

		if question.Policy.Fallback != nil {
			policy.Fallback = &question.Policy.Fallback.Text
		}

		policies = append(policies, policy)
	}

	return policies
}

type policyPrint struct {
	ID            string   `json:"id"`
	Threshold     *float64 `json:"threshold"`
	MinConfidence *float64 `json:"min_confidence"`
	Fallback      *string  `json:"fallback"`
}

func (o *File) completeRows(quoted bool) (rows int, length int64, err error) {
	// TSV has no quoting, so a row is a line.
	if !quoted {
		return o.completeLines()
	}

	file, err := o.open(o.path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}

	if err != nil {
		return 0, 0, fmt.Errorf("reading %s to resume: %w", o.path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	reader := csv.NewReader(bufio.NewReader(file))
	reader.FieldsPerRecord = -1

	// A quoted field can hold a newline, so rows are counted as csv rather than as lines. A row
	// only counts once its closing newline is on disk, which is what tells a finished row from one
	// cut off at a field boundary.
	for {
		if _, readErr := reader.Read(); readErr != nil {
			return rows, length, nil
		}

		offset := reader.InputOffset()

		last := make([]byte, 1)
		if _, readErr := file.ReadAt(last, offset-1); readErr != nil || last[0] != '\n' {
			return rows, length, nil
		}

		rows++
		length = offset
	}
}

func (o *File) completeLines() (lines int, length int64, err error) {
	file, err := o.open(o.path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}

	if err != nil {
		return 0, 0, fmt.Errorf("reading %s to resume: %w", o.path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	reader := bufio.NewReader(file)

	var read int64

	for {
		chunk, readErr := reader.ReadSlice('\n')
		read += int64(len(chunk))

		if readErr == nil {
			lines++
			length = read

			continue
		}

		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}

		if errors.Is(readErr, io.EOF) {
			return lines, length, nil
		}

		return 0, 0, fmt.Errorf("reading %s to resume: %w", o.path, readErr)
	}
}
