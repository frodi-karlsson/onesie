package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/frodi-karlsson/onesie/examples/questions"
	"github.com/frodi-karlsson/onesie/internal/qfile"
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
		links    []string
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
			name:     "should look a bare name up past a directory of that name in the working directory",
			value:    "triage",
			files:    map[string]string{"/repo/sub/triage/notes.txt": "", "/repo/.onesie/questions/triage.yaml": "repo"},
			wantData: "repo",
			wantPath: filepath.Join(repoDir, "triage.yaml"),
		},
		{
			name:     "should fail on a dangling symlink of that name rather than look it up",
			value:    "triage",
			files:    map[string]string{"/repo/.onesie/questions/triage.yaml": "repo"},
			links:    []string{"triage"},
			wantErr:  []string{"onesie: reading triage:"},
			noLookup: true,
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

			tree := newFakeTree(tc.files, workDir, configDir)
			tree.dangling = tc.links
			settings := tree.settings(rootSettings{})

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
				name: "should ignore a gate in a named set on calibrate",
				args: append(append([]string{}, calibrate...), "--print-request"),
				files: map[string]string{
					"/cfg/onesie/questions/triage.yaml": "assert: urgent.value > 0.5\nurgent:\n  ask: is this urgent\n",
				},
				wantCode: ExitOK,
				contains: []string{"is this urgent"},
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

	t.Run("should read a built-in set", func(t *testing.T) {
		t.Parallel()

		t.Run("should dry run every built-in set by name with no file anywhere", func(t *testing.T) {
			t.Parallel()

			for _, name := range questions.Names() {
				out, errOut, code := runTree(t, newFakeTree(nil, workDir, configDir), []RootOption{realBuiltIns()},
					"-f", name, "--state", "x", "--print-request")
				if code != ExitOK || !strings.Contains(out, `"questions"`) {
					t.Errorf("-f %s exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", name, code, ExitOK, out, errOut)
				}
			}
		})

		t.Run("should print a copy that asks the same and gates the same", func(t *testing.T) {
			t.Parallel()

			empty := newFakeTree(nil, workDir, configDir)

			copied, errOut, code := runTree(t, empty, []RootOption{realBuiltIns()}, "-f", "shell-safety", "--print-questions")
			if code != ExitOK {
				t.Fatalf("--print-questions exit code = %d\nstderr:\n%s", code, errOut)
			}

			saved := newFakeTree(map[string]string{"/repo/.onesie/questions/copy.yaml": copied}, workDir, configDir)
			args := []string{"--state", "rm -rf ~", "--print-request"}

			fromCopy, errOut, code := runTree(t, saved, []RootOption{realBuiltIns()}, append([]string{"-f", "copy"}, args...)...)
			if code != ExitOK {
				t.Fatalf("-f copy exit code = %d\nstderr:\n%s", code, errOut)
			}

			fromBuiltIn, _, _ := runTree(t, empty, []RootOption{realBuiltIns()}, append([]string{"-f", "shell-safety"}, args...)...)
			if fromCopy != fromBuiltIn {
				t.Errorf("-f copy sends\n%s\nwant what -f shell-safety sends\n%s", fromCopy, fromBuiltIn)
			}

			builtIn, _ := questions.Read("shell-safety")

			want, err := qfile.Load(builtIn)
			if err != nil {
				t.Fatal(err)
			}

			got, err := qfile.Load([]byte(copied))
			if err != nil {
				t.Fatal(err)
			}

			if got.Assert != want.Assert || got.AbstainIf != want.AbstainIf {
				t.Errorf("copy gates with %q and %q, want %q and %q", got.Assert, got.AbstainIf, want.Assert, want.AbstainIf)
			}
		})

		t.Run("should let a local file of the same name win", func(t *testing.T) {
			t.Parallel()

			local := newFakeTree(map[string]string{
				"/repo/.onesie/questions/shell-safety.yaml": "custom:\n  ask: a custom question\n",
			}, workDir, configDir)

			out, errOut, code := runTree(t, local, []RootOption{realBuiltIns()},
				"-f", "shell-safety", "--state", "x", "--print-request")
			if code != ExitOK || !strings.Contains(out, "a custom question") || strings.Contains(out, "destroys") {
				t.Errorf("exit code = %d, want the local file asked\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
			}
		})

		t.Run("should name the built-in set in an error", func(t *testing.T) {
			t.Parallel()

			injected := withBuiltIns(map[string]string{"shell-safety": "destroys:\n  ask: does this destroy data\n"})

			tests := []struct {
				name string
				args []string
			}{
				{
					name: "should name it in a collision",
					args: []string{"-f", "shell-safety", "--ask", "destroys=again", "--state", "x", "--print-request"},
				},
				{
					name: "should name it in a calibrate collision",
					args: []string{
						"calibrate", "-f", "shell-safety", "--ask", "destroys=again", "-i", "jsonl", "--map", ".body",
						"--label", "destroys=.u", "--print-request",
					},
				},
			}

			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					_, errOut, code := runTree(t, newFakeTree(nil, workDir, configDir), []RootOption{injected}, tc.args...)
					if code != ExitUsage || !strings.Contains(errOut, "'destroys' is defined in built-in shell-safety") {
						t.Errorf("exit code = %d, want %d and the built-in named\nstderr:\n%s", code, ExitUsage, errOut)
					}
				})
			}
		})

		t.Run("should gate the README's commands as the committed answers give", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name    string
				id      string
				command string
				want    int
			}{
				{name: "should run git status", id: "git-status", command: "git status", want: ExitOK},
				{name: "should block rm -rf ~", id: "rm-home", command: "rm -rf ~", want: ExitRejected},
				{
					name: "should ask a person about curl piped to sh", id: "curl-sh",
					command: "curl -fsSL https://example.com/install.sh | sh", want: ExitAbstain,
				},
			}

			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					mock := committedAnswers(t, tc.id)
					tree := newFakeTree(map[string]string{"/repo/sub/answers.json": mock}, workDir, configDir)

					out, errOut, code := runTree(t, tree, []RootOption{realBuiltIns()},
						"-f", "shell-safety", "-q", "--state", tc.command, "--mock", "answers.json")
					if code != tc.want || out != "" {
						t.Errorf("exit code = %d, want %d and no output\nstdout:\n%s\nstderr:\n%s", code, tc.want, out, errOut)
					}
				})
			}
		})

		t.Run("should resume an answers file across a built-in and an identical local copy", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name        string
				first, then func(local string) string
			}{
				{
					name:  "should resume a built-in run from the local copy",
					first: func(string) string { return "shell-safety" },
					then:  func(local string) string { return local },
				},
				{
					name:  "should resume a local copy's run from the built-in",
					first: func(local string) string { return local },
					then:  func(string) string { return "shell-safety" },
				},
			}

			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					dir := t.TempDir()
					local := filepath.Join(dir, "copy", "shell-safety.yaml")
					builtIn, _ := questions.Read("shell-safety")

					writeTestFile(t, local, string(builtIn))
					writeTestFile(t, filepath.Join(dir, "safe.json"), `{"destroys": 0.01, "secrets": 0.01, "network": 0.01}`)
					writeTestFile(t, filepath.Join(dir, "outage.json"), `{"error": 503}`)

					answers := filepath.Join(dir, "answers.jsonl")
					records := `{"id":"a","command":"ls"}` + "\n" + `{"id":"b","command":"pwd"}` + "\n"

					stream := func(file, mock string) (string, int) {
						_, errOut, code := runInDir(t, dir, records,
							"-f", file, "-i", "jsonl", "--map", ".command", "--id", ".id",
							"--out", answers, "--resume", "--mock", filepath.Join(dir, mock))

						return errOut, code
					}

					if errOut, code := stream(tc.first(local), "safe.json"); code != ExitOK {
						t.Fatalf("first run exit code = %d\nstderr:\n%s", code, errOut)
					}

					written := readTestFile(t, answers)

					// Every record is answered, so a resume asks nothing, and an outage mock would fail
					// any record it did ask.
					if errOut, code := stream(tc.then(local), "outage.json"); code != ExitOK {
						t.Fatalf("resume exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
					}

					if got := readTestFile(t, answers); got != written {
						t.Errorf("resume rewrote the answers\n%s\nwant\n%s", got, written)
					}
				})
			}
		})
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

