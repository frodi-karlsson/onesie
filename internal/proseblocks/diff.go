package proseblocks

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var hunkHeader = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// A diff line is a line of a source file plus one marker, so this bounds a file line too.
const maxDiffLine = 16 << 20

// ParseDiff reads a unified diff as git writes it and returns, for each file it adds lines to, the
// new side lines it added. A deleted file and a file with no added line are left out.
func ParseDiff(r io.Reader) ([]Change, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(nil, maxDiffLine)

	d := &diffParser{index: map[string]int{}}

	for scanner.Scan() {
		if err := d.line(scanner.Text()); err != nil {
			return nil, err
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading the diff: %w", err)
	}

	if d.oldLeft > 0 || d.newLeft > 0 {
		return nil, fmt.Errorf("line %d: the diff ends inside a hunk", d.n)
	}

	return d.changes, nil
}

// Change is one file a diff adds lines to. Path is the file's path on the new side.
type Change struct {
	Path  string
	Added []Lines
}

// Lines is a run of line numbers from First to Last, both included.
type Lines struct {
	First, Last int
}

type diffParser struct {
	changes []Change
	index   map[string]int

	n       int
	path    string
	next    int
	oldLeft int
	newLeft int
}

func (d *diffParser) line(text string) error {
	d.n++

	if d.oldLeft > 0 || d.newLeft > 0 {
		return d.body(text)
	}

	switch {
	case strings.HasPrefix(text, "diff "):
		d.path = ""
	case strings.HasPrefix(text, "+++ "):
		path, err := newSidePath(strings.TrimPrefix(text, "+++ "))
		if err != nil {
			return fmt.Errorf("line %d: %w", d.n, err)
		}

		d.path = path
	case strings.HasPrefix(text, "@@ "):
		return d.hunk(text)
	}

	return nil
}

func (d *diffParser) hunk(text string) error {
	match := hunkHeader.FindStringSubmatch(text)
	if match == nil {
		return fmt.Errorf("line %d: a hunk header that does not parse: %s", d.n, text)
	}

	numbers := make([]int, 0, len(match)-1)

	for _, field := range match[1:] {
		n, err := count(field)
		if err != nil {
			return fmt.Errorf("line %d: a hunk header that does not parse: %s", d.n, text)
		}

		numbers = append(numbers, n)
	}

	d.oldLeft, d.next, d.newLeft = numbers[0], numbers[1], numbers[2]

	return nil
}

func (d *diffParser) body(text string) error {
	switch {
	case strings.HasPrefix(text, "+") && d.newLeft > 0:
		d.add(d.next)
		d.next++
		d.newLeft--
	case strings.HasPrefix(text, "-") && d.oldLeft > 0:
		d.oldLeft--
	case (text == "" || strings.HasPrefix(text, " ")) && d.oldLeft > 0 && d.newLeft > 0:
		d.next++
		d.oldLeft--
		d.newLeft--
	case strings.HasPrefix(text, `\`):
	default:
		return fmt.Errorf("line %d: a hunk line that does not fit its header: %q", d.n, text)
	}

	return nil
}

func (d *diffParser) add(line int) {
	if d.path == "" {
		return
	}

	at, found := d.index[d.path]
	if !found {
		at = len(d.changes)
		d.index[d.path] = at
		d.changes = append(d.changes, Change{Path: d.path})
	}

	added := &d.changes[at].Added
	if last := len(*added) - 1; last >= 0 && (*added)[last].Last == line-1 {
		(*added)[last].Last = line

		return
	}

	*added = append(*added, Lines{First: line, Last: line})
}

func count(field string) (int, error) {
	if field == "" {
		return 1, nil
	}

	return strconv.Atoi(field)
}

func newSidePath(field string) (string, error) {
	// git ends a path that holds a space with a tab, so patch can tell where the name stops.
	field = strings.TrimSuffix(field, "\t")
	if field == "/dev/null" {
		return "", nil
	}

	if strings.HasPrefix(field, `"`) {
		unquoted, err := strconv.Unquote(field)
		if err != nil {
			return "", fmt.Errorf("a quoted path that does not parse: %s", field)
		}

		field = unquoted
	}

	path, found := strings.CutPrefix(field, "b/")
	if !found {
		return "", fmt.Errorf("a new side path without the b/ prefix: %s", field)
	}

	return path, nil
}
