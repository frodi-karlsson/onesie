package creds_test

import (
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

			got, found, err := creds.NewStore().Load(path)

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
