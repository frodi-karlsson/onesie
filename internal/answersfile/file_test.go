package answersfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestResolveTarget(t *testing.T) {
	t.Parallel()

	failed := errors.New("input/output error")

	tests := []struct {
		name    string
		file    bool
		links   map[string]string
		path    string
		resolve func(path string) (string, error)
		want    string
		wantErr error
	}{
		{
			name: "should keep a path with no file at it",
			path: "answers.jsonl",
			want: "answers.jsonl",
		},
		{
			name:  "should follow a link to a file that exists",
			file:  true,
			links: map[string]string{"link.jsonl": "answers.jsonl"},
			path:  "link.jsonl",
			want:  "answers.jsonl",
		},
		{
			name:  "should follow a chain of links to a file not written yet",
			links: map[string]string{"link.jsonl": "middle.jsonl", "middle.jsonl": "answers.jsonl"},
			path:  "link.jsonl",
			want:  "answers.jsonl",
		},
		{
			name:  "should refuse a loop of links",
			links: map[string]string{"link.jsonl": "other.jsonl", "other.jsonl": "link.jsonl"},
			path:  "link.jsonl",
			resolve: func(string) (string, error) {
				return "", fs.ErrNotExist
			},
			wantErr: errLinkLoop,
		},
		{
			name:    "should return a failure to resolve the path",
			path:    "answers.jsonl",
			resolve: func(string) (string, error) { return "", failed },
			wantErr: failed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if len(tc.links) > 0 && runtime.GOOS == "windows" {
				t.Skip("creating a symlink on windows needs a privilege the test may not have")
			}

			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatalf("resolving the directory: %v", err)
			}

			if tc.file {
				if writeErr := os.WriteFile(filepath.Join(dir, "answers.jsonl"), nil, 0o600); writeErr != nil {
					t.Fatalf("writing the answers file: %v", writeErr)
				}
			}

			for link, target := range tc.links {
				if linkErr := os.Symlink(target, filepath.Join(dir, link)); linkErr != nil {
					t.Fatalf("linking: %v", linkErr)
				}
			}

			resolve := tc.resolve
			if resolve == nil {
				resolve = filepath.EvalSymlinks
			}

			got, err := resolveTarget(filepath.Join(dir, tc.path), resolve, os.Readlink)
			if !errors.Is(err, tc.wantErr) || (tc.wantErr == nil) != (err == nil) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}

			if err != nil {
				return
			}

			if want := filepath.Join(dir, tc.want); filepath.Clean(got) != want {
				t.Errorf("target = %q, want %q", got, want)
			}
		})
	}
}

func TestFile_Lock(t *testing.T) {
	t.Parallel()

	refused := errors.New("operation not supported")

	tests := []struct {
		name       string
		resume     bool
		lockErr    error
		wantErr    string
		wantLocked bool
	}{
		{
			name:       "should hold the lock on a fresh run",
			wantLocked: true,
		},
		{
			name:    "should refuse a fresh run while a resume holds the file",
			lockErr: errLocked,
			wantErr: "is in use by another onesie run",
		},
		{
			name:    "should write a fresh run unguarded when no lock can be taken beside the file",
			lockErr: refused,
		},
		{
			name:    "should refuse a resume when no lock can be taken beside the file",
			resume:  true,
			lockErr: refused,
			wantErr: "operation not supported",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var took string

			out := &File{path: "link.jsonl", target: "answers.jsonl"}

			err := out.lock(func(answers string) (func() error, error) {
				took = answers
				if tc.lockErr != nil {
					return nil, tc.lockErr
				}

				return func() error { return nil }, nil
			}, tc.resume)
			if !strings.Contains(fmt.Sprint(err), tc.wantErr) || (tc.wantErr == "") != (err == nil) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}

			if took != "answers.jsonl" {
				t.Errorf("locked %q, want the resolved target", took)
			}

			if (out.unlock != nil) != tc.wantLocked {
				t.Errorf("holding the lock = %v, want %v", out.unlock != nil, tc.wantLocked)
			}
		})
	}
}

