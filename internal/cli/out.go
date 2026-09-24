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

	"github.com/frodi-karlsson/onesie/internal/plan"
)

const fingerprintSuffix = ".onesie"

func openOut(settings rootSettings, flags *runFlags) (*outFile, error) {
	if flags.out == "" {
		return nil, nil
	}

	out := &outFile{path: flags.out, open: settings.openFile}

	if !flags.resume {
		return out, nil
	}

	if flags.output == "csv" || flags.output == "tsv" {
		rows, length, err := completeRows(settings, flags.out, flags.output == "csv")
		if err != nil {
			return nil, err
		}

		out.resume = true
		out.keep = length

		// The first row is the header, which is written once and answers no record.
		if rows > 0 {
			flags.resumeSkip = rows - 1
			flags.resumeHeader = true
		}

		return out, nil
	}

	lines, length, err := completeLines(settings, flags.out)
	if err != nil {
		return nil, err
	}

	out.resume = true
	out.keep = length
	flags.resumeSkip = lines

	return out, nil
}

type outFile struct {
	path        string
	open        func(name string, flag int, perm os.FileMode) (*os.File, error)
	resume      bool
	keep        int64
	fingerprint string
	file        *os.File
}

func (o *outFile) Write(p []byte) (int, error) {
	// Opened on the first write, so a command rejected before it writes leaves the file, and the
	// answers a resume needs, untouched.
	if o.file == nil {
		if err := o.create(); err != nil {
			return 0, err
		}
	}

	return o.file.Write(p)
}

func (o *outFile) finish(runErr error) error {
	// A run that failed before writing anything leaves the file as it was, so an interrupt or an
	// outage never costs the answers a resume needs.
	succeeded := runErr == nil
	if o.file == nil && !succeeded {
		return nil
	}

	if o.file == nil {
		if err := o.create(); err != nil {
			return err
		}
	}

	if err := o.file.Close(); err != nil {
		return fmt.Errorf("onesie: closing %s: %w", o.path, err)
	}

	return nil
}

func (o *outFile) bind(fingerprint string) error {
	if o.resume {
		if err := o.checkFingerprint(fingerprint); err != nil {
			return err
		}
	}

	o.fingerprint = fingerprint

	return nil
}

func (o *outFile) checkFingerprint(fingerprint string) error {
	written, err := o.holdsAnswers()
	if err != nil || !written {
		return err
	}

	stored, found, err := o.readFingerprint()
	if err != nil {
		return err
	}

	if !found {
		return fmt.Errorf(
			"onesie: %s has no fingerprint beside it, so onesie cannot tell which run wrote it. "+
				"Drop --resume to start over", o.path)
	}

	if stored != fingerprint {
		return fmt.Errorf(
			"onesie: the questions, model, --map or --id changed since %s was written. "+
				"Drop --resume to start over", o.path)
	}

	return nil
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
	if err := o.openAnswers(); err != nil {
		return err
	}

	// After the answers file is opened, so a run cut off between the two never leaves an earlier
	// run's answers beside a fingerprint that vouches for this one.
	if err := o.writeFingerprint(); err != nil {
		return errors.Join(err, o.file.Close())
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
	if o.fingerprint == "" {
		return nil
	}

	path := o.path + fingerprintSuffix

	file, err := o.open(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("onesie: opening %s: %w", path, err)
	}

	_, err = io.WriteString(file, o.fingerprint+"\n")
	if err != nil {
		err = fmt.Errorf("onesie: writing %s: %w", path, err)
	}

	return errors.Join(err, file.Close())
}

func fingerprintOf(questions []plan.Question, model, mapSource, idSource string) (string, error) {
	encoded, err := json.Marshal(struct {
		Questions json.Marshaler `json:"questions"`
		Model     string         `json:"model"`
		Map       string         `json:"map"`
		ID        string         `json:"id"`
	}{Questions: wireAll(questions), Model: model, Map: mapSource, ID: idSource})
	if err != nil {
		return "", fmt.Errorf("onesie: fingerprinting the run: %w", err)
	}

	sum := sha256.Sum256(encoded)

	return hex.EncodeToString(sum[:]), nil
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
