package qfile_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func TestIsName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "should take a bare word as a name", value: "triage", want: true},
		{name: "should take a word with a dash as a name", value: "ticket-triage", want: true},
		{name: "should take a value with an extension as a path", value: "triage.yaml"},
		{name: "should take a value with a slash as a path", value: "./triage"},
		{name: "should take a value with a nested slash as a path", value: "sets/triage"},
		{name: "should take a value with a backslash as a path", value: `sets\triage`},
		{name: "should take a hidden file as a path", value: ".triage"},
		{name: "should take an empty value as no name", value: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := qfile.IsName(tc.value); got != tc.want {
				t.Errorf("IsName(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestFind(t *testing.T) {
	t.Parallel()

	repoDir := filepath.Join(string(filepath.Separator)+"repo", ".onesie", "questions")
	configDir := filepath.Join(string(filepath.Separator)+"home", ".config", "onesie")
	configQuestions := filepath.Join(configDir, "questions")
	workDir := filepath.Join(string(filepath.Separator)+"repo", "sub", "deeper")

	errNoHome := errors.New("onesie: cannot find a home directory for the config dir, where the credential file lives")

	tests := []struct {
		name      string
		files     []string
		configErr error
		want      string
		wantErr   []string
		wantIs    error
	}{
		{
			name:  "should find a yaml file in the repository",
			files: []string{"/repo/.onesie/questions/triage.yaml"},
			want:  filepath.Join(repoDir, "triage.yaml"),
		},
		{
			name:  "should find a yml file in the repository",
			files: []string{"/repo/.onesie/questions/triage.yml"},
			want:  filepath.Join(repoDir, "triage.yml"),
		},
		{
			name:  "should find a json file in the repository",
			files: []string{"/repo/.onesie/questions/triage.json"},
			want:  filepath.Join(repoDir, "triage.json"),
		},
		{
			name: "should prefer the repository over the config dir",
			files: []string{
				"/repo/.onesie/questions/triage.yaml",
				"/home/.config/onesie/questions/triage.yaml",
			},
			want: filepath.Join(repoDir, "triage.yaml"),
		},
		{
			name:  "should fall back to the config dir",
			files: []string{"/home/.config/onesie/questions/triage.json"},
			want:  filepath.Join(configQuestions, "triage.json"),
		},
		{
			name: "should fall back to the config dir when the repository set lacks the name",
			files: []string{
				"/repo/.onesie/questions/other.yaml",
				"/home/.config/onesie/questions/triage.yaml",
			},
			want: filepath.Join(configQuestions, "triage.yaml"),
		},
		{
			name: "should stop the walk at the nearest questions directory",
			files: []string{
				"/repo/sub/.onesie/questions/other.yaml",
				"/repo/.onesie/questions/triage.yaml",
				"/home/.config/onesie/questions/triage.yaml",
			},
			want: filepath.Join(configQuestions, "triage.yaml"),
		},
		{
			name: "should let a nearer set shadow a parent's",
			files: []string{
				"/repo/sub/deeper/.onesie/questions/triage.yml",
				"/repo/.onesie/questions/triage.yaml",
			},
			want: filepath.Join(string(filepath.Separator)+"repo", "sub", "deeper", ".onesie", "questions",
				"triage.yml"),
		},
		{
			name:  "should walk up to the filesystem root",
			files: []string{"/.onesie/questions/triage.yaml"},
			want:  filepath.Join(string(filepath.Separator), ".onesie", "questions", "triage.yaml"),
		},
		{
			name:  "should skip a directory that carries the name",
			files: []string{"/repo/.onesie/questions/triage.yaml/inner.yaml", "/home/.config/onesie/questions/triage.yaml"},
			want:  filepath.Join(configQuestions, "triage.yaml"),
		},
		{
			name:  "should not match another name with the same prefix",
			files: []string{"/repo/.onesie/questions/triage-old.yaml"},
			wantErr: []string{
				"onesie: no file or question file named triage",
				"triage.yaml, triage.yml and triage.json",
				repoDir,
				configQuestions,
			},
		},
		{
			name: "should name both files of a clash in one directory",
			files: []string{
				"/repo/.onesie/questions/triage.yaml",
				"/repo/.onesie/questions/triage.json",
				"/home/.config/onesie/questions/triage.yaml",
			},
			wantErr: []string{
				"onesie: question file triage is both",
				filepath.Join(repoDir, "triage.yaml"),
				filepath.Join(repoDir, "triage.json"),
				"Remove one",
			},
		},
		{
			name: "should name a clash in the config dir",
			files: []string{
				"/home/.config/onesie/questions/triage.yaml",
				"/home/.config/onesie/questions/triage.yml",
			},
			wantErr: []string{
				filepath.Join(configQuestions, "triage.yaml"),
				filepath.Join(configQuestions, "triage.yml"),
			},
		},
		{
			name: "should name every directory searched when no repository set exists",
			wantErr: []string{
				"onesie: no file or question file named triage",
				configQuestions,
				"no .onesie/questions in " + workDir + " or any parent",
			},
		},
		{
			name:      "should not need the config dir when the repository has the name",
			files:     []string{"/repo/.onesie/questions/triage.yaml"},
			configErr: errNoHome,
			want:      filepath.Join(repoDir, "triage.yaml"),
		},
		{
			name:      "should name the repository set and an unresolved config dir for a missing name",
			files:     []string{"/repo/.onesie/questions/other.yaml"},
			configErr: errNoHome,
			wantErr: []string{
				"onesie: no file or question file named triage",
				"in " + repoDir + ".",
				"The config dir could not be resolved, so it was not searched: " +
					"cannot find a home directory for the config dir",
			},
			wantIs: errNoHome,
		},
		{
			name:      "should say nothing was searched when neither set exists",
			configErr: errNoHome,
			wantErr: []string{
				"Found no .onesie/questions in " + workDir + " or any parent",
				"The config dir could not be resolved",
			},
			wantIs: errNoHome,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.Find("triage", fakeEnv(tc.files, workDir, configDir, tc.configErr))

			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("Find = %s, want an error", got)
				}

				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("Find error = %v, want it to contain %s", err, want)
					}
				}

				if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
					t.Errorf("Find error = %v, want it to wrap %v", err, tc.wantIs)
				}

				return
			}

			if err != nil {
				t.Fatalf("Find: %v", err)
			}

			if got != tc.want {
				t.Errorf("Find = %s, want %s", got, tc.want)
			}
		})
	}

	t.Run("should walk past a parent whose .onesie is a file", func(t *testing.T) {
		t.Parallel()

		env := fakeEnv([]string{"/repo/.onesie/questions/triage.yaml"}, workDir, configDir, nil)
		listed := env.ReadDir
		blocked := filepath.Join(string(filepath.Separator)+"repo", "sub", ".onesie", "questions")

		env.ReadDir = func(dir string) ([]fs.DirEntry, error) {
			if dir == blocked {
				return nil, &fs.PathError{Op: "open", Path: dir, Err: syscall.ENOTDIR}
			}

			return listed(dir)
		}

		got, err := qfile.Find("triage", env)
		if err != nil {
			t.Fatalf("Find: %v", err)
		}

		if want := filepath.Join(repoDir, "triage.yaml"); got != want {
			t.Errorf("Find = %s, want %s", got, want)
		}
	})

	t.Run("should name a config dir that is the repository set once", func(t *testing.T) {
		t.Parallel()

		repoRoot := filepath.Join(string(filepath.Separator)+"repo", ".onesie")

		tests := []struct {
			name    string
			config  string
			resolve func(string) (string, error)
		}{
			{
				name:   "should compare a relative config dir as absolute",
				config: filepath.Join("..", ".onesie"),
			},
			{
				name:   "should compare a symlinked config dir by its target",
				config: filepath.Join(string(filepath.Separator) + "linked"),
				resolve: func(path string) (string, error) {
					linked := filepath.Join(string(filepath.Separator)+"linked", "questions")
					if path == linked {
						return repoDir, nil
					}

					return path, nil
				},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				env := fakeEnv([]string{"/repo/.onesie/questions/other.yaml"}, workDir, tc.config, nil)
				if tc.resolve != nil {
					env.Resolve = tc.resolve
				}

				_, err := qfile.Find("triage", env)
				if err == nil {
					t.Fatal("Find found triage, want an error")
				}

				if count := strings.Count(err.Error(), repoRoot); count != 1 {
					t.Errorf("Find error = %v, names the repository set %d times, want once", err, count)
				}
			})
		}
	})

	t.Run("should report a directory that cannot be read", func(t *testing.T) {
		t.Parallel()

		denied := errors.New("permission denied")

		env := qfile.FindEnv{
			WorkDir:   workDir,
			ConfigDir: func() (string, error) { return configDir, nil },
			ReadDir:   func(string) ([]fs.DirEntry, error) { return nil, denied },
		}

		if _, err := qfile.Find("triage", env); !errors.Is(err, denied) {
			t.Errorf("Find error = %v, want it to wrap %v", err, denied)
		}
	})
}