func TestFile_Write(t *testing.T) {
	t.Parallel()

	failedRename := errors.New("disk full")

	tests := []struct {
		name        string
		resume      bool
		bind        bool
		rename      func(string, string) error
		wantErr     string
		wantFile    string
		wantSidecar string
		wantRenames []string
	}{
		{
			name:        "should write the fingerprint through a temporary file and a rename",
			bind:        true,
			wantFile:    "answer\n",
			wantSidecar: "v1:" + strings.Repeat("a", 64) + "\n",
			wantRenames: []string{"answers.jsonl.onesie.tmp answers.jsonl.onesie"},
		},
		{
			name:        "should refuse a resume that was never bound",
			resume:      true,
			wantErr:     "was never checked against its fingerprint",
			wantFile:    "keep me\n",
			wantSidecar: "keep this\n",
		},
		{
			name:        "should let go of the file when the fingerprint cannot be written",
			bind:        true,
			rename:      func(string, string) error { return failedRename },
			wantErr:     "disk full",
			wantFile:    "",
			wantSidecar: "keep this\n",
			wantRenames: []string{
				"answers.jsonl.onesie.tmp answers.jsonl.onesie",
				"answers.jsonl.onesie.tmp answers.jsonl.onesie",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, "answers.jsonl")

			if err := os.WriteFile(path, []byte("keep me\n"), 0o600); err != nil {
				t.Fatalf("writing the existing file: %v", err)
			}

			if err := os.WriteFile(path+".onesie", []byte("keep this\n"), 0o600); err != nil {
				t.Fatalf("writing the existing fingerprint: %v", err)
			}

			rename := tc.rename
			if rename == nil {
				rename = os.Rename
			}

			var renames []string

			out := &File{
				path:   path,
				target: path,
				open:   os.OpenFile,
				remove: os.Remove,
				rename: func(from, to string) error {
					renames = append(renames, filepath.Base(from)+" "+filepath.Base(to))

					return rename(from, to)
				},
				resume: tc.resume,
				keep:   int64(len("keep me\n")),
			}

			if tc.bind {
				if err := out.bindFingerprint("v1:" + strings.Repeat("a", 64)); err != nil {
					t.Fatalf("bind: %v", err)
				}
			}

			_, writeErr := io.WriteString(out, "answer\n")
			if writeErr == nil {
				writeErr = out.Finish(nil)
			}

			if !strings.Contains(fmt.Sprint(writeErr), tc.wantErr) || (tc.wantErr == "") != (writeErr == nil) {
				t.Fatalf("error = %v, want %q", writeErr, tc.wantErr)
			}

			if writeErr != nil {
				if out.file != nil {
					t.Errorf("file = %v, want it let go after the failure", out.file)
				}

				_, againErr := io.WriteString(out, "again\n")
				if againErr == nil || errors.Is(againErr, os.ErrClosed) {
					t.Errorf("second write error = %v, want the first failure again", againErr)
				}

				if err := out.Finish(writeErr); err != nil {
					t.Errorf("finish after the failure: %v", err)
				}
			}

			assertFileHolds(t, path, tc.wantFile)
			assertFileHolds(t, path+".onesie", tc.wantSidecar)

			if _, err := os.Stat(path + ".onesie.tmp"); !os.IsNotExist(err) {
				t.Errorf("temporary fingerprint left behind: %v", err)
			}

			if strings.Join(renames, ",") != strings.Join(tc.wantRenames, ",") {
				t.Errorf("renames = %q, want %q", renames, tc.wantRenames)
			}
		})
	}
}

func TestFile_Finish(t *testing.T) {
	t.Parallel()

	const appended = "{\"id\":2,\"answer\":0.5}\n{\"id\":1,\"answer\":0.5}\n"

	const compacted = "{\"id\":1,\"answer\":0.5}\n{\"id\":2,\"answer\":0.5}\n"

	failed := errors.New("disk full")

	tests := []struct {
		name        string
		rename      func(string, string) error
		open        func(string, int, os.FileMode) (*os.File, error)
		link        bool
		goos        string
		wantErr     string
		wantFile    string
		wantDirSync bool
	}{
		{
			name:        "should rewrite the file in input order through a rename",
			wantFile:    compacted,
			wantDirSync: true,
		},
		{
			name: "should leave the uncompacted file when the rename fails",
			rename: func(from, to string) error {
				if strings.HasSuffix(from, compactSuffix) {
					return failed
				}

				return os.Rename(from, to)
			},
			wantErr:  "disk full",
			wantFile: appended,
		},
		{
			name: "should leave the uncompacted file when the temporary file cannot be opened",
			open: func(name string, flag int, perm os.FileMode) (*os.File, error) {
				if strings.HasSuffix(name, compactSuffix) {
					return nil, failed
				}

				return os.OpenFile(name, flag, perm)
			},
			wantErr:  "disk full",
			wantFile: appended,
		},
		{
			name:        "should write through a symlink at the path and leave the link in place",
			link:        true,
			wantFile:    compacted,
			wantDirSync: true,
		},
		{
			name:     "should not flush the directory on windows, which cannot",
			goos:     "windows",
			wantFile: compacted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.link && runtime.GOOS == "windows" {
				t.Skip("creating a symlink on windows needs a privilege the test may not have")
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "answers.jsonl")
			target := path

			if tc.link {
				target = filepath.Join(dir, "target.jsonl")
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("linking: %v", err)
				}
			}

			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatalf("writing the existing file: %v", err)
			}

			rename := tc.rename
			if rename == nil {
				rename = os.Rename
			}

			open := tc.open
			if open == nil {
				open = os.OpenFile
			}

			goos := tc.goos
			if goos == "" {
				goos = "linux"
			}

			resolvedDir, resolveErr := filepath.EvalSymlinks(dir)
			if resolveErr != nil {
				t.Fatalf("resolving the directory: %v", resolveErr)
			}

			var dirSynced atomic.Bool

			out := &File{
				path: path,
				open: func(name string, flag int, perm os.FileMode) (*os.File, error) {
					if name == resolvedDir {
						dirSynced.Store(true)
					}

					return open(name, flag, perm)
				},
				target: filepath.Join(resolvedDir, filepath.Base(target)),
				rename: rename,
				remove: os.Remove,
				goos:   goos,
				resume: true,
			}
			if err := out.bindFingerprint("v1:" + strings.Repeat("a", 64)); err != nil {
				t.Fatalf("bind: %v", err)
			}

			if _, err := io.WriteString(out, appended); err != nil {
				t.Fatalf("write: %v", err)
			}

			half := int64(len(appended) / 2)
			out.CompactInto([]Span{{Start: half, End: 2 * half}, {Start: 0, End: half}}, output.Values)

			err := out.Finish(nil)
			if !strings.Contains(fmt.Sprint(err), tc.wantErr) || (tc.wantErr == "") != (err == nil) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}

			assertFileHolds(t, target, tc.wantFile)

			if runtime.GOOS != "windows" {
				assertMode(t, target, 0o600)
			}

			if info, statErr := os.Lstat(path); statErr != nil || (info.Mode()&os.ModeSymlink != 0) != tc.link {
				t.Errorf("path = %v, %v, want a symlink %v", info, statErr, tc.link)
			}

			for _, beside := range []string{path, target} {
				if _, statErr := os.Stat(beside + compactSuffix); !os.IsNotExist(statErr) {
					t.Errorf("temporary compaction file left behind: %v", statErr)
				}
			}

			if dirSynced.Load() != tc.wantDirSync {
				t.Errorf("directory flushed = %v, want %v", dirSynced.Load(), tc.wantDirSync)
			}
		})
	}
}

