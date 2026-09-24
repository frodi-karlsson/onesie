package qfile_test

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func FuzzIsName(f *testing.F) {
	for _, seed := range []string{"triage", "ticket-triage", "triage.yaml", "./triage", "sets/triage", `sets\triage`, ".triage", ""} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		if !qfile.IsName(value) {
			return
		}

		// A name is joined onto a searched directory, so it must stay one element inside it.
		for _, ext := range []string{".yaml", ".yml", ".json"} {
			dir := filepath.Join(string(filepath.Separator)+"set", "questions")
			if joined := filepath.Join(dir, value+ext); filepath.Dir(joined) != dir || filepath.Base(joined) != value+ext {
				t.Fatalf("IsName(%q) took a name that joins to %s outside %s", value, joined, dir)
			}
		}
	})
}

func FuzzFind(f *testing.F) {
	f.Add("triage", uint8(2), uint8(0), "triage.yaml", "triage.json", false)
	f.Add("triage", uint8(2), uint8(0), "triage.yaml\x00triage.json", "", false)
	f.Add("triage", uint8(2), uint8(1), "triage.yaml/", "triage.yaml", false)
	f.Add("triage", uint8(1), uint8(0), "triage-old.yaml", "", true)
	f.Add("triage", uint8(3), uint8(3), "other.yaml", "triage.yml", false)
	f.Add("triage", uint8(0), uint8(0), "two.parts.yaml\x00.yaml\x00notes.txt", "", false)
	f.Add("triage", uint8(1), uint8(0), "../triage.yaml", "../../triage.json", false)
	f.Add("triage", uint8(0), uint8(0), `sets\triage.yaml`, "", false)

	f.Fuzz(func(t *testing.T, name string, depth, level uint8, repoNames, configNames string, relative bool) {
		sep := string(filepath.Separator)
		workDir := sep + "repo"

		for i := range int(depth % 4) {
			workDir = filepath.Join(workDir, "sub"+string(rune('a'+i)))
		}

		repoRoot := workDir
		for range int(level) % (int(depth%4) + 2) {
			repoRoot = filepath.Dir(repoRoot)
		}

		repoDir := filepath.Join(repoRoot, ".onesie", "questions")
		configRoot := filepath.Join(sep+"home", ".config", "onesie")

		if relative {
			configRoot = filepath.Join("..", "cfg")
		}

		configDir := filepath.Join(configRoot, "questions")
		sets := map[string][]fs.DirEntry{repoDir: entries(repoNames), configDir: entries(configNames)}

		env := qfile.FindEnv{
			WorkDir:   workDir,
			ConfigDir: func() (string, error) { return configRoot, nil },
			ReadDir: func(dir string) ([]fs.DirEntry, error) {
				if listed, ok := sets[dir]; ok {
					return listed, nil
				}

				return nil, fs.ErrNotExist
			},
			Resolve: func(path string) (string, error) { return path, nil },
		}

		searched := []string{repoDir, configDir}

		path, err := qfile.Find(name, env)
		if err == nil {
			if !qfile.IsName(name) {
				t.Fatalf("Find(%q) = %s, a match for a value IsName takes as a path", name, path)
			}

			checkFound(t, name, path, searched, sets)
		}

		listed, err := qfile.List(env)
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		for _, found := range listed {
			if !qfile.IsName(found.Name) {
				t.Fatalf("List named %q, which IsName takes as a path", found.Name)
			}

			for _, listedPath := range found.Paths {
				checkFound(t, found.Name, listedPath, searched, sets)
			}
		}
	})
}

func checkFound(t *testing.T, name, path string, searched []string, sets map[string][]fs.DirEntry) {
	t.Helper()

	dir := filepath.Dir(path)
	if !slices.Contains(searched, dir) {
		t.Fatalf("found %q at %s, outside the searched %v", name, path, searched)
	}

	base := filepath.Base(path)
	if base != name+".yaml" && base != name+".yml" && base != name+".json" {
		t.Fatalf("found %q at %s, which is not the name with a question file extension", name, path)
	}

	if !slices.ContainsFunc(sets[dir], func(entry fs.DirEntry) bool { return entry.Name() == base && !entry.IsDir() }) {
		t.Fatalf("found %q at %s, which the directory does not list as a file", name, path)
	}
}

func entries(joined string) []fs.DirEntry {
	var listed []fs.DirEntry

	for name := range strings.SplitSeq(joined, "\x00") {
		trimmed, isDir := strings.CutSuffix(name, "/")
		listed = append(listed, entry{name: trimmed, dir: isDir})
	}

	return listed
}

type entry struct {
	name string
	dir  bool
}

func (e entry) Name() string { return e.name }

func (e entry) IsDir() bool { return e.dir }

func (e entry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}

	return 0
}

func (e entry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }
