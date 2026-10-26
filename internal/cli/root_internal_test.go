package cli

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSchemaURLOf(t *testing.T) {
	t.Parallel()

	const base = "https://raw.githubusercontent.com/frodi-karlsson/onesie/"

	tests := []struct {
		name string
		tag  string
		want string
	}{
		{name: "should name the tag the binary was built from", tag: "v0.2.0", want: base + "v0.2.0/schema/questions.json"},
		{name: "should name a prerelease tag", tag: "v0.2.0-rc.1", want: base + "v0.2.0-rc.1/schema/questions.json"},
		{name: "should name main for a build with no tag", tag: "", want: base + "main/schema/questions.json"},
		{name: "should name main for a dev build", tag: "dev", want: base + "main/schema/questions.json"},
		{name: "should name main for a build past a tag", tag: "v0.1.0-3-gabc1234", want: base + "main/schema/questions.json"},
		{name: "should name main for a tag with no v", tag: "0.2.0", want: base + "main/schema/questions.json"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := schemaURLOf(BuildInfo{Version: "1.2.3", Tag: tc.tag}); got != tc.want {
				t.Errorf("schemaURLOf = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWithTerminalWidth(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"onesie-1.13.0","answers":{"team":{"type":"choice",` +
		`"choice":"x","confidence":1,"probabilities":{"x":1,"y":0}}}}`

	tests := []struct {
		name     string
		columns  int
		known    bool
		env      map[string]string
		wantBars int
	}{
		{
			name: "should size the bars to the terminal", columns: 60, known: true,
			wantBars: 39,
		},
		{
			name: "should size the bars to a wider terminal", columns: 100, known: true,
			wantBars: 79,
		},
		{
			name:     "should fall back to eighty columns when the terminal reports no width",
			wantBars: 59,
		},
		{
			name: "should prefer COLUMNS over the terminal", columns: 100, known: true,
			env:      map[string]string{"COLUMNS": "50"},
			wantBars: 29,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(answerHandler(answered))
			defer srv.Close()

			out, errOut, code := runAgainst(t,
				[]string{"--ask", "team=who owns this", "--pick", "x,y", "-o", "table"},
				"the server is down", srv.URL,
				WithLookupEnv(lookupFrom(tc.env)),
				WithTerminalWidth(func() (int, bool) { return tc.columns, tc.known }))

			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, ExitOK, out, errOut)
			}

			if got := strings.Count(out, "█"); got != tc.wantBars {
				t.Errorf("bar cells = %d, want %d\nstdout:\n%s", got, tc.wantBars, out)
			}
		})
	}
}

func TestProfilesLocation(t *testing.T) {
	t.Parallel()

	t.Run("should resolve profiles.json from APPDATA for the goos it is given", func(t *testing.T) {
		t.Parallel()

		env := lookupFrom(map[string]string{"APPDATA": "appdata"})
		home := func() (string, error) { return "home", nil }

		for goos, want := range map[string]string{
			"windows": filepath.Join("appdata", "onesie", "profiles.json"),
			"linux":   filepath.Join("home", ".config", "onesie", "profiles.json"),
		} {
			if got, err := profilesLocation(env, home, goos)(); err != nil || got != want {
				t.Errorf("%s: path = %q, %v, want %q", goos, got, err, want)
			}
		}
	})

	tests := []struct {
		name     string
		variable string
		// nested names the directories the config dir sits under in the one the variable holds.
		nested []string
	}{
		{name: "should find profiles.json under the home directory", nested: []string{".config", "onesie"}},
		{name: "should find profiles.json in ONESIE_CONFIG_DIR", variable: "ONESIE_CONFIG_DIR"},
		{name: "should find profiles.json under XDG_CONFIG_HOME", variable: "XDG_CONFIG_HOME", nested: []string{"onesie"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeConfig(t, filepath.Join(append([]string{dir}, tc.nested...)...), "profiles.json",
				`{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK"}}}`, 0)

			env := map[string]string{}
			if tc.variable != "" {
				env[tc.variable] = dir
			}

			var out, errOut bytes.Buffer

			// Only the environment and the home directory are replaced, so the ordering NewRootCmd
			// builds from them is the one under test.
			root := NewRootCmd(BuildInfo{Version: "1.2.3"},
				WithLookupEnv(lookupFrom(env)),
				WithHomeDir(func() (string, error) { return dir, nil }),
				WithKeychain(noKeychain()))
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs([]string{"profile", "list"})

			if code := Execute(t.Context(), root); code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut.String())
			}

			if !strings.Contains(out.String(), "* work") {
				t.Errorf("stdout = %q, want the profile from %v marked", out.String(), tc.nested)
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		open func(t *testing.T) *os.File
	}{
		{
			// A character device that is not a terminal, as stdin is under < /dev/null.
			name: "should not take the null device for a terminal",
			open: func(t *testing.T) *os.File {
				t.Helper()

				f, err := os.Open(os.DevNull)
				if err != nil {
					t.Fatalf("opening the null device: %v", err)
				}

				return f
			},
		},
		{
			name: "should not take a regular file for a terminal",
			open: func(t *testing.T) *os.File {
				t.Helper()

				path := filepath.Join(t.TempDir(), "stdin")
				if err := os.WriteFile(path, nil, 0o600); err != nil {
					t.Fatalf("writing the file: %v", err)
				}

				f, err := os.Open(path)
				if err != nil {
					t.Fatalf("opening the file: %v", err)
				}

				return f
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := tc.open(t)
			defer f.Close()

			if isTerminal(f) {
				t.Errorf("isTerminal(%s) = true, want false", f.Name())
			}
		})
	}
}

func TestPathNamedOnce(t *testing.T) {
	t.Parallel()

	missing := &fs.PathError{Op: "open", Path: "x.txt", Err: fs.ErrNotExist}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "should drop the copy of a path the message already names",
			err:  fmt.Errorf("onesie: reading x.txt: %w", missing),
			want: "onesie: reading x.txt: file does not exist",
		},
		{
			name: "should keep a path error the message names only once",
			err:  fmt.Errorf("onesie: --mock: %w", missing),
			want: "onesie: --mock: open x.txt: file does not exist",
		},
		{
			name: "should keep a message with no path error",
			err:  fmt.Errorf("onesie: reading x.txt: open x.txt: %s", "gone"),
			want: "onesie: reading x.txt: open x.txt: gone",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := pathNamedOnce(tc.err.Error(), tc.err); got != tc.want {
				t.Errorf("pathNamedOnce = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPlainArgErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check cobra.PositionalArgs
		want  string
	}{
		{
			name:  "should reword cobra.NoArgs as a plain sentence",
			check: cobra.NoArgs,
			want:  "onesie: leaf takes no argument, got 'a'",
		},
		{
			name:  "should keep another validator's message as written",
			check: cobra.ExactArgs(3),
			want:  "accepts 3 arg(s), received 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := &cobra.Command{Use: "onesie"}
			leaf := &cobra.Command{Use: "leaf", Args: tc.check, RunE: func(*cobra.Command, []string) error { return nil }}
			root.AddCommand(leaf)
			plainArgErrors(root)

			if err := leaf.Args(leaf, []string{"a", "b"}); err == nil || err.Error() != tc.want {
				t.Errorf("Args = %v, want %q", err, tc.want)
			}
		})
	}
}
