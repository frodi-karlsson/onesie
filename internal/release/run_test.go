package release_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/release"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		stdin      string
		inTree     bool
		wantCode   int
		wantStdout string
		wantStderr string
		wantBumped bool
	}{
		{
			name:     "should accept a tag above every existing name on stdin",
			args:     []string{"check-tag", "v0.2.0"},
			stdin:    "v0.1.0\nabc\trefs/tags/v0.1.0^{}\n",
			wantCode: 0,
		},
		{
			name:       "should refuse a tag that is not above the latest, with the reason on stderr",
			args:       []string{"check-tag", "v0.1.0"},
			stdin:      "v0.1.0\n",
			wantCode:   1,
			wantStderr: "latest tag is v0.1.0",
		},
		{
			name:       "should print each change it makes",
			args:       []string{"bump", "v0.2.0"},
			inTree:     true,
			wantCode:   0,
			wantStdout: "set .agents/plugins/marketplace.json ref from v0.1.0 to v0.2.0\n",
			wantBumped: true,
		},
		{
			name:       "should print each change it would make under -dry-run",
			args:       []string{"bump", "-dry-run", "v0.2.0"},
			inTree:     true,
			wantCode:   0,
			wantStdout: "would set .claude-plugin/plugin.json version from 0.1.0 to 0.2.0\n",
		},
		{
			name:       "should fail a bump with no manifests",
			args:       []string{"bump", "v0.2.0"},
			wantCode:   1,
			wantStderr: "releaseprep:",
		},
		{name: "should call a prerelease a prerelease", args: []string{"prerelease", "v0.2.0-rc.1"}, wantCode: 0},
		{name: "should not call a release a prerelease", args: []string{"prerelease", "v0.2.0"}, wantCode: 1},
		{name: "should print usage with no subcommand", args: nil, wantCode: 2, wantStderr: "usage: releaseprep"},
		{name: "should print usage for an unknown subcommand", args: []string{"verify", "v0.2.0"}, wantCode: 2, wantStderr: "usage: releaseprep"},
		{name: "should print usage for a missing tag", args: []string{"bump"}, wantCode: 2, wantStderr: "usage: releaseprep"},
		{name: "should print usage for an unknown flag", args: []string{"bump", "-force", "v0.2.0"}, wantCode: 2, wantStderr: "usage: releaseprep"},
		{name: "should print usage for a malformed tag to check", args: []string{"check-tag", "0.2.0"}, wantCode: 2, wantStderr: "usage: releaseprep"},
		{name: "should print usage for a malformed tag to bump", args: []string{"bump", "v0.2"}, wantCode: 2, wantStderr: "vMAJOR.MINOR.PATCH"},
		{name: "should print usage for a malformed prerelease", args: []string{"prerelease", "v0.2.0+b"}, wantCode: 2, wantStderr: "usage: releaseprep"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.inTree {
				writeManifests(t, dir, "0.1.0")
			}

			t.Chdir(dir)

			var stdout, stderr bytes.Buffer

			code := release.Run(tc.args, strings.NewReader(tc.stdin), &stdout, &stderr)

			if code != tc.wantCode {
				t.Errorf("exit = %d, want %d, stderr: %s", code, tc.wantCode, stderr.String())
			}

			if !strings.Contains(stdout.String(), tc.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), tc.wantStdout)
			}

			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}

			if !tc.inTree {
				return
			}

			want := "0.1.0"
			if tc.wantBumped {
				want = "0.2.0"
			}

			for name, content := range manifests(want) {
				got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
				if err != nil {
					t.Fatal(err)
				}

				if string(got) != content {
					t.Errorf("%s =\n%s\nwant\n%s", name, got, content)
				}
			}
		})
	}
}

func writeManifests(t *testing.T, dir, version string) {
	t.Helper()

	for name, content := range manifests(version) {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
