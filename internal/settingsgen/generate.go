// Package settingsgen writes the files the settings rows generate: the question file schema and the
// settings blocks of REFERENCE.md.
package settingsgen

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/qfile"
	"github.com/frodi-karlsson/onesie/internal/settings"
)

// Generate writes every generated file under root, rewriting only a file whose content changed. A
// missing marker fails it before anything is written.
func Generate(root string, files FS) error {
	outputs, err := render(root, files)
	if err != nil {
		return err
	}

	for _, out := range outputs {
		if !out.changed {
			continue
		}

		if err := files.WriteFile(filepath.Join(root, out.name), out.content); err != nil {
			return fmt.Errorf("onesie: %w", err)
		}
	}

	return nil
}

// Check renders every generated file in memory and returns the names of those that differ from
// what root holds, writing nothing.
func Check(root string, files FS) ([]string, error) {
	outputs, err := render(root, files)
	if err != nil {
		return nil, err
	}

	var stale []string

	for _, out := range outputs {
		if out.changed {
			stale = append(stale, out.name)
		}
	}

	return stale, nil
}

// FS reads and writes the files Generate renders, so a test needs no disk.
type FS interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte) error
}

// OS returns the FS of the real filesystem, which creates a missing directory before it writes.
func OS() FS {
	return osFS{}
}

type osFS struct{}

func (osFS) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (osFS) WriteFile(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}

	return os.WriteFile(name, data, 0o644)
}

func render(root string, files FS) ([]output, error) {
	generated := []struct {
		name      string
		build     func(existing []byte) ([]byte, error)
		mustExist bool
	}{
		{name: "schema/questions.json", build: func([]byte) ([]byte, error) { return qfile.Schema(), nil }},
		{name: "REFERENCE.md", build: withBlocks, mustExist: true},
	}

	outputs := make([]output, 0, len(generated))

	for _, file := range generated {
		existing, err := files.ReadFile(filepath.Join(root, filepath.FromSlash(file.name)))
		missing := errors.Is(err, fs.ErrNotExist)

		if err != nil && (file.mustExist || !missing) {
			return nil, fmt.Errorf("onesie: %w", err)
		}

		content, err := file.build(existing)
		if err != nil {
			return nil, fmt.Errorf("onesie: %s: %w", file.name, err)
		}

		outputs = append(outputs, output{
			name: file.name, content: content, changed: missing || !bytes.Equal(existing, content),
		})
	}

	return outputs, nil
}

type output struct {
	name    string
	content []byte
	changed bool
}

func withBlocks(existing []byte) ([]byte, error) {
	text := string(existing)
	rows := settings.Table()

	for _, block := range []struct {
		name string
		body string
	}{
		{name: "environment", body: environmentBlock(rows)},
		{name: "question_keys", body: questionKeysBlock(rows)},
		{name: "profile_members", body: profileMembersBlock(creds.FileMembers())},
	} {
		replaced, err := replaceBlock(text, block.name, block.body)
		if err != nil {
			return nil, err
		}

		text = replaced
	}

	return []byte(text), nil
}

func replaceBlock(text, name, body string) (string, error) {
	begin, end := marker(name, "begin"), marker(name, "end")

	for _, tag := range []string{begin, end} {
		switch strings.Count(text, tag) {
		case 0:
			return "", fmt.Errorf("no %s marker", strings.Trim(tag, "<!-> "))
		case 1:
		default:
			return "", fmt.Errorf("the %s marker appears more than once", strings.Trim(tag, "<!-> "))
		}
	}

	start := strings.Index(text, begin) + len(begin)
	stop := strings.Index(text, end)

	if stop < start {
		return "", fmt.Errorf("the settings:%s end marker comes before its begin marker", name)
	}

	return text[:start] + "\n" + body + text[stop:], nil
}

func marker(name, edge string) string {
	return "<!-- settings:" + name + " " + edge + " -->"
}

func environmentBlock(rows []settings.Row) string {
	var variables []settings.EnvSpec
	for _, row := range rows {
		variables = append(variables, row.Env...)
	}

	slices.SortStableFunc(variables, func(a, b settings.EnvSpec) int {
		return cmp.Compare(a.Order, b.Order)
	})

	table := tableHead("Variable", "What it does")

	for i := 0; i < len(variables); {
		names := []string{codeSpan(variables[i].Name)}

		next := i + 1
		for ; next < len(variables) && variables[next].Order == variables[i].Order; next++ {
			names = append(names, codeSpan(variables[next].Name))
		}

		table += tableRow(strings.Join(names, ", "), variables[i].Help)
		i = next
	}

	return table
}

func questionKeysBlock(rows []settings.Row) string {
	table := tableHead("Key", "Where", "What it does")

	for _, row := range rows {
		if row.FileKey != "" {
			table += tableRow(codeSpan(row.FileKey), keyPlace(row), row.FileHelp)
		}
	}

	return table
}

func keyPlace(row settings.Row) string {
	switch {
	case row.Scope == settings.ScopeQuestion:
		return "a question"
	case row.InBody:
		return "a request body"
	default:
		return "the file"
	}
}

func profileMembersBlock(members []creds.Member) string {
	table := tableHead("Member", "Meaning")

	for _, member := range members {
		keys := make([]string, 0, len(member.Keys))
		for _, key := range member.Keys {
			keys = append(keys, codeSpan(key))
		}

		table += tableRow(strings.Join(keys, ", "), member.Meaning)
	}

	return table
}

func tableHead(headings ...string) string {
	rules := make([]string, 0, len(headings))
	for _, heading := range headings {
		rules = append(rules, strings.Repeat("-", len(heading)+2))
	}

	return tableRow(headings...) + "|" + strings.Join(rules, "|") + "|\n"
}

func tableRow(cells ...string) string {
	return "| " + strings.Join(cells, " | ") + " |\n"
}

func codeSpan(text string) string {
	return "`" + text + "`"
}
