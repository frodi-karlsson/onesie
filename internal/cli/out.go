package cli

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
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	fingerprintSuffix  = ".onesie"
	fingerprintVersion = 2
	compactSuffix      = ".onesie.part"
	forwardGap         = 64 << 10
	maxLinks           = 40
)

var errLinkLoop = errors.New("too many levels of symbolic links")

func openOut(settings rootSettings, flags *runFlags) (*outFile, error) {
	if flags.out == "" {
		return nil, nil
	}

	out := &outFile{
		path:   flags.out,
		open:   settings.openFile,
		rename: settings.rename,
		remove: settings.remove,
		goos:   settings.goos,
	}

	// Once, and ahead of the lock, so a run through a symlink and a run into its target take the
	// same lock and compact beside the same file.
	target, err := resolveTarget(flags.out, settings.resolve, settings.readlink)
	if err != nil {
		return nil, err
	}

	out.target = target

	// Held for the whole run, since a second run appending to the file, truncating it or renaming
	// its own compaction over it would lose answers this one wrote.
	if err := out.lock(settings.lock, flags.resume); err != nil {
		return nil, err
	}

	if !flags.resume {
		return out, nil
	}

	if err := resumeOut(out, settings, flags); err != nil {
		return nil, errors.Join(err, out.release())
	}

	return out, nil
}

func resumeOut(out *outFile, settings rootSettings, flags *runFlags) error {
	if err := out.removeStalePart(); err != nil {
		return err
	}

	if flags.output == "csv" || flags.output == "tsv" {
		rows, length, err := completeRows(settings, flags.out, flags.output == "csv")
		if err != nil {
			return err
		}

		out.resumeAt(length)

		// The first row is the header, which is written once and answers no record.
		if rows > 0 {
			flags.resumeHeader = true
		}

		if rows > 0 && !resumesByID(flags) {
			flags.resumeSkip = rows - 1
		}

		return nil
	}

	lines, length, err := completeLines(settings, flags.out)
	if err != nil {
		return err
	}

	out.resumeAt(length)

	if !resumesByID(flags) {
		flags.resumeSkip = lines
	}

	return nil
}

func resumesByID(flags *runFlags) bool {
	return flags.idSource != ""
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
			return "", fmt.Errorf("onesie: resolving %s: %w", path, err)
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

	return "", fmt.Errorf("onesie: resolving %s: %w", path, errLinkLoop)
}

func linkAt(path string, readlink func(name string) (string, error)) (string, bool) {
	link, err := readlink(path)

	return link, err == nil
}

type outFile struct {
	path    string
	target  string
	open    func(name string, flag int, perm os.FileMode) (*os.File, error)
	rename  func(oldpath, newpath string) error
	remove  func(name string) error
	goos    string
	unlock  func() error
	resume  bool
	keep    int64
	bound   bool
	matched bool

	fingerprint string
	file        *os.File
	size        int64
	rewrite     *rewrite
}

type rewrite struct {
	lines     []span
	delimited bool
	quoted    bool
}

func (o *outFile) lock(take func(answers string) (func() error, error), resume bool) error {
	unlock, err := take(o.target)
	if errors.Is(err, errLocked) {
		return fmt.Errorf("onesie: %s is being resumed by another onesie run. Wait for it to finish, "+
			"then resume again", o.path)
	}

	// A fresh run locks only to stay clear of a resume, which could not have locked the file either.
	// So a path nothing can be locked beside, such as /dev/stdout, is still written as before.
	if err != nil && !resume {
		return nil
	}

	if err != nil {
		return fmt.Errorf("onesie: locking %s: %w", o.path, err)
	}

	o.unlock = unlock

	return nil
}

func (o *outFile) release() error {
	if o == nil || o.unlock == nil {
		return nil
	}

	unlock := o.unlock
	o.unlock = nil

	if err := unlock(); err != nil {
		return fmt.Errorf("onesie: unlocking %s: %w", o.path, err)
	}

	return nil
}

func (o *outFile) resumeAt(length int64) {
	o.resume = true
	o.keep = length
	o.size = length
}