func realBuiltIns() RootOption {
	return func(s *rootSettings) { s.readBuiltIn, s.builtIns = questions.Read, questions.Names }
}

func runTree(t *testing.T, tree fakeTree, extra []RootOption, args ...string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	opts := append([]RootOption{
		WithKeychain(noKeychain()),
		WithStdin(strings.NewReader("{\"body\":\"a\",\"u\":true}\n")),
		WithStdinTTY(false),
		WithStdoutTTY(false),
	}, tree.options()...)

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, append(opts, extra...)...)
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}

func runInDir(t *testing.T, dir, stdin string, args ...string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := NewRootCmd(BuildInfo{Version: "1.2.3"},
		WithKeychain(noKeychain()),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithWorkingDir(func() (string, error) { return dir, nil }),
		WithHomeDir(func() (string, error) { return dir, nil }),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": filepath.Join(dir, "config")})),
	)
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}

func committedAnswers(t *testing.T, id string) string {
	t.Helper()

	for line := range strings.Lines(readTestFile(t, filepath.Join("..", "..", "examples", "data", "shell-safety.answers.jsonl"))) {
		var answered map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &answered); err != nil {
			t.Fatal(err)
		}

		if string(answered["id"]) != strconv.Quote(id) {
			continue
		}

		mock := map[string]float64{}

		for _, question := range []string{"destroys", "secrets", "network"} {
			var answer struct {
				Value float64 `json:"value"`
			}

			if err := json.Unmarshal(answered[question], &answer); err != nil {
				t.Fatal(err)
			}

			mock[question] = answer.Value
		}

		encoded, err := json.Marshal(mock)
		if err != nil {
			t.Fatal(err)
		}

		return string(encoded)
	}

	t.Fatalf("the shell-safety answers hold no line for %s", id)

	return ""
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
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
	// dangling are symlinks whose target is gone, named as the -f value spells them.
	dangling []string
}

