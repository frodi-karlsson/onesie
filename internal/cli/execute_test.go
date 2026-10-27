package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestExecute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "should exit 130 on an interrupt while one record is still on stdin", args: []string{"is this urgent"}},
		{name: "should exit 130 on an interrupt while a json record is still on stdin", args: []string{"is this urgent", "-i", "json"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			held, _ := io.Pipe()

			code := interruptedRun(t, tc.args,
				WithStdin(held),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithLookupEnv(lookupFrom(map[string]string{"TYPESAFE_API_KEY": "k"})),
				WithKeychain(noKeychain()),
				WithClientFactory(func(context.Context, ...onesie.Option) (*onesie.Client, error) {
					return nil, errors.New("onesie: no client should be built before stdin ends")
				}))

			if code != ExitInterrupt {
				t.Errorf("exit code = %d, want %d", code, ExitInterrupt)
			}
		})
	}

	pins := []struct {
		name     string
		args     []string
		stdin    string
		env      map[string]string
		files    map[string]string
		prepare  func(t *testing.T, dir string) []RootOption
		unixOnly bool
		wantCode int
		want     string
	}{
		{
			name:     "should print a plan error with one onesie prefix",
			args:     []string{"-r", "-o", "json", "--state", "x", "is it urgent"},
			wantCode: ExitUsage,
			want:     "onesie: -r and -o are mutually exclusive\n",
		},
		{
			name:     "should print an input error with one onesie prefix",
			args:     []string{"-i", "bogus", "is it urgent"},
			wantCode: ExitUsage,
			want:     "onesie: -i takes text, json, jsonl, lines, csv, tsv or request, got 'bogus'\n",
		},
		{
			name:     "should print an output error with one onesie prefix",
			args:     []string{"-o", "bogus", "--state", "x", "is it urgent"},
			wantCode: ExitUsage,
			want:     "onesie: -o takes auto, json, values, table, raw, csv, tsv or markdown, got 'bogus'\n",
		},
		{
			name:     "should print a question file error with one onesie prefix",
			args:     []string{"--print-questions", "is it urgent"},
			wantCode: ExitUsage,
			want:     "onesie: --print-questions needs a named question. Use --ask NAME=QUESTION\n",
		},
		{
			name:     "should embed the config dir error in a missing question file error without a second prefix",
			args:     []string{"-f", "nosuch", "--state", "x"},
			wantCode: ExitUsage,
			want: "onesie: no file or question file named nosuch. Looked for nosuch.yaml, nosuch.yml and nosuch.json. " +
				"Found no .onesie/questions in {dir} or any parent. " +
				"Built in: moderation, personal-data, prompt-injection, shell-safety. " +
				"The config dir could not be resolved, so it was not searched: " +
				"cannot find a home directory for the config dir, where the profiles file lives\n",
		},
		{
			name:     "should print a profiles file error with one onesie prefix",
			args:     []string{"profile", "list"},
			env:      map[string]string{"ONESIE_CONFIG_DIR": "{dir}"},
			files:    map[string]string{"profiles.json": `{"profiles":`},
			wantCode: ExitUsage,
			want:     "onesie: profiles file {dir}{/}profiles.json is not a JSON object of profiles\n",
		},
		{
			name:  "should embed a keychain error in a warning without a prefix",
			args:  []string{"profile", "create", "work", "--provider", "typesafe"},
			stdin: key16 + "\n",
			env:   map[string]string{"ONESIE_CONFIG_DIR": "{dir}"},
			want: "warning: could not store the key in the OS keychain: no keychain in tests. " +
				"The key is in {dir}{/}profiles.json instead\n" +
				"onesie: 'work' is the default profile, since it is the first\n",
		},
		{
			name:    "should print a conversion warning with one onesie prefix",
			args:    []string{"profile", "list"},
			env:     map[string]string{"ONESIE_CONFIG_DIR": "{dir}"},
			files:   map[string]string{"credentials.json": `{"providers":{"levanto":{"api_key":"SECRET-LV"}}}`},
			prepare: fullDisk,
			want: "onesie: converted credentials.json for this run, with profile 'levanto'. " +
				"No profile is the default, so run onesie profile use NAME to choose one\n" +
				"onesie: could not write {dir}{/}profiles.json, so {dir}{/}credentials.json is converted for this " +
				"run only: creating a temporary file in {dir}: the disk is full\n" +
				"active: none, since no profile is the default and nothing chose one. " +
				"Run onesie profile use NAME, or pass --profile NAME or --provider NAME\n",
		},
		{
			name:  "should name a cache that could not open in a warning",
			args:  []string{"--cache", "--base-url", "{url}", "--state", "x", "is it urgent"},
			env:   map[string]string{"ONESIE_CACHE_DIR": "{dir}{/}notdir"},
			files: map[string]string{"notdir": "x"},
			want: "warning: the cache could not open, so onesie asks the API for the rest of this run: " +
				"the cache dir {dir}{/}notdir is not a directory\n",
		},
		{
			name:     "should print a cache error with one onesie prefix",
			args:     []string{"--cache", "--base-url", "{url}", "--state", "x", "is it urgent"},
			env:      map[string]string{"ONESIE_CACHE_DIR": "{dir}{/}cache"},
			prepare:  readableCacheDir,
			unixOnly: true,
			wantCode: ExitUsage,
			want: "onesie: the cache dir {dir}{/}cache has mode 755, so others can read cached answers. " +
				"Run chmod 700 {dir}{/}cache\n",
		},
		{
			name:     "should print the cache command's mode warning with one onesie prefix",
			args:     []string{"cache"},
			env:      map[string]string{"ONESIE_CACHE_DIR": "{dir}{/}cache"},
			prepare:  readableCacheDir,
			unixOnly: true,
			want: "onesie: the cache dir {dir}{/}cache has mode 755, so others can read cached answers. " +
				"Run chmod 700 {dir}{/}cache\n",
		},
		{
			name:     "should print a mock file error with one onesie prefix",
			args:     []string{"--mock", "{dir}{/}empty.jsonl", "--state", "x", "is it urgent"},
			files:    map[string]string{"empty.jsonl": ""},
			wantCode: ExitUsage,
			want:     "onesie: --mock: the file is empty\n",
		},
		{
			name:     "should print a record the mock file does not answer with one onesie prefix",
			args:     []string{"--mock", "{dir}{/}two.jsonl", "-i", "lines", "-o", "json", "--ask", "q=is it urgent"},
			stdin:    "a\nb\nc\n",
			files:    map[string]string{"two.jsonl": "{\"q\":0.9}\n{\"q\":0.8}\n"},
			wantCode: ExitUsage,
			want:     "onesie: --mock has no line 3, so input line 3 has no answer. Add one\n",
		},
		{
			name:     "should print a command error with one onesie prefix",
			args:     []string{"cache", "clear", "extra"},
			wantCode: ExitUsage,
			want:     "onesie: cache clear takes no argument\n",
		},
	}

	for _, tc := range pins {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode check does not apply")
			}

			dir := t.TempDir()
			stub := newAnswerStub(t)
			fill := strings.NewReplacer("{dir}", dir, "{/}", string(filepath.Separator), "{url}", stub.url).Replace

			for name, content := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			env := map[string]string{"TYPESAFE_API_KEY": "k"}
			for name, value := range tc.env {
				env[name] = fill(value)
			}

			args := make([]string, 0, len(tc.args))
			for _, arg := range tc.args {
				args = append(args, fill(arg))
			}

			opts := []RootOption{WithWorkingDir(fixedPath(dir))}
			if tc.prepare != nil {
				opts = append(opts, tc.prepare(t, dir)...)
			}

			_, errOut, code := runCached(t, env, args, tc.stdin, opts...)
			if code != tc.wantCode || errOut != fill(tc.want) {
				t.Errorf("exit %d, stderr %q, want exit %d and %q", code, errOut, tc.wantCode, fill(tc.want))
			}
		})
	}
}

func interruptedRun(t *testing.T, args []string, opts ...RootOption) int {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var out, errOut bytes.Buffer

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, opts...)
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	done := make(chan int, 1)

	go func() {
		done <- Execute(ctx, root)
	}()

	select {
	case code := <-done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatalf("the run was still waiting on stdin after the interrupt\nstderr:\n%s", errOut.String())

		return 0
	}
}

func fullDisk(*testing.T, string) []RootOption {
	return []RootOption{WithCredentialStore(creds.NewStore(creds.WithCreateTemp(func(string, string) (*os.File, error) {
		return nil, errors.New("the disk is full")
	})))}
}

func readableCacheDir(t *testing.T, dir string) []RootOption {
	t.Helper()

	cacheDir := filepath.Join(dir, "cache")
	if err := os.Mkdir(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Mkdir respects the umask, so the mode is set explicitly.
	if err := os.Chmod(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}

	return nil
}
