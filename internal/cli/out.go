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
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	fingerprintSuffix  = ".onesie"
	fingerprintVersion = 1
	compactSuffix      = ".onesie.part"
)

func openOut(settings rootSettings, flags *runFlags) (*outFile, error) {
	if flags.out == "" {
		return nil, nil
	}

	out := &outFile{
		path:   flags.out,
		open:   settings.openFile,
		rename: settings.rename,
		remove: settings.remove,
	}

	if !flags.resume {
		return out, nil
	}

	if flags.output == "csv" || flags.output == "tsv" {
		rows, length, err := completeRows(settings, flags.out, flags.output == "csv")
		if err != nil {
			return nil, err
		}

		out.resumeAt(length)

		// The first row is the header, which is written once and answers no record.
		if rows > 0 {
			flags.resumeHeader = true
		}

		if rows > 0 && !byID(flags) {
			flags.resumeSkip = rows - 1
		}

		return out, nil
	}

	lines, length, err := completeLines(settings, flags.out)
	if err != nil {
		return nil, err
	}

	out.resumeAt(length)

	if !byID(flags) {
		flags.resumeSkip = lines
	}

	return out, nil
}

func byID(flags *runFlags) bool {
	// Under --id the records the file answers are skipped by id as they are read, not by position.
	return flags.idSource != ""
}

type outFile struct {
	path    string
	open    func(name string, flag int, perm os.FileMode) (*os.File, error)
	rename  func(oldpath, newpath string) error
	remove  func(name string) error
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

func (o *outFile) resumeAt(length int64) {
	o.resume = true
	o.keep = length
	o.size = length
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
	// leaves every appended answer where it was.
	temporary := o.path + compactSuffix

	err := o.writeCompacted(temporary)
	if err == nil {
		err = o.rename(temporary, o.path)
	}

	if err == nil {
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

	var header int64
	if o.rewrite.delimited {
		header, err = headerLength(io.NewSectionReader(source, 0, o.size), o.rewrite.quoted)
		if err != nil {
			return err
		}
	}

	target, err := o.open(temporary, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	err = copyLines(target, source, header, o.rewrite.lines)
	if err == nil {
		err = target.Sync()
	}

	return errors.Join(err, target.Close())
}

func copyLines(w io.Writer, source io.ReaderAt, header int64, lines []span) error {
	buffered := bufio.NewWriter(w)

	if _, err := io.Copy(buffered, io.NewSectionReader(source, 0, header)); err != nil {
		return err
	}

	for _, at := range lines {
		// The first row a fresh csv run wrote carries the header with it, and the header is
		// written once, above.
		from := max(at.start, header)
		if from >= at.end {
			continue
		}

		if _, err := io.Copy(buffered, io.NewSectionReader(source, from, at.end-from)); err != nil {
			return err
		}
	}

	return buffered.Flush()
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
			"onesie: the questions, provider, model, --map or --id changed since %s was written. "+
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

func fingerprintOf(questions []plan.Question, provider, model, mapSource, idSource string) (string, error) {
	encoded, err := json.Marshal(struct {
		Questions json.Marshaler `json:"questions"`
		Provider  string         `json:"provider"`
		Model     string         `json:"model"`
		Map       string         `json:"map"`
		ID        string         `json:"id"`
	}{Questions: wireAll(questions), Provider: provider, Model: model, Map: mapSource, ID: idSource})
	if err != nil {
		return "", fmt.Errorf("onesie: fingerprinting the run: %w", err)
	}

	sum := sha256.Sum256(encoded)

	return fmt.Sprintf("v%d:%s", fingerprintVersion, hex.EncodeToString(sum[:])), nil
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
