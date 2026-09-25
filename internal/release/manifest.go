package release

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	errPrerelease = errors.New("a prerelease names no manifest, so it bumps nothing")
	manifestKeys  = []struct{ file, key string }{
		{file: path.Join(".claude-plugin", "plugin.json"), key: "version"},
		{file: path.Join(".codex-plugin", "plugin.json"), key: "version"},
		{file: path.Join(".claude-plugin", "marketplace.json"), key: "ref"},
		{file: path.Join(".agents", "plugins", "marketplace.json"), key: "ref"},
	}
)

// Bump sets the version in both plugin manifests and the ref in both marketplace files under root to tag.
// It reads and checks all four before it writes any, and under dryRun it writes nothing.
func Bump(root string, tag Tag, dryRun bool, fsys Files) ([]Change, error) {
	if tag.Prerelease() {
		return nil, fmt.Errorf("%w: %s is a prerelease", errPrerelease, tag)
	}

	if fsys == nil {
		fsys = osFiles{}
	}

	edits := make([]edit, 0, len(manifestKeys))

	for _, target := range manifestKeys {
		name := filepath.Join(root, filepath.FromSlash(target.file))

		data, err := fsys.ReadFile(name)
		if err != nil {
			return nil, err
		}

		value := strings.TrimPrefix(tag.String(), "v")
		if target.key == "ref" {
			value = tag.String()
		}

		next, change, err := replaceOnce(data, target.file, target.key, value)
		if err != nil {
			return nil, err
		}

		edits = append(edits, edit{name: name, data: next, change: change})
	}

	changes := make([]Change, 0, len(edits))

	for _, e := range edits {
		if !dryRun {
			if err := fsys.WriteFile(e.name, e.data); err != nil {
				return changes, err
			}
		}

		changes = append(changes, e.change)
	}

	return changes, nil
}

// Change is one value Bump sets, or would set under a dry run.
type Change struct {
	File, Key, From, To string
}

// Describe words c as a line of output, as what Bump would set when dryRun is true.
func (c Change) Describe(dryRun bool) string {
	text := fmt.Sprintf("set %s %s from %s to %s", c.File, c.Key, c.From, c.To)
	if dryRun {
		return "would " + text
	}

	return text
}

// Files reads and writes the manifests Bump edits.
type Files interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte) error
}

type edit struct {
	name   string
	data   []byte
	change Change
}

func replaceOnce(data []byte, file, key, value string) ([]byte, Change, error) {
	pattern := regexp.MustCompile(`("` + regexp.QuoteMeta(key) + `"\s*:\s*")([^"\\]*)(")`)

	matches := pattern.FindAllSubmatchIndex(data, -1)
	if len(matches) != 1 {
		return nil, Change{}, fmt.Errorf("%s holds %q %d times, want once", file, key, len(matches))
	}

	m := matches[0]
	from := string(data[m[4]:m[5]])

	next := make([]byte, 0, len(data)+len(value))
	next = append(next, data[:m[4]]...)
	next = append(next, value...)
	next = append(next, data[m[5]:]...)

	return next, Change{File: file, Key: key, From: from, To: value}, nil
}

type osFiles struct{}

func (osFiles) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (osFiles) WriteFile(name string, data []byte) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}

	return os.WriteFile(name, data, info.Mode().Perm())
}
