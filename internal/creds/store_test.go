package creds_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/creds"
)

func TestLoad(t *testing.T) {
	t.Parallel()

	// unixOnly marks the cases that turn on permission bits. Windows carries none, so the mode
	// check is scoped out there and the fixture could not be written in the first place.
	tests := []struct {
		name     string
		contents string
		mode     os.FileMode
		absent   bool
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode check does not apply")
			}

			path := filepath.Join(t.TempDir(), "credentials.json")

			if !tc.absent {
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
