package qfile

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
		if path, matchErr := matchOne(name, repo, entries); path != "" || matchErr != nil {
			return path, matchErr
		}
	}

	config, err := configSet(env, repo)
	if err != nil {
		return "", err
	}

	if config.dir != "" {
		if path, matchErr := matchOne(name, config.dir, config.entries); path != "" || matchErr != nil {
			return path, matchErr
		}
	}

	return "", notFound(name, env.WorkDir, repo, config)
}

// List reports every name Find would resolve, the repository set first and each set sorted by name.
// A name the repository set holds is left out of the config dir's, since Find never reaches it there.
// A config dir that cannot be resolved is not searched, as in Find.
func List(env FindEnv) ([]Found, error) {
	repo, repoEntries, err := repoSet(env)
	if err != nil {
		return nil, err
	}

	config, err := configSet(env, repo)
	if err != nil {
		return nil, err
	}

	listed := namesIn(repo, repoEntries, true)
	shadowed := map[string]bool{}

	for _, name := range listed {
		shadowed[name.Name] = true
	}

	for _, name := range namesIn(config.dir, config.entries, false) {
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
	// Resolve follows symlinks, so the config dir is compared with the repository set by what it
	// points at. filepath.EvalSymlinks in production.
	Resolve func(string) (string, error)
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

func configSet(env FindEnv, repo string) (configDir, error) {
	dir, unresolved := questionsDir(env)
	if dir == "" {
		return configDir{unresolved: unresolved}, nil
	}

	if repo != "" && canonical(env, dir) == canonical(env, repo) {
		return configDir{}, nil
	}

	entries, _, err := readSet(env, dir)
	if err != nil {
		return configDir{}, err
	}

	// Named in a not found message even when absent, since it is where a user would save a set.
	return configDir{dir: dir, entries: entries}, nil
}

func questionsDir(env FindEnv) (string, error) {
	root, err := env.ConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(root, "questions"), nil
}

type configDir struct {
	dir        string
	entries    []fs.DirEntry
	unresolved error
}

func canonical(env FindEnv, dir string) string {
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(env.WorkDir, dir)
	}

	dir = filepath.Clean(dir)

	if resolved, err := env.Resolve(dir); err == nil {
		return resolved
	}

	return dir
}

func readSet(env FindEnv, dir string) ([]fs.DirEntry, bool, error) {
	entries, err := env.ReadDir(dir)
	// A file named .onesie in a parent is no set, and says so with ENOTDIR rather than not found.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, fmt.Errorf("onesie: reading %s: %w", dir, err)
	}

	return entries, true, nil
}

func namesIn(dir string, entries []fs.DirEntry, inRepo bool) []Found {
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

func notFound(name, workDir, repo string, config configDir) error {
	var searched []string
	if repo != "" {
		searched = append(searched, repo)
	}

	if config.dir != "" {
		searched = append(searched, config.dir)
	}

	var tried []string
	for _, ext := range extensions {
		tried = append(tried, name+ext)
	}

	message := fmt.Sprintf("onesie: no file or question file named %s. Looked for %s", name, joinAnd(tried))
	if len(searched) > 0 {
		message += " in " + joinAnd(searched)
	}

	if repo == "" {
		message += fmt.Sprintf(". Found no .onesie/questions in %s or any parent", workDir)
	}

	if config.unresolved != nil {
		return &unsearchedError{message: message, cause: config.unresolved}
	}

	return errors.New(message)
}

type unsearchedError struct {
	message string
	cause   error
}

func (e *unsearchedError) Error() string {
	return fmt.Sprintf("%s. The config dir could not be resolved, so it was not searched: %s",
		e.message, strings.TrimPrefix(e.cause.Error(), "onesie: "))
}

func (e *unsearchedError) Unwrap() error {
	return e.cause
}

func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}

	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}
