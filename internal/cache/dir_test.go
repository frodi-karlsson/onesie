package cache_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/cache"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestDir(t *testing.T) {
	t.Parallel()

	errNoHome := errors.New("the test home lookup failed")

	// want holds path segments, since filepath.Join uses the host's separator and not the one goos
	// implies.
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
			name: "should prefer ONESIE_CACHE_DIR, cleaned",
			env:  map[string]string{"ONESIE_CACHE_DIR": "/c/sub/../", "XDG_CACHE_HOME": "/xdg"},
			goos: "darwin",
			home: "/home/x",
			want: []string{"/c"},
		},
		{
			name:    "should not consult the home directory when ONESIE_CACHE_DIR is set",
			env:     map[string]string{"ONESIE_CACHE_DIR": "/c"},
			goos:    "linux",
			homeErr: errNoHome,
			want:    []string{"/c"},
		},
		{
			name: "should fall back to XDG_CACHE_HOME",
			env:  map[string]string{"XDG_CACHE_HOME": "/xdg", "LOCALAPPDATA": "/local"},
			goos: "windows",
			home: "/home/x",
			want: []string{"/xdg", "onesie"},
		},
		{
			name: "should use Library/Caches on darwin",
			goos: "darwin",
			home: "/Users/x",
			want: []string{"/Users/x", "Library", "Caches", "onesie"},
		},
		{
			name: "should use .cache on linux",
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".cache", "onesie"},
		},
		{
			name: "should use .cache on freebsd",
			goos: "freebsd",
			home: "/home/x",
			want: []string{"/home/x", ".cache", "onesie"},
		},
		{
			name: "should use LOCALAPPDATA on windows",
			env:  map[string]string{"LOCALAPPDATA": `C:\Users\x\AppData\Local`},
			goos: "windows",
			home: `C:\Users\x`,
			want: []string{`C:\Users\x\AppData\Local`, "onesie"},
		},
		{
			name: "should use AppData Local under the home directory on windows with no LOCALAPPDATA",
			goos: "windows",
			home: `C:\Users\x`,
			want: []string{`C:\Users\x`, "AppData", "Local", "onesie"},
		},
		{
			name: "should ignore LOCALAPPDATA off windows",
			env:  map[string]string{"LOCALAPPDATA": "/local"},
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".cache", "onesie"},
		},
		{
			name: "should ignore an empty variable",
			env:  map[string]string{"ONESIE_CACHE_DIR": "", "XDG_CACHE_HOME": ""},
			goos: "linux",
			home: "/home/x",
			want: []string{"/home/x", ".cache", "onesie"},
		},
		{
			name:    "should name the cache dir when the home lookup fails",
			goos:    "linux",
			homeErr: errNoHome,
			wantErr: "cache dir",
		},
		{
			name:    "should name the cache dir when there is no home directory",
			goos:    "darwin",
			wantErr: "cannot find a home directory for the cache dir",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cache.Dir(creds.Env{
				Lookup: func(name string) (string, bool) {
					value, ok := tc.env[name]

					return value, ok
				},
				GOOS: tc.goos,
				Home: func() (string, error) { return tc.home, tc.homeErr },
			})

			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.HasPrefix(err.Error(), "onesie: ") {
					t.Fatalf("Dir = %q, %v, want an onesie error containing %q", got, err, tc.wantErr)
				}

				if tc.homeErr != nil && !errors.Is(err, tc.homeErr) {
					t.Errorf("Dir error = %v, want it to wrap %v", err, tc.homeErr)
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
