package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestAddFlagCompletions(t *testing.T) {
	t.Parallel()

	profiles := `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK"},` +
		`"home":{"provider":"levanto","api_key":"SECRET-HOME"}}}`

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "should offer every output mode for -o", args: []string{"-o", ""}, want: []string{"auto", "json", "markdown"}},
		{name: "should offer every input mode for -i", args: []string{"-i", ""}, want: []string{"lines", "jsonl", "request"}},
		{name: "should offer every provider for --provider", args: []string{"--provider", ""}, want: []string{"typesafe", "vercel"}},
		{name: "should offer the stored profiles for --profile", args: []string{"--profile", ""}, want: []string{"home", "work"}},
		{name: "should offer the built in sets for -f", args: []string{"-f", ""}, want: []string{"shell-safety"}},
		{name: "should offer the built in sets for calibrate -f", args: []string{"calibrate", "-f", ""}, want: []string{"moderation"}},
		{name: "should offer the report formats for calibrate -o", args: []string{"calibrate", "-o", ""}, want: []string{"table", "json"}},
		{name: "should offer the input modes for calibrate -i", args: []string{"calibrate", "-i", ""}, want: []string{"jsonl", "csv"}},
		{name: "should offer every outcome for --stop-on", args: []string{"--stop-on", ""}, want: []string{"error", "fail", "unsure"}},
	}

	for _, sub := range []string{"update", "delete", "use", "show", "test"} {
		tests = append(tests, struct {
			name string
			args []string
			want []string
		}{name: "should offer the stored profiles to profile " + sub, args: []string{"profile", sub, ""}, want: []string{"home", "work"}})
	}

	for _, sub := range []string{"create", "update"} {
		tests = append(tests, struct {
			name string
			args []string
			want []string
		}{name: "should offer every provider for profile " + sub + " --provider", args: []string{"profile", sub, "x", "--provider", ""}, want: []string{"typesafe", "vercel"}})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runInConfig(t, configWith(t, profiles), append([]string{"__complete"}, tc.args...), "", nil)
			offered := strings.Split(out, "\n")

			for _, want := range tc.want {
				if code != ExitOK || !slices.Contains(offered, want) {
					t.Errorf("exit %d, offered %q, want %q among them\n%s", code, offered, want, errOut)
				}
			}
		})
	}

	t.Run("should leave an old credentials.json unconverted when it offers profiles", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		writeConfig(t, dir, "credentials.json", `{"providers":{"typesafe":{"api_key":"SECRET-OLD"}}}`, 0)

		if _, errOut, code := runInConfig(t, dir, []string{"__complete", "--profile", ""}, "", nil); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, errOut)
		}

		if _, err := os.Stat(filepath.Join(dir, "profiles.json")); !os.IsNotExist(err) {
			t.Errorf("profiles.json stat = %v, want it absent", err)
		}
	})
}