func fakeEnv(files []string, workDir, configDir string, configErr error) qfile.FindEnv {
	tree := fstest.MapFS{}
	for _, file := range files {
		tree[strings.TrimPrefix(file, "/")] = &fstest.MapFile{Data: []byte("a: q\n")}
	}

	return qfile.FindEnv{
		WorkDir: workDir,
		ConfigDir: func() (string, error) {
			if configErr != nil {
				return "", configErr
			}

			return configDir, nil
		},
		ReadDir: func(dir string) ([]fs.DirEntry, error) {
			return fs.ReadDir(tree, fsPath(dir))
		},
		Resolve: func(path string) (string, error) { return path, nil },
	}
}

func fsPath(dir string) string {
	trimmed := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(dir)), "/")
	if trimmed == "" {
		return "."
	}

	return trimmed
}

func TestList(t *testing.T) {
	t.Parallel()

	repoDir := filepath.Join(string(filepath.Separator)+"repo", ".onesie", "questions")
	configDir := filepath.Join(string(filepath.Separator)+"home", ".config", "onesie")
	configQuestions := filepath.Join(configDir, "questions")
	workDir := filepath.Join(string(filepath.Separator)+"repo", "sub")

	errNoHome := errors.New("onesie: cannot find a home directory for the config dir, where the credential file lives")

	tests := []struct {
		name      string
		files     []string
		configErr error
		want      []qfile.Found
	}{
		{
			name: "should list nothing when no set exists",
		},
		{
			name: "should list the repository set before the config dir's, each sorted by name",
			files: []string{
				"/repo/.onesie/questions/triage.yaml",
				"/repo/.onesie/questions/billing.json",
				"/home/.config/onesie/questions/support.yml",
				"/home/.config/onesie/questions/alpha.yaml",
			},
			want: []qfile.Found{
				{Name: "billing", Paths: []string{filepath.Join(repoDir, "billing.json")}, InRepo: true},
				{Name: "triage", Paths: []string{filepath.Join(repoDir, "triage.yaml")}, InRepo: true},
				{Name: "alpha", Paths: []string{filepath.Join(configQuestions, "alpha.yaml")}},
				{Name: "support", Paths: []string{filepath.Join(configQuestions, "support.yml")}},
			},
		},
		{
			name: "should list a shadowed name only from the repository",
			files: []string{
				"/repo/.onesie/questions/triage.yaml",
				"/home/.config/onesie/questions/triage.json",
			},
			want: []qfile.Found{
				{Name: "triage", Paths: []string{filepath.Join(repoDir, "triage.yaml")}, InRepo: true},
			},
		},
		{
			name: "should list both files of a clash in extension order",
			files: []string{
				"/repo/.onesie/questions/triage.json",
				"/repo/.onesie/questions/triage.yaml",
			},
			want: []qfile.Found{
				{Name: "triage", InRepo: true, Paths: []string{
					filepath.Join(repoDir, "triage.yaml"), filepath.Join(repoDir, "triage.json"),
				}},
			},
		},
		{
			name: "should skip what -f would not find",
			files: []string{
				"/repo/.onesie/questions/notes.txt",
				"/repo/.onesie/questions/two.parts.yaml",
				"/repo/.onesie/questions/.yaml",
				"/repo/.onesie/questions/nested.yaml/inner.yaml",
			},
		},
		{
			name:      "should list the repository set when the config dir cannot be resolved",
			files:     []string{"/repo/.onesie/questions/triage.yaml"},
			configErr: errNoHome,
			want: []qfile.Found{
				{Name: "triage", Paths: []string{filepath.Join(repoDir, "triage.yaml")}, InRepo: true},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := qfile.List(fakeEnv(tc.files, workDir, configDir, tc.configErr))
			if err != nil {
				t.Fatalf("List: %v", err)
			}

			if !slices.EqualFunc(got, tc.want, func(a, b qfile.Found) bool {
				return a.Name == b.Name && slices.Equal(a.Paths, b.Paths) && a.InRepo == b.InRepo
			}) {
				t.Errorf("List = %v, want %v", got, tc.want)
			}
		})
	}
}
