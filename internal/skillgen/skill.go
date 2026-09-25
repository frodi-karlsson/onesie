// Package skillgen decodes and validates skill.json, the schema a generated skill is built from.
package skillgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var nameShape = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Load reads a skill.json file, decodes it rejecting unknown fields, and validates it.
func Load(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, fmt.Errorf("onesie: %w", err)
	}

	var s Skill

	decoder := json.NewDecoder(bytes.NewReader(data))
	// DisallowUnknownFields, so a to_verify marker left on a rule cannot survive into a committed
	// skill.json by accident.
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&s); err != nil {
		return Skill{}, fmt.Errorf("onesie: %s: %w", path, err)
	}

	if decoder.More() {
		return Skill{}, fmt.Errorf("onesie: %s: trailing content after the skill object", path)
	}

	if err := Validate(path, s); err != nil {
		return Skill{}, err
	}

	return s, nil
}

// Validate enforces the skill.json field rules. path is the skill.json file itself, and fragment
// paths are checked for existence against its directory.
func Validate(path string, s Skill) error {
	v := validator{path: path, dir: filepath.Dir(path)}

	if err := v.name(s.Name); err != nil {
		return err
	}

	if err := v.description(s.Description); err != nil {
		return err
	}

	if err := v.metadata(s.Metadata); err != nil {
		return err
	}

	if err := v.rules(s.Rules); err != nil {
		return err
	}

	if err := v.fragment("intro", s.Intro); err != nil {
		return err
	}

	if err := v.fragments("sections", s.Sections); err != nil {
		return err
	}

	if err := v.fragments("references", s.References); err != nil {
		return err
	}

	return nil
}

// Skill is the decoded shape of a skill.json file.
type Skill struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Compatibility string            `json:"compatibility,omitempty"`
	License       string            `json:"license,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Intro         string            `json:"intro,omitempty"`
	Rules         []Rule            `json:"rules,omitempty"`
	Sections      []string          `json:"sections,omitempty"`
	References    []string          `json:"references,omitempty"`
}

type validator struct {
	path string
	dir  string
}

func (v validator) name(name string) error {
	if name == "" {
		return v.errorf("name is required")
	}

	if utf8.RuneCountInString(name) > 64 {
		return v.errorf("name '%s' must be at most 64 characters", name)
	}

	if !nameShape.MatchString(name) {
		return v.errorf(
			"name '%s' must be lowercase letters, digits and single hyphens, "+
				"with no leading, trailing or consecutive hyphen", name)
	}

	if want := filepath.Base(v.dir); name != want {
		return v.errorf("name '%s' must equal the directory name '%s'", name, want)
	}

	return nil
}

func (v validator) description(description string) error {
	if description == "" {
		return v.errorf("description is required")
	}

	if utf8.RuneCountInString(description) > 1024 {
		return v.errorf("description must be at most 1024 characters")
	}

	return v.noControlChars("description", description)
}

func (v validator) metadata(metadata map[string]string) error {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	for _, key := range keys {
		if err := v.noControlChars("metadata '"+key+"'", metadata[key]); err != nil {
			return err
		}
	}

	return nil
}

func (v validator) rules(rules []Rule) error {
	seen := make(map[string]bool, len(rules))

	for i, rule := range rules {
		if rule.ID == "" {
			return v.errorf("%s: id is required", ruleLabel(i, ""))
		}

		if rule.Short == "" {
			return v.errorf("%s: short is required", ruleLabel(i, rule.ID))
		}

		if err := v.noControlChars(ruleLabel(i, rule.ID)+": short", rule.Short); err != nil {
			return err
		}

		if strings.TrimSpace(rule.Why) == "" {
			return v.errorf("%s: why is required", ruleLabel(i, rule.ID))
		}

		if err := v.noControlChars(ruleLabel(i, rule.ID)+": why", rule.Why); err != nil {
			return err
		}

		if seen[rule.ID] {
			return v.errorf("%s: id is already used by another rule", ruleLabel(i, rule.ID))
		}

		seen[rule.ID] = true

		if rule.BadPasses && rule.BadUnverifiable != "" {
			return v.errorf("%s: bad_passes and bad_unverifiable contradict, keep one",
				ruleLabel(i, rule.ID))
		}

		if rule.GoodFails && rule.GoodUnverifiable != "" {
			return v.errorf("%s: good_fails and good_unverifiable contradict, keep one",
				ruleLabel(i, rule.ID))
		}
	}

	return nil
}

// Rule is one entry in a skill's rules list.
type Rule struct {
	ID               string `json:"id"`
	Short            string `json:"short"`
	Why              string `json:"why"`
	Bad              string `json:"bad,omitempty"`
	Good             string `json:"good,omitempty"`
	GoodFails        bool   `json:"good_fails,omitempty"`
	BadPasses        bool   `json:"bad_passes,omitempty"`
	BadUnverifiable  string `json:"bad_unverifiable,omitempty"`
	GoodUnverifiable string `json:"good_unverifiable,omitempty"`
}

func ruleLabel(i int, id string) string {
	if id == "" {
		return fmt.Sprintf("rules[%d]", i)
	}

	return fmt.Sprintf("rules[%d] '%s'", i, id)
}

func (v validator) fragment(field, name string) error {
	if name == "" {
		return nil
	}

	return v.fragmentEntry(field, name)
}

func (v validator) fragments(field string, names []string) error {
	seen := make(map[string]bool, len(names))

	for i, name := range names {
		label := fmt.Sprintf("%s[%d]", field, i)

		if seen[name] {
			return v.errorf("%s '%s' is already listed", label, name)
		}

		seen[name] = true

		if err := v.fragmentEntry(label, name); err != nil {
			return err
		}
	}

	return nil
}

func (v validator) fragmentEntry(label, name string) error {
	if !fs.ValidPath(name) {
		return v.errorf("%s '%s' must be a relative path inside the skill directory", label, name)
	}

	info, err := os.Stat(filepath.Join(v.dir, name))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return v.errorf("%s '%s' does not exist", label, name)
		}

		return v.errorf("%s '%s': %w", label, name, err)
	}

	if !info.Mode().IsRegular() {
		return v.errorf("%s '%s' must be a regular file", label, name)
	}

	return nil
}

func (v validator) noControlChars(context, s string) error {
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}

		if r < 0x20 || r == 0x7f {
			return v.errorf("%s has a control character", context)
		}
	}

	return nil
}

func (v validator) errorf(format string, args ...any) error {
	return fmt.Errorf("onesie: %s: "+format, append([]any{v.path}, args...)...)
}
