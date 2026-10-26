package creds_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestStoreLoad(t *testing.T) {
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
			contents: `{"providers":{"typesafe":{"api_key":"k","base_url":"https://proxy.example"}}}`,
			mode:     0o600,
			want:     creds.File{Providers: map[string]creds.Entry{"typesafe": {APIKey: "k", BaseURL: "https://proxy.example"}}},
		},
		{
			name:     "should read a key with no base url",
			contents: `{"providers":{"typesafe":{"api_key":"k"}}}`,
			mode:     0o600,
			want:     creds.File{Providers: map[string]creds.Entry{"typesafe": {APIKey: "k"}}},
		},
		{
			name:   "should report an absent file as absent rather than an error",
			absent: true,
		},
		{
			name:     "should refuse a file readable by the group",
			contents: `{"providers":{"typesafe":{"api_key":"k"}}}`,
			mode:     0o640,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 640",
		},
		{
			name:     "should refuse a file readable by the world",
			contents: `{"providers":{"typesafe":{"api_key":"k"}}}`,
			mode:     0o604,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 604",
		},
		{
			name:     "should refuse a file the group can write but not read",
			contents: `{"providers":{"typesafe":{"api_key":"k"}}}`,
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
			contents: `{"providers":{"typesafe":{"api_key":"k"}}}`,
			mode:     0o060,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 60",
		},
		{
			name:     "should refuse a file only the world can read",
			contents: `{"providers":{"typesafe":{"api_key":"k"}}}`,
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
			contents: `{"providers":{"typesafe":{"api_key":"` + strings.Repeat("k", (1<<20)-41) + `"}}}`,
			mode:     0o600,
			want:     creds.File{Providers: map[string]creds.Entry{"typesafe": {APIKey: strings.Repeat("k", (1<<20)-41)}}},
		},
		{
			name:     "should refuse a file larger than the read cap",
			contents: `{"providers":{"typesafe":{"api_key":"` + strings.Repeat("k", 1<<20) + `"}}}`,
			mode:     0o600,
			wantErr:  "is larger than 1048576 bytes",
		},
		{
			name:     "should read two providers",
			contents: `{"providers":{"openrouter":{"api_key":"o"},"typesafe":{"api_key":"k"}}}`,
			mode:     0o600,
			want: creds.File{Providers: map[string]creds.Entry{
				"openrouter": {APIKey: "o"},
				"typesafe":   {APIKey: "k"},
			}},
		},
		{
			name:     "should read an entry whose key is in the keychain",
			contents: `{"providers":{"typesafe":{"store":"keychain","base_url":"https://proxy.example"}}}`,
			mode:     0o600,
			want: creds.File{Providers: map[string]creds.Entry{
				"typesafe": {Store: creds.StoreKeychain, BaseURL: "https://proxy.example"},
			}},
		},
		{
			name:     "should refuse an entry with both a key and a keychain store",
			contents: `{"providers":{"typesafe":{"api_key":"k","store":"keychain"}}}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse an entry with an unknown store",
			contents: `{"providers":{"typesafe":{"store":"vault"}}}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a file with no providers",
			contents: `{}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a file with an empty providers map",
			contents: `{"providers":{}}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse the flat shape",
			contents: `{"api_key":"k"}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a file where one of two entries has an empty api_key",
			contents: `{"providers":{"openrouter":{"api_key":""},"typesafe":{"api_key":"k"}}}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse an entry whose api_key is empty",
			contents: `{"providers":{"typesafe":{"api_key":""}}}`,
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
			contents: `{"providers":{"typesafe":{"api_key":42}}}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a file whose base_url is not a string",
			contents: `{"providers":{"typesafe":{"api_key":"k","base_url":42}}}`,
			mode:     0o600,
			wantErr:  "is not a JSON object",
		},
		{
			name:     "should refuse a group readable file on a unix filesystem",
			contents: `{"providers":{"typesafe":{"api_key":"k"}}}`,
			goos:     "linux",
			mode:     0o640,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 640",
		},
		{
			name:     "should accept a group readable file on windows, where the mode carries no meaning",
			contents: `{"providers":{"typesafe":{"api_key":"k"}}}`,
			goos:     "windows",
			mode:     0o640,
			want:     creds.File{Providers: map[string]creds.Entry{"typesafe": {APIKey: "k"}}},
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

				if got.Providers != nil {
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

			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Load = %+v, want %+v", got, tc.want)
			}
		})
	}

	t.Run("should report what the filesystem refuses", func(t *testing.T) {
		t.Parallel()

		denied := &fs.PathError{Op: "open", Path: "credentials.json", Err: fs.ErrPermission}
		broken := errors.New("stub open failure")

		tests := []struct {
			name     string
			openErr  error
			stat     func(string) (fs.FileInfo, error)
			wantMode os.FileMode
			wantErr  error
		}{
			{
				name:    "should wrap an open failure that is not a missing file",
				openErr: broken,
				wantErr: broken,
			},
			{
				// A test running as root can open a file of any mode, so only a stubbed open
				// reaches this branch everywhere.
				name:     "should name the mode of a file its owner cannot open",
				openErr:  denied,
				stat:     func(string) (fs.FileInfo, error) { return modeInfo(0o644), nil },
				wantMode: 0o644,
			},
			{
				name:    "should report the open failure when the mode cannot be read either",
				openErr: denied,
				stat: func(string) (fs.FileInfo, error) {
					return nil, errors.New("stub stat failure")
				},
				wantErr: fs.ErrPermission,
			},
			{
				name:    "should report the open failure when the mode is private",
				openErr: denied,
				stat:    func(string) (fs.FileInfo, error) { return modeInfo(0o600), nil },
				wantErr: fs.ErrPermission,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				opts := []creds.StoreOption{
					creds.WithGOOS("linux"),
					creds.WithOpen(func(string) (*os.File, error) { return nil, tc.openErr }),
				}
				if tc.stat != nil {
					opts = append(opts, creds.WithStat(tc.stat))
				}

				_, found, err := creds.NewStore(opts...).Load("credentials.json")
				if found {
					t.Errorf("found = true, want false alongside an error")
				}

				if tc.wantMode != 0 {
					var readable *creds.ReadableError
					if !errors.As(err, &readable) {
						t.Fatalf("Load error = %v, want a ReadableError", err)
					}

					if readable.Mode != tc.wantMode {
						t.Errorf("mode = %o, want %o", readable.Mode, tc.wantMode)
					}

					return
				}

				if !errors.Is(err, tc.wantErr) {
					t.Errorf("Load error = %v, want it to wrap %v", err, tc.wantErr)
				}

				if !strings.Contains(err.Error(), "reading credentials.json") {
					t.Errorf("Load error = %v, want it to name the path", err)
				}
			})
		}
	})

	t.Run("should name the target's mode through a symlink", func(t *testing.T) {
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

				if err := os.WriteFile(target, []byte(`{"providers":{"typesafe":{"api_key":"k"}}}`), 0o600); err != nil {
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
	})
}

type modeInfo os.FileMode

func (m modeInfo) Name() string       { return "credentials.json" }
func (m modeInfo) Size() int64        { return 0 }
func (m modeInfo) Mode() os.FileMode  { return os.FileMode(m) }
func (m modeInfo) ModTime() time.Time { return time.Time{} }
func (m modeInfo) IsDir() bool        { return os.FileMode(m).IsDir() }
func (m modeInfo) Sys() any           { return nil }
