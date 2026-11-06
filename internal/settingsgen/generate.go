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
	"unicode"
	"unicode/utf8"

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

		if err := files.WriteFile(pathOf(root, out.name), out.content); err != nil {
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
		existing, err := files.ReadFile(pathOf(root, file.name))
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

func pathOf(root, name string) string {
	return filepath.Join(root, filepath.FromSlash(name))
}

type output struct {
	name    string
	content []byte
	changed bool
}

func withBlocks(existing []byte) ([]byte, error) {
	text := string(existing)
	rows := settings.Table()

	blocks := []struct {
		name   string
		render func() (string, error)
	}{
		{name: "environment", render: func() (string, error) { return environmentBlock(rows) }},
		{name: "question_keys", render: func() (string, error) { return questionKeysBlock(rows) }},
		{name: "run_uses", render: func() (string, error) { return runUsesBlock(rows) }},
		{name: "profile_members", render: func() (string, error) { return profileMembersBlock(creds.FileMembers()) }},
	}

	known := make([]string, 0, len(blocks))
	for _, block := range blocks {
		known = append(known, block.name)
	}

	if err := refuseUnknownMarkers(text, known); err != nil {
		return nil, err
	}

	for _, block := range blocks {
		body, err := block.render()
		if err != nil {
			return nil, fmt.Errorf("the settings:%s block: %w", block.name, err)
		}

		replaced, err := replaceBlock(text, block.name, body)
		if err != nil {
			return nil, err
		}

		text = replaced
	}

	return []byte(text), nil
}

func refuseUnknownMarkers(text string, known []string) error {
	const opening = "<!-- settings:"

	for rest := text; ; {
		at := strings.Index(rest, opening)
		if at < 0 {
			return nil
		}

		rest = rest[at+len(opening):]

		name, _, _ := strings.Cut(rest, " ")
		if !slices.Contains(known, name) {
			return fmt.Errorf("the settings:%s marker names no block. The blocks are %s", name, strings.Join(known, ", "))
		}
	}
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

func environmentBlock(rows []settings.Row) (string, error) {
	var variables []settings.EnvSpec
	for _, row := range rows {
		variables = append(variables, row.Env...)
	}

	slices.SortStableFunc(variables, func(a, b settings.EnvSpec) int {
		return cmp.Compare(a.Order, b.Order)
	})

	table := newTable("Variable", "What it does")

	for i := 0; i < len(variables); {
		names := []string{codeSpan(variables[i].Name)}

		next := i + 1
		for ; next < len(variables) && variables[next].Order == variables[i].Order; next++ {
			names = append(names, codeSpan(variables[next].Name))
		}

		table.row(strings.Join(names, ", "), variables[i].Help)
		i = next
	}

	return table.String()
}

func questionKeysBlock(rows []settings.Row) (string, error) {
	table := newTable("Key", "Where", "What it does")

	for _, row := range rows {
		if row.FileKey != "" {
			table.row(codeSpan(row.FileKey), keyPlace(row), asCell(row.FileHelp))
		}
	}

	return table.String()
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

func asCell(sentence string) string {
	sentence = strings.TrimSuffix(sentence, ".")
	if sentence == "" {
		return sentence
	}

	first, size := utf8.DecodeRuneInString(sentence)

	return string(unicode.ToLower(first)) + sentence[size:]
}

func runUsesBlock(rows []settings.Row) (string, error) {
	table := newTable("Setting", "Flag", "Chosen profile", "Question file or body", "Environment", "Default profile")

	for _, row := range rows {
		if !ranked(row) {
			continue
		}

		var flag string
		if row.Flag.Long != "" {
			flag = codeSpan("--" + row.Flag.Long)
		}

		members := profileMembers(row)
		table.row(codeSpan(string(row.Name)), flag, members, fileSource(row), variables(row), members)
	}

	return table.String()
}

func ranked(row settings.Row) bool {
	// The provider and the profile choose the sources the other rows rank.
	if row.Scope != settings.ScopeRun || row.Name == settings.Provider.Name() || row.Name == settings.Profile.Name() {
		return false
	}

	var sources int

	for _, gives := range []bool{row.Flag.Long != "", len(row.Profile) > 0, row.FileKey != "", len(row.Env) > 0} {
		if gives {
			sources++
		}
	}

	return sources > 1
}

func profileMembers(row settings.Row) string {
	keys := make([]string, 0, len(row.Profile))
	for _, member := range row.Profile {
		keys = append(keys, codeSpan(member.Key))
	}

	return strings.Join(keys, " or ")
}

func fileSource(row settings.Row) string {
	switch {
	case row.FileKey == "":
		return ""
	case row.InBody:
		return "the request body's " + codeSpan(row.FileKey)
	default:
		return "the question file's " + codeSpan(row.FileKey)
	}
}

func variables(row settings.Row) string {
	names := make([]string, 0, len(row.Env))
	providers := map[string]bool{}

	for _, variable := range row.Env {
		name := codeSpan(variable.Name)
		if variable.Provider != "" {
			name += " on " + variable.Provider
			providers[variable.Provider] = true
		}

		names = append(names, name)
	}

	// Variables of different providers never meet in one run, so they are alternatives, not an order.
	if len(providers) == len(row.Env) {
		return strings.Join(names, ", or ")
	}

	return strings.Join(names, ", then ")
}

func profileMembersBlock(members []creds.Member) (string, error) {
	table := newTable("Member", "Meaning")

	for _, member := range members {
		keys := make([]string, 0, len(member.Keys))
		for _, key := range member.Keys {
			keys = append(keys, codeSpan(key))
		}

		table.row(strings.Join(keys, ", "), member.Meaning)
	}

	return table.String()
}

func newTable(headings ...string) *table {
	t := &table{}
	t.row(headings...)

	rules := make([]string, 0, len(headings))
	for _, heading := range headings {
		rules = append(rules, strings.Repeat("-", len(heading)+2))
	}

	t.text.WriteString("|" + strings.Join(rules, "|") + "|\n")

	return t
}

type table struct {
	text strings.Builder
	err  error
}

func (t *table) row(cells ...string) {
	escaped := make([]string, 0, len(cells))

	for _, cell := range cells {
		if strings.ContainsAny(cell, "\r\n") && t.err == nil {
			t.err = fmt.Errorf("a cell holds a newline, which ends a table row: %q", cell)
		}

		escaped = append(escaped, strings.ReplaceAll(cell, "|", `\|`))
	}

	t.text.WriteString("| " + strings.Join(escaped, " | ") + " |\n")
}

func (t *table) String() (string, error) {
	return t.text.String(), t.err
}

func codeSpan(text string) string {
	return "`" + text + "`"
}