func (o *outFile) removeStalePart() error {
	part := o.target + compactSuffix
	if err := o.remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("onesie: removing %s, which an interrupted run left behind: %w", part, err)
	}

	return nil
}

func (o *outFile) offset() int64 {
	return o.size
}

func (o *outFile) compactInto(lines []span, mode output.Mode) {
	o.rewrite = &rewrite{
		lines:     lines,
		delimited: mode == output.CSV || mode == output.TSV,
		quoted:    mode == output.CSV,
	}
}

func (o *outFile) Write(p []byte) (int, error) {
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

func (o *outFile) finish(runErr error) error {
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
		return fmt.Errorf("onesie: closing %s: %w", o.path, err)
	}

	if o.rewrite == nil {
		return nil
	}

	return o.compact()
}

func (o *outFile) compact() error {
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

	return errors.Join(fmt.Errorf("onesie: compacting %s: %w", o.path, err), removeErr)
}

func (o *outFile) writeCompacted(temporary string) (err error) {
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

func (o *outFile) syncDir(dir string) {
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

func copyLines(w io.Writer, source io.ReaderAt, size, header int64, lines []span) error {
	buffered := bufio.NewWriter(w)
	reader := &forwardReader{source: source, size: size}

	if err := reader.copy(buffered, 0, header); err != nil {
		return err
	}

	for _, at := range lines {
		// The first row a fresh csv run wrote carries the header with it, and the header is
		// written once, above.
		from := max(at.start, header)
		if from >= at.end {
			continue
		}

		if err := reader.copy(buffered, from, at.end); err != nil {
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

func (o *outFile) bind(fingerprint string) error {
	if o.resume {
		matched, err := o.checkFingerprint(fingerprint)
		if err != nil {
			return err
		}

		o.matched = matched
	}

	o.fingerprint = fingerprint
	o.bound = true

	return nil
}

func (o *outFile) bindWithoutFingerprint() {
	if o == nil || o.resume {
		return
	}

	o.bound = true
}

func (o *outFile) checkFingerprint(fingerprint string) (matched bool, err error) {
	written, err := o.holdsAnswers()
	if err != nil || !written {
		return false, err
	}

	stored, found, err := o.readFingerprint()
	if err != nil {
		return false, err
	}

	sidecar := o.path + fingerprintSuffix

	if !found {
		return false, fmt.Errorf(
			"onesie: %s has no fingerprint beside it, so onesie cannot tell which run wrote it. "+
				"Drop --resume to start over", o.path)
	}

	version, ok := fingerprintVersionOf(stored)

	switch {
	case !ok:
		return false, fmt.Errorf(
			"onesie: %s does not hold a fingerprint onesie wrote, so onesie cannot tell which run "+
				"wrote %s. Drop --resume to start over", sidecar, o.path)
	case version < fingerprintVersion:
		return false, fmt.Errorf(
			"onesie: an older onesie wrote %s, so this one cannot tell which run wrote %s. "+
				"Drop --resume to start over", sidecar, o.path)
	case version > fingerprintVersion:
		return false, fmt.Errorf(
			"onesie: a newer onesie wrote %s, so this one cannot tell which run wrote %s. "+
				"Drop --resume to start over", sidecar, o.path)
	case stored != fingerprint:
		return false, fmt.Errorf(
			"onesie: the questions, flags or gate changed since %s was written. "+
				"Drop --resume to start over", o.path)
	}

	return true, nil
}

func fingerprintVersionOf(stored string) (version int, ok bool) {
	tag, sum, found := strings.Cut(stored, ":")
	digits, tagged := strings.CutPrefix(tag, "v")

	if !found || !tagged || digits == "" || !allDigits(digits) {
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

func allDigits(text string) bool {
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func (o *outFile) holdsAnswers() (written bool, err error) {
	file, err := o.open(o.path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("onesie: reading %s to resume: %w", o.path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	info, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("onesie: reading %s to resume: %w", o.path, err)
	}

	return info.Size() > 0, nil
}

func (o *outFile) readFingerprint() (stored string, found bool, err error) {
	path := o.path + fingerprintSuffix

	file, err := o.open(path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("onesie: reading %s: %w", path, err)
	}

	defer func() {
		err = errors.Join(err, file.Close())
	}()

	data, err := io.ReadAll(io.LimitReader(file, 1024))
	if err != nil {
		return "", false, fmt.Errorf("onesie: reading %s: %w", path, err)
	}

	return string(bytes.TrimSpace(data)), true, nil
}

func (o *outFile) create() error {
	if o.resume && !o.bound {
		return fmt.Errorf("onesie: resuming %s, which was never checked against its fingerprint", o.path)
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

func (o *outFile) openAnswers() error {
	if !o.resume {
		if err := o.removeStalePart(); err != nil {
			return err
		}

		file, err := o.open(o.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return fmt.Errorf("onesie: opening %s: %w", o.path, err)
		}

		o.file = file

		return nil
	}

	file, err := o.open(o.path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("onesie: opening %s: %w", o.path, err)
	}

	// Drops a line the earlier run was cut off in the middle of, so the next answer starts clean.
	if err := file.Truncate(o.keep); err != nil {
		return errors.Join(fmt.Errorf("onesie: trimming %s: %w", o.path, err), file.Close())
	}

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return errors.Join(fmt.Errorf("onesie: seeking in %s: %w", o.path, err), file.Close())
	}

	o.file = file

	return nil
}

func (o *outFile) writeFingerprint() error {
	path := o.path + fingerprintSuffix

	if o.fingerprint == "" {
		if err := o.remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("onesie: removing %s: %w", path, err)
		}

		return nil
	}

	if o.matched {
		return nil
	}

	// Written beside the target and renamed over it, so a crash or a full disk leaves either the
	// old fingerprint or the new one, never an empty file that reads as a changed run.
	temporary := path + ".tmp"

	file, err := o.open(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("onesie: opening %s: %w", temporary, err)
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
		return errors.Join(fmt.Errorf("onesie: writing %s: %w", path, err), o.remove(temporary))
	}

	return nil
}

func fingerprintOf(questions []plan.Question, inputs fingerprintInputs) (string, error) {
	encoded, err := json.Marshal(struct {
		Questions json.Marshaler `json:"questions"`
		Policies  []policyPrint  `json:"policies"`
		Provider  string         `json:"provider"`
		Model     string         `json:"model"`
		Map       string         `json:"map"`
		ID        string         `json:"id"`
		Output    string         `json:"output"`
		Input     string         `json:"input"`
		MergeKey  string         `json:"merge_key"`
		Assert    string         `json:"assert"`
		AbstainIf string         `json:"abstain_if"`
	}{
		Questions: wireAll(questions),
		Policies:  policiesOf(questions),
		Provider:  inputs.provider,
		Model:     inputs.model,
		Map:       inputs.mapSource,
		ID:        inputs.idSource,
		Output:    inputs.output,
		Input:     inputs.input,
		MergeKey:  inputs.mergeKey,
		Assert:    inputs.assert,
		AbstainIf: inputs.abstainIf,
	})
	if err != nil {
		return "", fmt.Errorf("onesie: fingerprinting the run: %w", err)
	}

	sum := sha256.Sum256(encoded)

	return fmt.Sprintf("v%d:%s", fingerprintVersion, hex.EncodeToString(sum[:])), nil
}

type fingerprintInputs struct {
	provider  string
	model     string
	mapSource string
	idSource  string
	output    string
	input     string
	mergeKey  string
	assert    string
	abstainIf string
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

func completeRows(settings rootSettings, path string, quoted bool) (rows int, length int64, err error) {
	// TSV has no quoting, so a row is a line.
	if !quoted {
		return completeLines(settings, path)
	}

	file, err := settings.openFile(path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}

	if err != nil {
		return 0, 0, fmt.Errorf("onesie: reading %s to resume: %w", path, err)
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

func completeLines(settings rootSettings, path string) (lines int, length int64, err error) {
	file, err := settings.openFile(path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}

	if err != nil {
		return 0, 0, fmt.Errorf("onesie: reading %s to resume: %w", path, err)
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

		return 0, 0, fmt.Errorf("onesie: reading %s to resume: %w", path, readErr)
	}
}
