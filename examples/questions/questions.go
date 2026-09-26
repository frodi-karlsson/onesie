// Package questions holds the starter question sets built into onesie, so -f finds them by name
// with no copy step.
package questions

import (
	"embed"
	"io/fs"
	"path"
	"slices"
	"strings"
)

//go:embed *.yaml
var files embed.FS

// Names lists the built-in sets in name order.
func Names() []string {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, strings.TrimSuffix(entry.Name(), path.Ext(entry.Name())))
	}

	slices.Sort(names)

	return names
}

// Read returns the built-in set a name gives, and false when there is none.
func Read(name string) ([]byte, bool) {
	data, err := files.ReadFile(name + ".yaml")

	return data, err == nil
}
