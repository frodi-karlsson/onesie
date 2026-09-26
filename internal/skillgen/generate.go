package skillgen

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const skillsDir = "skills"

var (
	// The optional frontmatter each skill loader accepts, from the evidence in this repo. It shows
	// none of them taking allowed-tools or argument-hint, so none is marked, and a field reaches a
	// SKILL.md only once every loader that reads that file is.
	claudeCode = Frontmatter{}
	codex      = Frontmatter{}
	cursor     = Frontmatter{}
	geminiCLI  = Frontmatter{}

	// skills/NAME/SKILL.md is read by Claude Code and Codex through the plugin manifests, and by
	// Cursor and Gemini CLI as a portable skill.
	sourceReaders = []Frontmatter{claudeCode, codex, cursor, geminiCLI}

	mirrors = []mirror{
		{dir: filepath.Join(".agents", "skills"), readers: []Frontmatter{codex}},
		{dir: filepath.Join(".cursor", "skills"), readers: []Frontmatter{cursor}},
	}
)

// Generate validates every skills/*/skill.json under root and writes each client's outputs and
// mirrors, touching only changed files. Nothing is written unless every skill validates.
func Generate(root string) error {
	found, err := Skills(root)
	if err != nil {
		return err
	}

	var outputs []output

	names := make([]string, 0, len(found))

	for _, f := range found {
		built, err := skillOutputs(root, f.Dir, f.Skill)
		if err != nil {
			return err
		}

		outputs = append(outputs, built...)
		names = append(names, f.Dir)
	}

	// A failure here can leave a partial tree, which the next successful run repairs, since every
	// write is independent.
	for _, out := range outputs {
		if err := writeIfChanged(out.path, out.content); err != nil {
			return err
		}
	}

	for _, m := range mirrors {
		if err := pruneMirror(filepath.Join(root, m.dir), names); err != nil {
			return err
		}
	}

	return nil
}

// Skills reads and validates every skills/*/skill.json under root, in directory order.
func Skills(root string) ([]Found, error) {
	skillsRoot := filepath.Join(root, skillsDir)

	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("onesie: %w", err)
	}

	var found []Found

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		path := filepath.Join(skillsRoot, entry.Name(), "skill.json")

		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}

			return nil, fmt.Errorf("onesie: %w", err)
		}

		skill, err := Load(path)
		if err != nil {
			return nil, err
		}

		found = append(found, Found{Dir: entry.Name(), Skill: skill})
	}

	return found, nil
}

// Found is a skill together with the directory it was read from.
type Found struct {
	Dir   string
	Skill Skill
}

func skillOutputs(root, name string, skill Skill) ([]output, error) {
	skillDir := filepath.Join(root, skillsDir, name)

	intro, err := readFragment(skillDir, skill.Intro)
	if err != nil {
		return nil, err
	}

	sections, err := readFragments(skillDir, skill.Sections)
	if err != nil {
		return nil, err
	}

	source := filepath.Join(skillsDir, name, "skill.json")

	outputs := []output{
		{
			path:    filepath.Join(skillDir, "SKILL.md"),
			content: RenderSkill(skill, acceptedByAll(sourceReaders), intro, sections, source),
		},
		{path: filepath.Join(skillDir, "agents", "gemini.toml"), content: RenderGemini(skill, source)},
	}

	for _, m := range mirrors {
		outputs = append(outputs, output{
			path:    filepath.Join(root, m.dir, name, "SKILL.md"),
			content: RenderSkill(skill, acceptedByAll(m.readers), intro, sections, source),
		})
	}

	references, err := referenceCopies(root, skillDir, name, skill.References)
	if err != nil {
		return nil, err
	}

	return append(outputs, references...), nil
}

type mirror struct {
	dir     string
	readers []Frontmatter
}

func acceptedByAll(readers []Frontmatter) Frontmatter {
	accepted := Frontmatter{AllowedTools: true, ArgumentHint: true}

	for _, r := range readers {
		accepted.AllowedTools = accepted.AllowedTools && r.AllowedTools
		accepted.ArgumentHint = accepted.ArgumentHint && r.ArgumentHint
	}

	return accepted
}

func readFragments(skillDir string, names []string) ([]string, error) {
	contents := make([]string, len(names))

	for i, name := range names {
		content, err := readFragment(skillDir, name)
		if err != nil {
			return nil, err
		}

		contents[i] = content
	}

	return contents, nil
}

func readFragment(skillDir, name string) (string, error) {
	if name == "" {
		return "", nil
	}

	data, err := os.ReadFile(filepath.Join(skillDir, name))
	if err != nil {
		return "", fmt.Errorf("onesie: %w", err)
	}

	return string(data), nil
}

func referenceCopies(root, skillDir, name string, references []string) ([]output, error) {
	var outputs []output

	for _, ref := range references {
		data, err := os.ReadFile(filepath.Join(skillDir, ref))
		if err != nil {
			return nil, fmt.Errorf("onesie: %w", err)
		}

		outputs = append(outputs, mirrorOutputs(root, name, ref, data)...)
	}

	return outputs, nil
}

func mirrorOutputs(root, name, rel string, content []byte) []output {
	outputs := make([]output, 0, len(mirrors))

	for _, m := range mirrors {
		outputs = append(outputs, output{path: filepath.Join(root, m.dir, name, rel), content: content})
	}

	return outputs
}

type output struct {
	path    string
	content []byte
}

func writeIfChanged(path string, content []byte) error {
	existing, err := os.ReadFile(path)

	switch {
	case err == nil && bytes.Equal(existing, content):
		return nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("onesie: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("onesie: %w", err)
	}

	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("onesie: %w", err)
	}

	return nil
}

func pruneMirror(mirrorRoot string, keep []string) error {
	entries, err := os.ReadDir(mirrorRoot)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return fmt.Errorf("onesie: %w", err)
	}

	keepSet := make(map[string]bool, len(keep))
	for _, name := range keep {
		keepSet[name] = true
	}

	for _, entry := range entries {
		if keepSet[entry.Name()] {
			continue
		}

		// mirrorRoot is always .agents/skills or .cursor/skills, so this can never reach outside a
		// mirror directory.
		if err := os.RemoveAll(filepath.Join(mirrorRoot, entry.Name())); err != nil {
			return fmt.Errorf("onesie: %w", err)
		}
	}

	return nil
}
