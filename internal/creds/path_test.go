package creds_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/creds"
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
	}{
		{
			name: "should prefer JEV_CONFIG_DIR",
			env: map[string]string{
				"JEV_CONFIG_DIR":  "/cfg",
				"XDG_CONFIG_HOME": "/xdg",
			},
			goos: "linux",
			home: "/home/x",
			want: []string{"/cfg", "credentials.json"},
		},
		{
			name: "should fall back to XDG_CONFIG_HOME",
			env:  map[string]string{"XDG_CONFIG_HOME": "/xdg"},
			goos: "linux",
			home: "/home/x",
			want: []string{"/xdg", "jev", "credentials.json"},
		},
		{
			name: "should prefer XDG_CONFIG_HOME over APPDATA on windows",
			env: map[string]string{
				"XDG_CONFIG_HOME": "/xdg",
				"APPDATA":         `C:\Users\x\AppData\Roaming`,
			},
			goos: "windows",
			home: `C:\Users\x`,
			want: []string{"/xdg", "jev", "credentials.json"},
		},
		{
			name: "should use APPDATA on windows",
			env:  map[string]string{"APPDATA": `C:\Users\x\AppData\Roaming`},
			goos: "windows",
			home: `C:\Users\x`,
			want: []string{`C:\Users\x\AppData\Roaming`, "jev", "credentials.json"},
		},
		{
			name: "should ignore APPDATA off windows",
			env:  map[string]string{"APPDATA": "/roaming"},
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".config", "jev", "credentials.json"},
		},
		{
			name: "should fall back to the home directory",
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".config", "jev", "credentials.json"},
		},
		{
			name: "should ignore an empty variable",
			env:  map[string]string{"JEV_CONFIG_DIR": "", "XDG_CONFIG_HOME": ""},
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".config", "jev", "credentials.json"},
		},
		{
			name:    "should fail when the home lookup fails",
			goos:    "linux",
			homeErr: errNoHome,
			wantErr: errNoHome.Error(),
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
