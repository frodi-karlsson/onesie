// Package release checks a release tag against the existing ones and bumps the plugin manifests to it.
package release

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	tagPattern  = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)
	numeric     = regexp.MustCompile(`^[0-9]+$`)
	errTagForm  = errors.New("a release tag is vMAJOR.MINOR.PATCH with an optional -PRERELEASE, and no +BUILD")
	errNotNewer = errors.New("a release tag must be greater than every existing one")
)

// CheckTag refuses a tag that is malformed or not greater than the latest version tag among existing,
// which may hold plain names, refs/tags/ refs, peeled ^{} refs and git ls-remote lines.
func CheckTag(tag string, existing []string) error {
	candidate, err := ParseTag(tag)
	if err != nil {
		return err
	}

	latest, found := latestOf(existing)
	if found && !candidate.Newer(latest) {
		return fmt.Errorf("%w: %s is not, since the latest tag is %s", errNotNewer, tag, latest)
	}

	return nil
}

// ParseTag reads a tag of the form vMAJOR.MINOR.PATCH with an optional -PRERELEASE.
func ParseTag(text string) (Tag, error) {
	match := tagPattern.FindStringSubmatch(text)
	if match == nil {
		return Tag{}, fmt.Errorf("%w: %q is not one", errTagForm, text)
	}

	var parts [3]int

	for i := range parts {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return Tag{}, fmt.Errorf("%w: %q has a number too large to compare", errTagForm, text)
		}

		parts[i] = n
	}

	tag := Tag{Major: parts[0], Minor: parts[1], Patch: parts[2]}

	if match[4] != "" {
		tag.Pre = strings.Split(match[4], ".")
		for _, id := range tag.Pre {
			if numeric.MatchString(id) && len(id) > 1 && id[0] == '0' {
				return Tag{}, fmt.Errorf("%w: %q has a numeric prerelease identifier with a leading zero", errTagForm, text)
			}
		}
	}

	return tag, nil
}

// Tag is a parsed release tag.
type Tag struct {
	Major, Minor, Patch int
	Pre                 []string
}

// Newer reports whether t has a higher semver 2.0 precedence than other.
func (t Tag) Newer(other Tag) bool {
	return t.compare(other) > 0
}

// Prerelease reports whether t carries a -PRERELEASE part.
func (t Tag) Prerelease() bool {
	return len(t.Pre) > 0
}

// String prints t as the tag it was parsed from.
func (t Tag) String() string {
	text := fmt.Sprintf("v%d.%d.%d", t.Major, t.Minor, t.Patch)
	if t.Prerelease() {
		text += "-" + strings.Join(t.Pre, ".")
	}

	return text
}

func (t Tag) compare(other Tag) int {
	for _, pair := range [][2]int{{t.Major, other.Major}, {t.Minor, other.Minor}, {t.Patch, other.Patch}} {
		if pair[0] != pair[1] {
			return cmp.Compare(pair[0], pair[1])
		}
	}

	switch {
	case !t.Prerelease() && !other.Prerelease():
		return 0
	case !t.Prerelease():
		return 1
	case !other.Prerelease():
		return -1
	}

	for i := 0; i < len(t.Pre) && i < len(other.Pre); i++ {
		if c := compareIdentifiers(t.Pre[i], other.Pre[i]); c != 0 {
			return c
		}
	}

	return cmp.Compare(len(t.Pre), len(other.Pre))
}

func latestOf(existing []string) (Tag, bool) {
	var (
		latest Tag
		found  bool
	)

	for _, line := range existing {
		tag, err := ParseTag(tagName(line))
		if err != nil {
			continue
		}

		if !found || tag.Newer(latest) {
			latest, found = tag, true
		}
	}

	return latest, found
}

func tagName(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}

	name := strings.TrimPrefix(fields[len(fields)-1], "refs/tags/")

	return strings.TrimSuffix(name, "^{}")
}

func compareIdentifiers(a, b string) int {
	aNumeric, bNumeric := numeric.MatchString(a), numeric.MatchString(b)

	switch {
	case aNumeric && bNumeric:
		return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b))
	case aNumeric:
		return -1
	case bNumeric:
		return 1
	}

	return strings.Compare(a, b)
}