func TestSpecialFile(t *testing.T) {
	t.Parallel()

	regular := func(string) (fs.FileInfo, error) { return os.Stat(os.Args[0]) }
	missing := func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	directory := func(string) (fs.FileInfo, error) { return os.Stat(os.TempDir()) }

	tests := []struct {
		name    string
		target  string
		goos    string
		stat    func(string) (fs.FileInfo, error)
		want    bool
		wantErr bool
	}{
		{name: "should treat a regular file as regular", target: "answers.jsonl", goos: "linux", stat: regular},
		{name: "should treat a file not written yet as regular", target: "answers.jsonl", goos: "linux", stat: missing},
		{
			name:   "should treat a descriptor under /dev as special when it stats as a regular file",
			target: "/dev/fd/1", goos: "darwin", stat: regular, want: true,
		},
		{name: "should refuse a directory", target: "out", goos: "linux", stat: directory, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := specialFile(tc.target, tc.goos, tc.stat)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want an error %v", err, tc.wantErr)
			}

			if got != tc.want {
				t.Errorf("special = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFile_CheckFingerprint(t *testing.T) {
	t.Parallel()

	current := "v2:" + strings.Repeat("ab", 32)

	tests := []struct {
		name     string
		sidecar  *string
		checking string
		wantErr  string
	}{
		{name: "should refuse a file with no sidecar", checking: current, wantErr: "has no fingerprint beside it"},
		{name: "should refuse a sidecar onesie did not write", sidecar: new("hello"), checking: current, wantErr: "does not hold a fingerprint onesie wrote"},
		{name: "should refuse an older version", sidecar: new("v1:abc"), checking: current, wantErr: "an older onesie wrote"},
		{name: "should refuse a newer version", sidecar: new("v3:abc"), checking: current, wantErr: "a newer onesie wrote"},
		{name: "should refuse a changed fingerprint", sidecar: new(current), checking: "v2:" + strings.Repeat("cd", 32), wantErr: "changed since"},
		{name: "should accept the same fingerprint", sidecar: new(current), checking: current},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "answers.jsonl")
			if err := os.WriteFile(path, []byte("{\"id\":\"a\"}\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			if tc.sidecar != nil {
				if err := os.WriteFile(path+fingerprintSuffix, []byte(*tc.sidecar+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			out := &File{path: path, target: path, open: os.OpenFile}

			matched, err := out.checkFingerprint(tc.checking)
			if tc.wantErr == "" {
				if err != nil || !matched {
					t.Fatalf("checkFingerprint = %v, %v, want a match", matched, err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !errors.Is(err, ErrStaleAnswers) {
				t.Fatalf("checkFingerprint error = %v, want one containing %q that is ErrStaleAnswers", err, tc.wantErr)
			}

			if strings.Contains(err.Error(), ErrStaleAnswers.Error()) {
				t.Errorf("error = %q, want today's wording without the sentinel's text", err)
			}
		})
	}
}

func assertFileHolds(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", filepath.Base(path), err)
	}

	if string(data) != want {
		t.Errorf("%s = %q, want %q", filepath.Base(path), data, want)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the mode of %s: %v", filepath.Base(path), err)
	}

	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", filepath.Base(path), got, want)
	}
}
