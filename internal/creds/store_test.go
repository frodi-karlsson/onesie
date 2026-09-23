package creds_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/creds"
)

func TestLoadThroughASymlink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		targetMode os.FileMode
		wantErr    string
	}{
		{
			// Open follows the link, so the refusal comes from the target and the message must
			// name the target's mode. os.Lstat would report the link's own 0o755 here, which
			// names the wrong object and suggests a chmod that would fix nothing.
			name:       "should name the target's mode when the owner cannot read it",
			targetMode: 0o060,
			wantErr:    "mode 60",
		},
		{
			name:       "should name the target's mode when others can reach it",
			targetMode: 0o644,
			wantErr:    "mode 644",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode check is a no op")
			}

			dir := t.TempDir()
			target := filepath.Join(dir, "target.json")
			link := filepath.Join(dir, "credentials.json")

			if err := os.WriteFile(target, []byte(`{"api_key":"k"}`), 0o600); err != nil {
				t.Fatalf("writing the target: %v", err)
			}

			if err := os.Chmod(target, tc.targetMode); err != nil {
				t.Fatalf("setting the target mode: %v", err)
			}

			if err := os.Symlink(target, link); err != nil {
				t.Fatalf("linking: %v", err)
			}

			_, found, err := creds.NewStore().Load(link)
			if err == nil {
				t.Fatalf("Load succeeded, want a refusal naming %s", tc.wantErr)
			}

			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Load error = %v, want it to contain %s", err, tc.wantErr)
			}

			if found {
				t.Error("Load reported found beside a refusal")
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()

	// unixOnly marks the cases that turn on permission bits. Windows carries none, so the mode
	// check is scoped out there and the fixture could not be written in the first place.
	tests := []struct {
		name     string
		contents string
		mode     os.FileMode
		absent   bool
		dir      bool
		unixOnly bool
		goos     string
		want     creds.File
		wantErr  string
	}{
		{
			name:     "should read a key and a base url",
			contents: `{"api_key":"k","base_url":"https://proxy.example"}`,
			mode:     0o600,
			want:     creds.File{APIKey: "k", BaseURL: "https://proxy.example"},
		},
		{
			name:     "should read a key with no base url",
			contents: `{"api_key":"k"}`,
			mode:     0o600,
			want:     creds.File{APIKey: "k"},
		},
		{
			name:   "should report an absent file as absent rather than an error",
			absent: true,
		},
		{
			name:     "should refuse a file readable by the group",
			contents: `{"api_key":"k"}`,
			mode:     0o640,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 640",
		},
		{
			name:     "should refuse a file readable by the world",
			contents: `{"api_key":"k"}`,
			mode:     0o604,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 604",
		},
		{
			name:     "should refuse a file the group can write but not read",
			contents: `{"api_key":"k"}`,
			mode:     0o620,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 620",
		},
		{
			name:     "should refuse a file others can reach before it looks at the contents",
			contents: `not json`,
			mode:     0o640,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 640",
		},
		{
			name:     "should refuse a file its owner cannot read",
			contents: `{"api_key":"k"}`,
			mode:     0o060,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 60",
		},
		{
			name:     "should refuse a file only the world can read",
			contents: `{"api_key":"k"}`,
			mode:     0o006,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 6",
		},
		{
			name:     "should refuse a directory before it complains about its mode",
			dir:      true,
			mode:     0o755,
			unixOnly: true,
			wantErr:  "is not a regular file",
		},
		{
			name:    "should refuse a directory at the credential path",
			dir:     true,
			mode:    0o700,
			wantErr: "is not a regular file",
		},
		{
			name:     "should read a file at exactly the read cap",
			contents: `{"api_key":"` + strings.Repeat("k", (1<<20)-14) + `"}`,
			mode:     0o600,
			want:     creds.File{APIKey: strings.Repeat("k", (1<<20)-14)},
		},
		{
			name:     "should refuse a file larger than the read cap",
			contents: `{"api_key":"` + strings.Repeat("k", 1<<20) + `"}`,
			mode:     0o600,
			wantErr:  "is larger than 1048576 bytes",
		},
		{
			name:     "should refuse a file with no api_key",
			contents: `{}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a file whose api_key is empty",
			contents: `{"api_key":""}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a file that is not JSON",
			contents: `not json`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a file whose key is not a string",
			contents: `{"api_key":42}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a file whose base_url is not a string",
			contents: `{"api_key":"k","base_url":42}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a group readable file on a unix filesystem",
			contents: `{"api_key":"k"}`,
			goos:     "linux",
			mode:     0o640,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 640",
		},
		{
			name:     "should accept a group readable file on windows, where the mode carries no meaning",
			contents: `{"api_key":"k"}`,
			goos:     "windows",
			mode:     0o640,
			want:     creds.File{APIKey: "k"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode check does not apply")
			}

			path := filepath.Join(t.TempDir(), "credentials.json")

			if tc.dir {
				if err := os.Mkdir(path, tc.mode); err != nil {
					t.Fatalf("creating the fixture directory: %v", err)
				}
				// Mkdir respects umask, so the mode is set explicitly afterwards.
				if err := os.Chmod(path, tc.mode); err != nil {
					t.Fatalf("setting the fixture directory mode: %v", err)
				}
			}

			if !tc.absent && !tc.dir {
				if err := os.WriteFile(path, []byte(tc.contents), tc.mode); err != nil {
					t.Fatalf("writing the fixture: %v", err)
				}
				// WriteFile respects umask, so the mode is set explicitly afterwards.
				if err := os.Chmod(path, tc.mode); err != nil {
					t.Fatalf("setting the fixture mode: %v", err)
				}
			}

			goos := tc.goos
			if goos == "" {
				goos = runtime.GOOS
			}

			got, found, err := creds.NewStore(creds.WithGOOS(goos)).Load(path)

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Load error = %v, want it to contain %s", err, tc.wantErr)
				}

				if found {
					t.Errorf("found = true, want false alongside an error")
				}

				if got != (creds.File{}) {
					t.Errorf("Load returned %+v alongside an error, want the zero File", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if found == tc.absent {
				t.Fatalf("found = %v, want %v", found, !tc.absent)
			}

			if got != tc.want {
				t.Errorf("Load = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSave(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file creds.File
		want string
	}{
		{
			name: "should write a key alone",
			file: creds.File{APIKey: "k"},
			want: `{"api_key":"k"}`,
		},
		{
			name: "should write a key and a base url",
			file: creds.File{APIKey: "k", BaseURL: "https://proxy.example"},
			want: `{"api_key":"k","base_url":"https://proxy.example"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "jev", "credentials.json")

			warning, err := creds.NewStore().Save(path, tc.file)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}

			if warning != nil {
				t.Fatalf("Save warned unexpectedly: %v", warning)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}

			if strings.TrimSpace(string(data)) != tc.want {
				// Never printed, since a parameterised case would make this the key.
				t.Errorf("wrote %d bytes that do not match the expected object", len(data))
			}

			// The mode is the point of this feature, so assert it rather than the bytes alone.
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}

			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Errorf("file mode = %o, want 600", info.Mode().Perm())
			}

			// The jev subdirectory is created by Save, so its mode is Save's to assert. The
			// overwrite test below does not assert it, since MkdirAll leaves an existing
			// directory's mode alone.
			dir, err := os.Stat(filepath.Dir(path))
			if err != nil {
				t.Fatalf("stat dir: %v", err)
			}

			if runtime.GOOS != "windows" && dir.Mode().Perm() != 0o700 {
				t.Errorf("directory mode = %o, want 700", dir.Mode().Perm())
			}
		})
	}

	t.Run("should read back what it wrote", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			file creds.File
		}{
			{
				name: "should read back a key with no base url",
				file: creds.File{APIKey: "k"},
			},
			{
				name: "should read back a key and a base url",
				file: creds.File{APIKey: "k", BaseURL: "https://proxy.example"},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				path := filepath.Join(t.TempDir(), "jev", "credentials.json")
				store := creds.NewStore()

				if _, err := store.Save(path, tc.file); err != nil {
					t.Fatalf("Save: %v", err)
				}

				got, found, err := store.Load(path)
				if err != nil {
					t.Fatalf("Load: %v", err)
				}

				if !found {
					t.Fatal("Load reported the file it just wrote as absent")
				}

				if got != tc.file {
					t.Errorf("round trip changed the file, base url = %q, want %q", got.BaseURL, tc.file.BaseURL)
				}
			})
		}
	})

	t.Run("should overwrite an existing file", func(t *testing.T) {
		t.Parallel()

		t.Run("should replace the contents and restore mode 600", func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "credentials.json")

			if err := os.WriteFile(path, []byte(`{"api_key":"old"}`), 0o644); err != nil {
				t.Fatalf("writing the fixture: %v", err)
			}
			// WriteFile respects umask, so the leaked mode is set explicitly afterwards.
			if err := os.Chmod(path, 0o644); err != nil {
				t.Fatalf("setting the fixture mode: %v", err)
			}

			warning, err := creds.NewStore().Save(path, creds.File{APIKey: "new"})
			if err != nil {
				t.Fatalf("Save: %v", err)
			}

			if warning != nil {
				t.Fatalf("Save warned unexpectedly: %v", warning)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}

			if strings.TrimSpace(string(data)) != `{"api_key":"new"}` {
				t.Error("Save left the old contents in place")
			}

			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}

			if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
				t.Errorf("file mode = %o, want 600", info.Mode().Perm())
			}
		})
	})

	t.Run("should leave no temporary file behind", func(t *testing.T) {
		t.Parallel()

		// A scratch file left behind has leaked the key into a second path, so the directory is
		// asserted whole rather than only at the credential path.
		tests := []struct {
			name        string
			blockRename bool
		}{
			{name: "should leave the credential file as the only entry"},
			{name: "should remove the temporary file when the rename fails", blockRename: true},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				path := filepath.Join(dir, "credentials.json")

				if tc.blockRename {
					// A rename onto an existing directory fails, which is the cheapest way to reach
					// Save's cleanup path without stubbing the filesystem.
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatalf("creating the blocking directory: %v", err)
					}
				}

				_, err := creds.NewStore().Save(path, creds.File{APIKey: "k"})

				if tc.blockRename && err == nil {
					t.Fatal("Save succeeded onto a directory, want a failure")
				}

				if !tc.blockRename && err != nil {
					t.Fatalf("Save: %v", err)
				}

				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatalf("reading the directory: %v", err)
				}

				if len(entries) != 1 {
					names := make([]string, 0, len(entries))
					for _, entry := range entries {
						names = append(names, entry.Name())
					}

					t.Fatalf("directory holds %v, want credentials.json alone", names)
				}

				if entries[0].Name() != "credentials.json" {
					t.Errorf("directory holds %s, want credentials.json", entries[0].Name())
				}
			})
		}
	})

	t.Run("should save through a symlink safely", func(t *testing.T) {
		t.Parallel()

		t.Run("should replace the link rather than write to its target", func(t *testing.T) {
			t.Parallel()

			if runtime.GOOS == "windows" {
				t.Skip("windows has no symlink without elevation, so the redirection is not reachable")
			}

			dir := t.TempDir()
			target := filepath.Join(t.TempDir(), "planted.json")
			path := filepath.Join(dir, "credentials.json")

			if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
				t.Fatalf("writing the target: %v", err)
			}

			if err := os.Symlink(target, path); err != nil {
				t.Fatalf("linking: %v", err)
			}

			if _, err := creds.NewStore().Save(path, creds.File{APIKey: "k"}); err != nil {
				t.Fatalf("Save: %v", err)
			}

			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatalf("reading the target: %v", err)
			}

			// Never printed, since the target holds the key when this assertion fails.
			if string(data) != "untouched" {
				t.Error("Save wrote through the symlink, so the key landed outside the config directory")
			}

			info, err := os.Lstat(path)
			if err != nil {
				t.Fatalf("lstat: %v", err)
			}

			if !info.Mode().IsRegular() {
				t.Errorf("credential path is %v, want a regular file", info.Mode().Type())
			}
		})
	})

	t.Run("should warn rather than fail when chmod fails", func(t *testing.T) {
		t.Parallel()

		t.Run("should write the file and warn rather than fail", func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "jev", "credentials.json")
			chmod := func(string, os.FileMode) error {
				return errors.New("this filesystem carries no modes")
			}

			warning, err := creds.NewStore(creds.WithChmod(chmod)).Save(path, creds.File{APIKey: "k"})
			if err != nil {
				t.Fatalf("Save returned the mode failure as an error, want a written file: %v", err)
			}

			if warning == nil {
				t.Fatal("Save returned no warning, want one naming the path")
			}

			if warning.Path != path {
				t.Errorf("warning path = %s, want %s", warning.Path, path)
			}

			if !strings.Contains(warning.Error(), path) {
				t.Errorf("warning = %v, want it to name %s", warning, path)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading back: %v", err)
			}

			if strings.TrimSpace(string(data)) != `{"api_key":"k"}` {
				t.Error("Save warned without writing the file")
			}
		})
	})

	t.Run("should create the temporary file beside the target", func(t *testing.T) {
		t.Parallel()

		// A rename out of os.TempDir crosses a mount and fails with EXDEV, and the key would sit in a
		// world listable directory on the way. The directory passed here is the only guard on that.
		t.Run("should create it in the credential file's own directory", func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "jev", "credentials.json")

			gotDir := ""
			createTemp := func(dir, pattern string) (*os.File, error) {
				gotDir = dir

				return os.CreateTemp(dir, pattern)
			}

			store := creds.NewStore(creds.WithCreateTemp(createTemp))
			if _, err := store.Save(path, creds.File{APIKey: "k"}); err != nil {
				t.Fatalf("Save: %v", err)
			}

			if gotDir != filepath.Dir(path) {
				t.Errorf("temporary file created in %q, want %s", gotDir, filepath.Dir(path))
			}
		})
	})

	t.Run("should fail when the temporary file cannot be written", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			handle  func(t *testing.T, dir string) (*os.File, error)
			wantErr string
		}{
			{
				// A real file opened read only, so the write fails with a real EBADF and the file is
				// still on disk for Save to remove.
				name: "should fail and remove the temporary file when the write fails",
				handle: func(_ *testing.T, dir string) (*os.File, error) {
					return os.OpenFile(filepath.Join(dir, ".credentials-stub"),
						os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0o600)
				},
				wantErr: "writing",
			},
			{
				// A pipe takes the write and refuses the flush, which is the only handle that reaches
				// the flush branch without crashing the machine.
				name: "should fail when the temporary file cannot be flushed",
				handle: func(t *testing.T, _ string) (*os.File, error) {
					read, write, err := os.Pipe()
					if err != nil {
						return nil, err
					}

					// Held open until the test ends, so the write lands in the pipe buffer instead of
					// failing with EPIPE and reaching the wrong branch.
					t.Cleanup(func() { read.Close() })

					return write, nil
				},
				wantErr: "flushing",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				dir := t.TempDir()
				path := filepath.Join(dir, "credentials.json")

				createTemp := func(d, _ string) (*os.File, error) {
					return tc.handle(t, d)
				}

				store := creds.NewStore(creds.WithCreateTemp(createTemp))

				_, err := store.Save(path, creds.File{APIKey: "k"})
				if err == nil {
					t.Fatal("Save succeeded on a handle it could not write, want a failure")
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("Save error = %v, want it to contain %s", err, tc.wantErr)
				}

				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatalf("reading the directory: %v", err)
				}

				if len(entries) != 0 {
					names := make([]string, 0, len(entries))
					for _, entry := range entries {
						names = append(names, entry.Name())
					}

					t.Fatalf("directory holds %v, want it empty", names)
				}
			})
		}
	})
}

func TestClear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		absent  bool
		dir     bool
		symlink bool
		wantErr string
	}{
		{name: "should remove an existing file"},
		{name: "should report an absent file as success", absent: true},
		{
			// The link is what sits at the credential path, so removing it is what Clear was
			// asked to do. A stat by path here would follow it and refuse a link to a directory.
			name:    "should remove a symlink at the credential path",
			symlink: true,
		},
		{
			// os.Remove calls rmdir on a directory, so without a check Clear would delete one it
			// was never asked to touch. Load refuses the same path for the same reason.
			name:    "should refuse a directory at the credential path",
			dir:     true,
			wantErr: "is not a regular file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.symlink && runtime.GOOS == "windows" {
				t.Skip("windows has no symlink without elevation")
			}

			path := filepath.Join(t.TempDir(), "credentials.json")

			if tc.dir {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("creating the fixture directory: %v", err)
				}
			}

			target := ""

			if tc.symlink {
				target = t.TempDir()
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("linking: %v", err)
				}
			}

			if !tc.absent && !tc.dir && !tc.symlink {
				if err := os.WriteFile(path, []byte(`{"api_key":"k"}`), 0o600); err != nil {
					t.Fatalf("writing the fixture: %v", err)
				}
			}

			clearErr := creds.NewStore().Clear(path)

			if tc.wantErr != "" {
				if clearErr == nil || !strings.Contains(clearErr.Error(), tc.wantErr) {
					t.Fatalf("Clear error = %v, want it to contain %s", clearErr, tc.wantErr)
				}

				if _, statErr := os.Lstat(path); statErr != nil {
					t.Errorf("Clear removed the directory it refused, stat = %v", statErr)
				}

				return
			}

			if clearErr != nil {
				t.Fatalf("Clear: %v", clearErr)
			}

			if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("after Clear the path stats as %v, want it gone", statErr)
			}

			if tc.symlink {
				if _, statErr := os.Stat(target); statErr != nil {
					t.Errorf("Clear removed the link's target, stat = %v", statErr)
				}
			}
		})
	}
}