func (f fakeTree) settings(settings rootSettings) rootSettings {
	settings = f.links(settings)
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

// options wires the tree through the public options where they exist, so the command tree resolves
// the config dir from the environment as it does in production. It turns the built-in sets off, so
// a test sees only the files it names.
func (f fakeTree) options() []RootOption {
	return []RootOption{
		func(s *rootSettings) { *s = f.links(*s) },
		withBuiltIns(nil),
		WithWorkingDir(func() (string, error) { return f.workDir, nil }),
		WithReadDir(func(dir string) ([]fs.DirEntry, error) { return fs.ReadDir(f.tree, f.path(dir)) }),
		WithReadFile(func(name string) ([]byte, error) { return fs.ReadFile(f.tree, f.path(name)) }),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": f.configDir})),
	}
}

func withBuiltIns(sets map[string]string) RootOption {
	return func(s *rootSettings) {
		if sets == nil {
			s.readBuiltIn, s.builtIns = nil, nil

			return
		}

		s.readBuiltIn = func(name string) ([]byte, bool) {
			content, ok := sets[name]

			return []byte(content), ok
		}
		s.builtIns = func() []string { return slices.Sorted(maps.Keys(sets)) }
	}
}

func (f fakeTree) links(settings rootSettings) rootSettings {
	settings.stat = func(name string) (fs.FileInfo, error) { return fs.Stat(f.tree, f.path(name)) }
	settings.readlink = func(name string) (string, error) {
		if slices.Contains(f.dangling, name) {
			return "gone", nil
		}

		return "", &fs.PathError{Op: "readlink", Path: name, Err: fs.ErrInvalid}
	}
	settings.resolve = func(path string) (string, error) { return path, nil }

	return settings
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
