package cli

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
)

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

// outFile is only opened on the first write, so a command rejected before it produces anything
// leaves the file, and the answers a resume needs, untouched.
type outFile struct {
	path   string
	open   func(name string, flag int, perm os.FileMode) (*os.File, error)
	resume bool
	keep   int64
	file   *os.File
}

func (o *outFile) Write(p []byte) (int, error) {
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

func (o *outFile) create() error {
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
