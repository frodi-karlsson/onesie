package creds_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestPath(t *testing.T) {
	t.Parallel()

	errNoHome := errors.New("the test home lookup failed")

	// want holds path segments rather than a joined string. filepath.Join uses the host's
	// separator, not the one tc.goos implies, so a windows expectation spelled with backslashes
	// would never match on linux.
	tests := []struct {
		name    string
		env     map[string]string
		goos    string
		home    string
		homeErr error
		want    []string
		wantErr string
		// wantIs pins the %w wrapping. A %v would still carry the text and lose the chain.
		wantIs error
	}{
		{
			name: "should prefer ONESIE_CONFIG_DIR",
			env: map[string]string{
				"ONESIE_CONFIG_DIR": "/cfg",
				"XDG_CONFIG_HOME":   "/xdg",
			},
			goos: "linux",
			home: "/home/x",
			want: []string{"/cfg", "credentials.json"},
		},
		{
			// A container with no HOME makes os.UserHomeDir fail, and setting ONESIE_CONFIG_DIR is
			// how a user copes with that. Resolving the home directory before the variables were
			// consulted would break every variable rule at once on exactly those machines.
			name:    "should not consult the home directory when ONESIE_CONFIG_DIR is set",
			env:     map[string]string{"ONESIE_CONFIG_DIR": "/cfg"},
			goos:    "linux",
			homeErr: errNoHome,
			want:    []string{"/cfg", "credentials.json"},
		},
		{
			name:    "should not consult the home directory when XDG_CONFIG_HOME is set",
			env:     map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			goos:    "linux",
			homeErr: errNoHome,
			want:    []string{"/xdg", "onesie", "credentials.json"},
		},
		{
			name:    "should not consult the home directory when APPDATA is set on windows",
			env:     map[string]string{"APPDATA": "/roaming"},
			goos:    "windows",
			homeErr: errNoHome,
			want:    []string{"/roaming", "onesie", "credentials.json"},
		},
		{
			name: "should fall back to XDG_CONFIG_HOME",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			goos: "linux",
			home: "/home/x",
			want: []string{"/xdg", "onesie", "credentials.json"},
		},
		{
			name: "should prefer XDG_CONFIG_HOME over APPDATA on windows",
			env: map[string]string{
				"XDG_CONFIG_HOME": "/xdg",
				"APPDATA":         `C:\Users\x\AppData\Roaming`,
			},
			goos: "windows",
			home: `C:\Users\x`,
			want: []string{"/xdg", "onesie", "credentials.json"},
		},
		{
			name: "should use APPDATA on windows",
			env:  map[string]string{"APPDATA": `C:\Users\x\AppData\Roaming`},
			goos: "windows",
			home: `C:\Users\x`,
			want: []string{`C:\Users\x\AppData\Roaming`, "onesie", "credentials.json"},
		},
		{
			name: "should ignore APPDATA off windows",
			env:  map[string]string{"APPDATA": "/roaming"},
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".config", "onesie", "credentials.json"},
		},
		{
			name: "should fall back to the home directory",
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".config", "onesie", "credentials.json"},
		},
		{
			name: "should ignore an empty variable",
			env:  map[string]string{"ONESIE_CONFIG_DIR": "", "XDG_CONFIG_HOME": ""},
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".config", "onesie", "credentials.json"},
		},
		{
			name:    "should fail when the home lookup fails",
			goos:    "linux",
			homeErr: errNoHome,
			wantErr: errNoHome.Error(),
			wantIs:  errNoHome,
		},
		{
			name:    "should fail rather than resolve under an empty home directory",
			goos:    "linux",
			home:    "",
			wantErr: "cannot find a home directory",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lookup := func(name string) (string, bool) {
				value, ok := tc.env[name]

				return value, ok
			}

			got, err := creds.Path(creds.Env{
				Lookup: lookup,
				GOOS:   tc.goos,
				Home:   func() (string, error) { return tc.home, tc.homeErr },
			})

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Path = %s, want an error containing %s", got, tc.wantErr)
				}

				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Path error = %v, want it to contain %s", err, tc.wantErr)
				}

				// Every user facing error in this repo opens with the prefix, and a consumer
				// filtering stderr should not have to know which package produced a line.
				if !strings.HasPrefix(err.Error(), "onesie: ") {
					t.Errorf("Path error = %v, want it to start with the onesie prefix", err)
				}

				if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
					t.Errorf("Path error = %v, want it to wrap %v", err, tc.wantIs)
				}

				if got != "" {
					t.Errorf("Path = %s, want an empty path beside the error", got)
				}

				return
			}

			if err != nil {
				t.Fatalf("Path: %v", err)
			}

			if want := filepath.Join(tc.want...); got != want {
				t.Errorf("Path = %s, want %s", got, want)
			}
		})
	}
}

func TestDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		goos    string
		home    string
		want    []string
		wantErr string
	}{
		{
			name: "should prefer ONESIE_CONFIG_DIR",
			env:  map[string]string{"ONESIE_CONFIG_DIR": "/cfg", "XDG_CONFIG_HOME": "/xdg"},
			goos: "linux",
			home: "/home/x",
			want: []string{"/cfg"},
		},
		{
			name: "should fall back to XDG_CONFIG_HOME",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			goos: "linux",
			home: "/home/x",
			want: []string{"/xdg", "onesie"},
		},
		{
			name: "should use APPDATA on windows",
			env:  map[string]string{"APPDATA": "/roaming"},
			goos: "windows",
			home: "/home/x",
			want: []string{"/roaming", "onesie"},
		},
		{
			name: "should fall back to the home directory",
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".config", "onesie"},
		},
		{
			name:    "should name the config dir when no home directory is found",
			goos:    "linux",
			wantErr: "onesie: cannot find a home directory for the config dir, where the credential file lives",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := creds.Dir(creds.Env{
				Lookup: func(name string) (string, bool) {
					value, ok := tc.env[name]

					return value, ok
				},
				GOOS: tc.goos,
				Home: func() (string, error) { return tc.home, nil },
			})

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Dir = %s, %v, want an error containing %s", got, err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Dir: %v", err)
			}

			if want := filepath.Join(tc.want...); got != want {
				t.Errorf("Dir = %s, want %s", got, want)
			}
		})
	}
}
