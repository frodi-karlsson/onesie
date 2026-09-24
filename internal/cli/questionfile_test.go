package cli

import (
	"bytes"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestReadQuestionFile(t *testing.T) {
	t.Parallel()

	workDir := filepath.Join(string(filepath.Separator)+"repo", "sub")
	configDir := filepath.Join(string(filepath.Separator)+"cfg", "onesie")
	repoDir := filepath.Join(string(filepath.Separator)+"repo", ".onesie", "questions")

	tests := []struct {
		name     string
		value    string
		files    map[string]string
		readErr  error
		wantData string
		wantPath string
		wantErr  []string
		noLookup bool
	}{
		{
			name:     "should read a path with an extension as it is",
			value:    "triage.yaml",
			files:    map[string]string{"/repo/sub/triage.yaml": "here", "/repo/.onesie/questions/triage.yaml": "repo"},
			wantData: "here",
			wantPath: "triage.yaml",
			noLookup: true,
		},
		{
			name:     "should read a bare name that is a file in the working directory",
			value:    "triage",
			files:    map[string]string{"/repo/sub/triage": "here", "/repo/.onesie/questions/triage.yaml": "repo"},
			wantData: "here",
			wantPath: "triage",
			noLookup: true,
		},
		{
			name:     "should look a bare name up in the repository",
			value:    "triage",
			files:    map[string]string{"/repo/.onesie/questions/triage.yaml": "repo"},
			wantData: "repo",
			wantPath: filepath.Join(repoDir, "triage.yaml"),
		},
		{
			name:     "should look a bare name up in the config dir",
			value:    "triage",
			files:    map[string]string{"/cfg/onesie/questions/triage.json": "config"},
			wantData: "config",
			wantPath: filepath.Join(configDir, "questions", "triage.json"),
		},
		{
			name:     "should not look up a missing path",
			value:    "triage.yaml",
			files:    map[string]string{"/repo/.onesie/questions/triage.yaml": "repo"},
			wantErr:  []string{"onesie: reading triage.yaml:"},
			noLookup: true,
		},
		{
			name:     "should not look up a missing path with a slash",
			value:    "./triage",
			files:    map[string]string{"/repo/.onesie/questions/triage.yaml": "repo"},
			wantErr:  []string{"onesie: reading ./triage:"},
			noLookup: true,
		},
		{
			name:     "should not look up a name the working directory refuses to read",
			value:    "triage",
			readErr:  fs.ErrPermission,
			wantErr:  []string{"onesie: reading triage:"},
			noLookup: true,
		},
		{
			name:  "should name every directory searched for a missing name",
			value: "triage",
			wantErr: []string{
				"onesie: no file or question file named triage",
				filepath.Join(configDir, "questions"),
				"no .onesie/questions in " + workDir + " or any parent",
			},
		},
		{
			name:  "should refuse a name with two files in one directory",
			value: "triage",
			files: map[string]string{
				"/repo/.onesie/questions/triage.yaml": "a",
				"/repo/.onesie/questions/triage.json": "b",
			},
			wantErr: []string{
				filepath.Join(repoDir, "triage.yaml") + " and " + filepath.Join(repoDir, "triage.json"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			settings := newFakeTree(tc.files, workDir, configDir).settings(rootSettings{})

			if tc.readErr != nil {
				settings.readFile = func(string) ([]byte, error) { return nil, tc.readErr }
			}

			if tc.noLookup {
				settings.readDir = func(dir string) ([]fs.DirEntry, error) {
					t.Errorf("read %s, want no lookup", dir)

					return nil, fs.ErrNotExist
				}
			}

			data, path, err := readQuestionFile(settings, tc.value)

			if len(tc.wantErr) > 0 {
				if err == nil {
					t.Fatalf("readQuestionFile = %s, want an error", path)
				}

				for _, want := range tc.wantErr {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %v, want it to contain %s", err, want)
					}
				}

				if code := Classify(err); code != ExitUsage {
					t.Errorf("exit code = %d, want %d", code, ExitUsage)
				}

				return
			}

			if err != nil {
				t.Fatalf("readQuestionFile: %v", err)
			}

			if string(data) != tc.wantData || path != tc.wantPath {
				t.Errorf("readQuestionFile = %q, %s, want %q, %s", data, path, tc.wantData, tc.wantPath)
			}
		})
	}

	t.Run("should resolve a name through the command tree", func(t *testing.T) {
		t.Parallel()

		set := map[string]string{
			"/repo/.onesie/questions/triage.yaml": "urgent:\n  ask: is this urgent from the set\n",
		}

		calibrate := []string{"calibrate", "-f", "triage", "-i", "jsonl", "--map", ".body", "--label", "urgent=.u"}

		tests := []struct {
			name     string
			args     []string
			files    map[string]string
			wantCode int
			contains []string
		}{
			{
				name:     "should ask the questions of a named set",
				args:     []string{"-f", "triage", "--state", "the site is down", "--print-request"},
				files:    set,
				wantCode: ExitOK,
				contains: []string{"is this urgent from the set"},
			},
			{
				name: "should layer --ask on top of a named set",
				args: []string{
					"-f", "triage", "--ask", "team=which team", "--pick", "a,b", "--state", "x", "--print-request",
				},
				files:    set,
				wantCode: ExitOK,
				contains: []string{"is this urgent from the set", "which team"},
			},
			{
				name:     "should name the resolved file in a collision",
				args:     []string{"-f", "triage", "--ask", "urgent=again", "--state", "x", "--print-request"},
				files:    set,
				wantCode: ExitUsage,
				contains: []string{filepath.Join(repoDir, "triage.yaml"), "Pass --replace to override"},
			},
			{
				name:     "should exit 2 on a name found nowhere",
				args:     []string{"-f", "triage", "--state", "x", "--print-request"},
				wantCode: ExitUsage,
				contains: []string{"onesie: no file or question file named triage"},
			},
			{
				name:     "should look a calibrate -f name up",
				args:     append(append([]string{}, calibrate...), "--print-request"),
				files:    set,
				wantCode: ExitOK,
				contains: []string{"is this urgent from the set"},
			},
			{
				name: "should refuse a gate in a named set on calibrate",
				args: calibrate,
				files: map[string]string{
					"/cfg/onesie/questions/triage.yaml": "assert: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n",
				},
				wantCode: ExitUsage,
				contains: []string{"so 'assert' does not apply"},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var out, errOut bytes.Buffer

				opts := append([]RootOption{
					WithKeychain(noKeychain()),
					WithStdin(strings.NewReader("{\"body\":\"a\",\"u\":true}\n")),
					WithStdinTTY(false),
					WithStdoutTTY(false),
				}, newFakeTree(tc.files, workDir, configDir).options()...)

				root := NewRootCmd(BuildInfo{Version: "1.2.3"}, opts...)
				root.SetOut(&out)
				root.SetErr(&errOut)
				root.SetArgs(tc.args)

				if code := Execute(t.Context(), root); code != tc.wantCode {
					t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tc.wantCode, &out, &errOut)
				}

				for _, want := range tc.contains {
					if !strings.Contains(out.String()+errOut.String(), want) {
						t.Errorf("output missing %q\nstdout:\n%s\nstderr:\n%s", want, &out, &errOut)
					}
				}
			})
		}
	})

	t.Run("should report a working directory that cannot be found", func(t *testing.T) {
		t.Parallel()

		gone := errors.New("getwd: no such file or directory")

		settings := newFakeTree(nil, workDir, configDir).settings(rootSettings{})
		settings.getwd = func() (string, error) { return "", gone }

		if _, _, err := readQuestionFile(settings, "triage"); !errors.Is(err, gone) {
			t.Errorf("error = %v, want it to wrap %v", err, gone)
		}
	})
}

