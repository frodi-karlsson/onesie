package qfile

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

var extensions = []string{".yaml", ".yml", ".json"}

// IsName reports whether an -f value is a bare name to look up rather than a path. A value holding a
// path separator or a dot is always a path.
func IsName(value string) bool {
	return value != "" && !strings.ContainsAny(value, `/\.`)
}

// Find resolves a bare name to the question file it names. It searches the nearest
// .onesie/questions directory at or above the working directory, then the config dir's questions.
func Find(name string, env FindEnv) (string, error) {
	repo, entries, err := repoSet(env)
	if err != nil {
		return "", err
	}

	if repo != "" {
		if found, matchErr := matchOne(name, repo, entries); found != "" || matchErr != nil {
			return found, matchErr
		}
	}

	config, entries, err := configSet(env, repo)
	if err != nil {
		return "", err
	}

	if config != "" {
		if found, matchErr := matchOne(name, config, entries); found != "" || matchErr != nil {
			return found, matchErr
		}
	}

	return "", notFound(name, env.WorkDir, repo, config)
}

// List reports every name Find would resolve, the repository set first and each set sorted by name.
// A name the repository set holds is left out of the config dir's, since Find never reaches it there.
func List(env FindEnv) ([]Found, error) {
	repo, repoEntries, err := repoSet(env)
	if err != nil {
		return nil, err
	}

	config, configEntries, err := configSet(env, repo)
	if err != nil {
		return nil, err
	}

	listed := found(repo, repoEntries, true)
	shadowed := map[string]bool{}

	for _, name := range listed {
		shadowed[name.Name] = true
	}

	for _, name := range found(config, configEntries, false) {
		if !shadowed[name.Name] {
			listed = append(listed, name)
		}
	}

	return listed, nil
}

// Found is one name List reports. More than one path is a clash, which Find refuses.
type Found struct {
	Name  string
	Paths []string
	// InRepo is true for a name from the .onesie/questions walk, false for one from the config dir.
	InRepo bool
}

// FindEnv is everything Find reads from outside the process, so a test needs no real filesystem.
type FindEnv struct {
	// WorkDir is the absolute directory the walk up starts from.
	WorkDir string
	// ConfigDir resolves onesie's config dir, the one auth set writes to.
	ConfigDir func() (string, error)
	// ReadDir lists a directory. os.ReadDir in production.
	ReadDir func(string) ([]fs.DirEntry, error)
}

func repoSet(env FindEnv) (string, []fs.DirEntry, error) {
	for dir := env.WorkDir; ; {
		candidate := filepath.Join(dir, ".onesie", "questions")

		entries, found, err := readSet(env, candidate)
		if err != nil || found {
			return candidate, entries, err
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil, nil
		}

		dir = parent
	}
}

func configSet(env FindEnv, repo string) (string, []fs.DirEntry, error) {
	root, err := env.ConfigDir()
	if err != nil {
		return "", nil, err
	}

	dir := filepath.Join(root, "questions")
	if dir == repo {
		return "", nil, nil
	}

	entries, found, err := readSet(env, dir)
	if err != nil {
		return "", nil, err
	}

	if !found {
		// Still named in a not found message, since it is where a user would save a set.
		return dir, nil, nil
	}

	return dir, entries, nil
}

func readSet(env FindEnv, dir string) ([]fs.DirEntry, bool, error) {
	entries, err := env.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("onesie: reading %s: %w", dir, err)
	}

	return entries, true, nil
}

func found(dir string, entries []fs.DirEntry, inRepo bool) []Found {
	var names []string

	for _, entry := range entries {
		for _, ext := range extensions {
			name, isSet := strings.CutSuffix(entry.Name(), ext)
			if isSet && IsName(name) && !entry.IsDir() && !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}

	slices.Sort(names)

	listed := make([]Found, 0, len(names))
	for _, name := range names {
		listed = append(listed, Found{Name: name, Paths: matches(name, dir, entries), InRepo: inRepo})
	}

	return listed
}

func matchOne(name, dir string, entries []fs.DirEntry) (string, error) {
	paths := matches(name, dir, entries)

	switch len(paths) {
	case 0:
		return "", nil
	case 1:
		return paths[0], nil
	default:
		return "", fmt.Errorf("onesie: question file %s is both %s. Remove one, "+
			"since the order would otherwise decide", name, joinAnd(paths))
	}
}

func matches(name, dir string, entries []fs.DirEntry) []string {
	var paths []string

	for _, ext := range extensions {
		for _, entry := range entries {
			if !entry.IsDir() && entry.Name() == name+ext {
				paths = append(paths, filepath.Join(dir, entry.Name()))
			}
		}
	}

	return paths
}

func notFound(name, workDir, repo, config string) error {
	var searched []string
	if repo != "" {
		searched = append(searched, repo)
	}

	if config != "" {
		searched = append(searched, config)
	}

	var tried []string
	for _, ext := range extensions {
		tried = append(tried, name+ext)
	}

	message := fmt.Sprintf("onesie: no file or question file named %s. Looked for %s in %s",
		name, joinAnd(tried), joinAnd(searched))

	if repo == "" {
		message += fmt.Sprintf(", and found no .onesie/questions in %s or any parent", workDir)
	}

	return errors.New(message)
}

func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}

	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