func newFakeTree(files map[string]string, workDir, configDir string) fakeTree {
	tree := fstest.MapFS{}
	for name, content := range files {
		tree[strings.TrimPrefix(name, "/")] = &fstest.MapFile{Data: []byte(content)}
	}

	return fakeTree{tree: tree, workDir: workDir, configDir: configDir}
}

type fakeTree struct {
	tree      fstest.MapFS
	workDir   string
	configDir string
}

func (f fakeTree) settings(settings rootSettings) rootSettings {
	settings.getwd = func() (string, error) { return f.workDir, nil }
	settings.configDir = func() (string, error) { return f.configDir, nil }
	settings.readDir = func(dir string) ([]fs.DirEntry, error) {
		return fs.ReadDir(f.tree, f.path(dir))
	}
	settings.readFile = func(name string) ([]byte, error) {
		return fs.ReadFile(f.tree, f.path(name))
	}

	return settings
}

func (f fakeTree) options() []RootOption {
	return []RootOption{
		WithWorkingDir(func() (string, error) { return f.workDir, nil }),
		WithReadDir(func(dir string) ([]fs.DirEntry, error) { return fs.ReadDir(f.tree, f.path(dir)) }),
		WithReadFile(func(name string) ([]byte, error) { return fs.ReadFile(f.tree, f.path(name)) }),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": f.configDir})),
	}
}

func (f fakeTree) path(name string) string {
	if !filepath.IsAbs(name) && !strings.HasPrefix(name, string(filepath.Separator)) {
		name = filepath.Join(f.workDir, name)
	}

	trimmed := strings.TrimPrefix(filepath.ToSlash(filepath.Clean(name)), "/")
	if trimmed == "" {
		return "."
	}

	return trimmed
}
