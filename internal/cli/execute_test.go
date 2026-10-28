package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestRootExecute(t *testing.T) {
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
				withKeychain(noKeychain()),
				WithClientFactory(func(context.Context, ...onesie.Option) (*onesie.Client, error) {
					return nil, errors.New("onesie: no client should be built before stdin ends")
				}))

			if code != ExitInterrupt {
				t.Errorf("exit code = %d, want %d", code, ExitInterrupt)
			}
		})
	}

	t.Run("should exit 4 for a server error whether or not stderr takes the message", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)

		for _, stderr := range []io.Writer{&bytes.Buffer{}, brokenWriter{}} {
			root := NewRootCmd(BuildInfo{Version: "1.2.3"},
				WithStdin(strings.NewReader("")),
				WithStdinTTY(false),
				WithStdoutTTY(false),
				WithLookupEnv(lookupFrom(map[string]string{"TYPESAFE_API_KEY": "k", "ONESIE_CONFIG_DIR": t.TempDir()})),
				withKeychain(noKeychain()))

			root.SetOut(io.Discard)
			root.SetErr(stderr)
			root.SetArgs([]string{"--base-url", srv.URL, "--retries", "0", "--state", "x", "is it urgent"})

			if code := root.Execute(t.Context()); code != ExitUnavailable {
				t.Errorf("exit code with stderr %T = %d, want %d", stderr, code, ExitUnavailable)
			}
		}
	})

	t.Run("should take the exit code from the failure alone and print each cleanup failure worth reporting", func(t *testing.T) {
		t.Parallel()

		stdoutGone := &fs.PathError{Op: "write", Path: "/dev/stdout", Err: syscall.EPIPE}
		release := errors.New("unlocking answers.jsonl: busy")

		cases := []struct {
			name       string
			failure    error
			cleanups   []error
			wantCode   int
			wantStderr string
		}{
			{
				name:       "should exit 2 for a failed save and print the notice stderr refused after it",
				failure:    errors.New("saving profiles.json: the disk is full"),
				cleanups:   []error{errors.New("stderr is gone")},
				wantCode:   ExitUsage,
				wantStderr: "onesie: saving profiles.json: the disk is full\nonesie: stderr is gone\n",
			},
			{
				name:       "should exit 5 for a timeout and leave out an interrupted finish ahead of a release failure",
				failure:    context.DeadlineExceeded,
				cleanups:   []error{context.Canceled, release},
				wantCode:   ExitTransport,
				wantStderr: "onesie: context deadline exceeded\nonesie: unlocking answers.jsonl: busy\n",
			},
			{
				name:     "should exit 130 and stay quiet for an interrupt followed by a broken pipe",
				failure:  context.Canceled,
				cleanups: []error{syscall.EPIPE},
				wantCode: ExitInterrupt,
			},
			{
				name:       "should exit 0 for a broken stdout and print the release failure that followed it",
				failure:    stdoutGone,
				cleanups:   []error{release},
				wantCode:   ExitOK,
				wantStderr: "onesie: unlocking answers.jsonl: busy\n",
			},
			{
				name:       "should exit 6 for failed records and print only what followed them",
				failure:    &recordsError{},
				cleanups:   []error{errors.New("compacting answers.jsonl: the disk is full"), release},
				wantCode:   ExitRecords,
				wantStderr: "onesie: compacting answers.jsonl: the disk is full\nonesie: unlocking answers.jsonl: busy\n",
			},
			{
				name:     "should exit 6 and stay quiet for failed records followed by a broken pipe and an interrupt",
				failure:  &recordsError{},
				cleanups: []error{syscall.EPIPE, context.Canceled},
				wantCode: ExitRecords,
			},
			{
				name:       "should take the code of a cleanup failure when nothing else failed",
				cleanups:   []error{release},
				wantCode:   ExitUsage,
				wantStderr: "onesie: unlocking answers.jsonl: busy\n",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				var errOut bytes.Buffer

				root := failingCleanups(&errOut, func(int) ([]error, error) { return tc.cleanups, tc.failure })

				if code := root.Execute(t.Context()); code != tc.wantCode || errOut.String() != tc.wantStderr {
					t.Errorf("exit %d, stderr %q, want exit %d and %q", code, errOut.String(), tc.wantCode, tc.wantStderr)
				}
			})
		}
	})

	t.Run("should start a second run with none of the first run's cleanup failures", func(t *testing.T) {
		t.Parallel()

		var errOut bytes.Buffer

		root := failingCleanups(&errOut, func(run int) ([]error, error) {
			if run == 1 {
				return []error{errors.New("stderr is gone")}, errors.New("saving profiles.json: the disk is full")
			}

			return nil, nil
		})

		if code := root.Execute(t.Context()); code != ExitUsage {
			t.Fatalf("first run exit %d, want %d", code, ExitUsage)
		}

		errOut.Reset()

		if code := root.Execute(t.Context()); code != ExitOK || errOut.Len() != 0 {
			t.Errorf("second run exit %d, stderr %q, want exit 0 and nothing", code, errOut.String())
		}
	})

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
			want:     "onesie: profiles file {dir}{/}profiles.json is not valid JSON\n",
		},
		{
			name:     "should print a profiles file of the wrong shape as not a JSON object of profiles",
			args:     []string{"profile", "list"},
			env:      map[string]string{"ONESIE_CONFIG_DIR": "{dir}"},
			files:    map[string]string{"profiles.json": `[]`},
			wantCode: ExitUsage,
			want:     "onesie: profiles file {dir}{/}profiles.json is not a JSON object of profiles\n",
		},
		{
			name:  "should embed a keychain error in a warning without a prefix",
			args:  []string{"profile", "create", "work", "--provider", "typesafe"},
			stdin: key16 + "\n",
			env:   map[string]string{"ONESIE_CONFIG_DIR": "{dir}"},
			want: "onesie: warning: could not store the key in the OS keychain: no keychain in tests. " +
				"The key is in {dir}{/}profiles.json instead\n" +
				"onesie: 'work' is the default profile, since it is the first\n",
		},
		{
			name:    "should print a mode warning as its own line without a prefix",
			args:    []string{"profile", "create", "work", "--provider", "typesafe", "--file"},
			stdin:   key16 + "\n",
			env:     map[string]string{"ONESIE_CONFIG_DIR": "{dir}"},
			prepare: failingChmodStore,
			want: "onesie: warning: could not set mode 600 on {dir}{/}profiles.json. The key is not protected by the filesystem\n" +
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
			want: "onesie: warning: the cache could not open, so onesie asks the API for the rest of this run: " +
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
			name: "should prefix both lines of a run failure followed by a release failure",
			args: []string{
				"--mock", "{dir}{/}empty.jsonl", "--out", "{dir}{/}answers.jsonl", "--state", "x", "is it urgent",
			},
			files:    map[string]string{"empty.jsonl": ""},
			prepare:  failingRelease,
			wantCode: ExitUsage,
			want: "onesie: --mock: the file is empty\n" +
				"onesie: unlocking {dir}{/}answers.jsonl: the test release failed\n",
		},
		{
			name: "should prefix both lines of a resume failure followed by a release failure",
			args: []string{
				"--mock", "{dir}{/}m.json", "--out", "{dir}{/}answers.jsonl", "--resume", "--state", "x", "is it urgent",
			},
			files:    map[string]string{"m.json": `{"q":0.9}`},
			prepare:  stalePartAndFailingRelease,
			unixOnly: true,
			wantCode: ExitUsage,
			want: "onesie: removing {dir}{/}answers.jsonl.onesie.part, which an interrupted run left behind: " +
				"remove {dir}{/}answers.jsonl.onesie.part: directory not empty\n" +
				"onesie: unlocking {dir}{/}answers.jsonl: the test release failed\n",
		},
		{
			name: "should print a release failure that follows failed records and keep exit 6",
			args: []string{
				"--mock", "{dir}{/}m.jsonl", "-i", "lines", "--out", "{dir}{/}answers.jsonl", "--ask", "q=is it urgent",
			},
			stdin:    "a\n",
			files:    map[string]string{"m.jsonl": "{\"error\":503}\n"},
			prepare:  failingRelease,
			wantCode: ExitRecords,
			want:     "onesie: unlocking {dir}{/}answers.jsonl: the test release failed\n",
		},
		{
			name: "should print a compaction failure that follows failed records and keep exit 6",
			args: []string{
				"--mock", "{dir}{/}m.jsonl", "-i", "jsonl", "--id", ".id", "--out", "{dir}{/}answers.jsonl", "--resume",
				"--ask", "q=is it urgent",
			},
			stdin:    "{\"id\":1}\n{\"id\":2}\n",
			files:    map[string]string{"m.jsonl": "{\"id\":1,\"error\":503}\n{\"id\":2,\"q\":0.9}\n"},
			prepare:  failingCompaction,
			wantCode: ExitRecords,
			want:     "onesie: compacting {dir}{/}answers.jsonl: the test rename failed\n",
		},
		{
			name: "should stay quiet for a release interrupted after failed records",
			args: []string{
				"--mock", "{dir}{/}m.jsonl", "-i", "lines", "--out", "{dir}{/}answers.jsonl", "--ask", "q=is it urgent",
			},
			stdin:    "a\n",
			files:    map[string]string{"m.jsonl": "{\"error\":503}\n"},
			prepare:  interruptedRelease,
			wantCode: ExitRecords,
		},
		{
			name:     "should print a command error with one onesie prefix",
			args:     []string{"cache", "clear", "extra"},
			wantCode: ExitUsage,
			want:     "onesie: cache clear takes no argument\n",
		},
		{
			name:     "should refuse --mock beside --list-models before listing",
			args:     []string{"--list-models", "--mock", "{dir}{/}m.json"},
			files:    map[string]string{"m.json": `{"q":0.9}`},
			wantCode: ExitUsage,
			want:     "onesie: --list-models asks no question, so --mock has nothing to answer. Drop one\n",
		},
		{
			name:     "should refuse --assert beside -i request before reading a body",
			args:     []string{"-i", "request", "--assert", "a"},
			wantCode: ExitUsage,
			want:     "onesie: --assert does not apply to -i request, which forwards raw responses\n",
		},
		{
			name:     "should name ONESIE_MOCK when it is set beside -i request",
			args:     []string{"-i", "request"},
			env:      map[string]string{"ONESIE_MOCK": "{dir}{/}m.json"},
			files:    map[string]string{"m.json": `{"q":0.9}`},
			wantCode: ExitUsage,
			want: "onesie: -i request sends each body as written, so ONESIE_MOCK has nothing to answer. " +
				"Unset it or drop -i request\n",
		},
		{
			name:     "should name the file's abstain_if key when it has no assertion beside it",
			args:     []string{"-f", "{dir}{/}abstain.yaml", "--state", "x"},
			files:    map[string]string{"abstain.yaml": "abstain_if: urgent.value > 0.5\nurgent:\n  ask: is it urgent\n"},
			wantCode: ExitUsage,
			want:     "onesie: 'abstain_if' needs --assert, since without one every record is a yes\n",
		},
		{
			name:  "should dry run a file whose only gate is its own assert key",
			args:  []string{"--print-request", "-f", "{dir}{/}gated.yaml", "--state", "x"},
			files: map[string]string{"gated.yaml": "assert: urgent.value > 0.5\nurgent:\n  ask: is it urgent\n"},
		},
		{
			name:     "should refuse --assert beside --print-request",
			args:     []string{"--print-request", "--assert", "a", "--state", "x", "is it urgent"},
			wantCode: ExitUsage,
			want:     "onesie: --assert judges an answer, which --print-request does not produce\n",
		},
		{
			name:     "should refuse -q on a stream",
			args:     []string{"-q", "-i", "jsonl", "is it urgent"},
			wantCode: ExitUsage,
			want:     "onesie: -q reads one record. Drop -i jsonl or use -o values and filter the stream\n",
		},
		{
			name:     "should report a missing question ahead of the flag rules",
			args:     []string{"--pick", "a,b", "-r", "-o", "json"},
			wantCode: ExitUsage,
			want:     "onesie: no question given. Pass a question, --ask, or -f\n",
		},
		{
			name:     "should report a question file it cannot load ahead of the flag rules",
			args:     []string{"-f", "{dir}{/}unknown.yaml", "-r", "-o", "json"},
			files:    map[string]string{"unknown.yaml": "urgent:\n  ask: x\n  bogus: 1\n"},
			wantCode: ExitUsage,
			want:     "onesie: {dir}{/}unknown.yaml: question 'urgent' has an unknown key 'bogus'\n",
		},
		{
			name:     "should refuse -q over two questions",
			args:     []string{"-q", "--ask", "a=x", "--ask", "b=y", "--state", "s"},
			wantCode: ExitUsage,
			want:     "onesie: -q needs a single question, or --assert to gate several. 'a', 'b' were asked\n",
		},
		{
			name:     "should report the line field ahead of a fault in the question",
			args:     []string{"--ask", "line=x", "-i", "lines", "--pick", "a"},
			wantCode: ExitUsage,
			want: "onesie: question id 'line' is reserved under -i lines, " +
				"which writes the input line number as line\n",
		},
		{
			name: "should print the version whatever ONESIE_CACHE holds",
			args: []string{"-V"},
			env:  map[string]string{"ONESIE_CACHE": "maybe"},
		},
		{
			name:     "should refuse an ONESIE_CACHE value it cannot read",
			args:     []string{"--state", "s", "is it urgent"},
			env:      map[string]string{"ONESIE_CACHE": "maybe"},
			wantCode: ExitUsage,
			want:     "onesie: ONESIE_CACHE=maybe is not a value --cache takes. Use 1 or 0\n",
		},
		{
			name: "should warn that -j is ignored for one record",
			args: []string{"-j", "4", "--print-request", "--state", "s", "is it urgent"},
			want: "onesie: warning: -j 4 ignored. -i text reads one record\n",
		},
		{
			name: "should refuse --offline beside --mock in calibrate",
			args: []string{
				"calibrate", "--ask", "urgent=is it urgent", "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
				"--offline", "--mock", "{dir}{/}m.json",
			},
			files:    map[string]string{"m.json": `{"q":0.9}`},
			wantCode: ExitUsage,
			want:     "onesie: --offline reads every answer from --out, and --mock answers from a file of its own. Drop one\n",
		},
		{
			name: "should say a run flag calibrate does not take belongs to a run",
			args: []string{
				"calibrate", "--ask", "urgent=is it urgent", "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
				"--no-dedup",
			},
			wantCode: ExitUsage,
			want:     "onesie: calibrate has no flag --no-dedup. --no-dedup belongs to a run, not to calibrate\n",
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

func TestRootExecuteContext(t *testing.T) {
	t.Parallel()

	failure := errors.New("saving profiles.json: the disk is full")
	release := errors.New("unlocking answers.jsonl: busy")

	tests := []struct {
		name     string
		failure  error
		cleanups []error
		wantIs   []error
	}{
		{name: "should return nil when nothing failed"},
		{name: "should return the failure alone when no cleanup failed", failure: failure, wantIs: []error{failure}},
		{
			name: "should return the failure joined with the cleanup failures that followed it", failure: failure,
			cleanups: []error{release}, wantIs: []error{failure, release},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := failingCleanups(io.Discard, func(int) ([]error, error) { return tc.cleanups, tc.failure })

			err := root.ExecuteContext(t.Context())
			if (err == nil) != (len(tc.wantIs) == 0) {
				t.Fatalf("error = %v, want one matching %v", err, tc.wantIs)
			}

			for _, want := range tc.wantIs {
				if !errors.Is(err, want) {
					t.Errorf("error = %v, want one matching %v", err, want)
				}
			}
		})
	}

	t.Run("should start a second run with none of the first run's cleanup failures", func(t *testing.T) {
		t.Parallel()

		root := failingCleanups(io.Discard, func(run int) ([]error, error) {
			if run == 1 {
				return []error{release}, failure
			}

			return nil, failure
		})

		if err := root.ExecuteContext(t.Context()); !errors.Is(err, release) {
			t.Fatalf("first run error = %v, want one matching %v", err, release)
		}

		if err := root.ExecuteContext(t.Context()); !errors.Is(err, failure) || errors.Is(err, release) {
			t.Errorf("second run error = %v, want %v alone", err, failure)
		}
	})
}

func failingCleanups(stderr io.Writer, script func(run int) (cleanups []error, failure error)) *Root {
	cleanups := &cleanupLog{}
	runs := 0

	root := &Root{Command: &cobra.Command{
		Use:           "onesie",
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(*cobra.Command, []string) error {
			runs++

			failed, err := script(runs)
			for _, cleanup := range failed {
				err = cleanups.after(err, cleanup)
			}

			return err
		},
	}, cleanups: cleanups}

	root.SetErr(stderr)
	root.SetArgs([]string{})

	return root
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
		done <- root.Execute(ctx)
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
	return []RootOption{withCredentialStore(creds.NewStore(creds.WithCreateTemp(func(string, string) (*os.File, error) {
		return nil, errors.New("the disk is full")
	})))}
}

func failingChmodStore(*testing.T, string) []RootOption {
	return []RootOption{withCredentialStore(creds.NewStore(creds.WithChmod(func(string, os.FileMode) error {
		return errors.New("the test chmod failed")
	})))}
}

func stalePartAndFailingRelease(t *testing.T, dir string) []RootOption {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, "answers.jsonl.onesie.part", "held"), 0o700); err != nil {
		t.Fatal(err)
	}

	return failingRelease(t, dir)
}

func failingRelease(*testing.T, string) []RootOption {
	return []RootOption{func(s *rootSettings) {
		s.lock = func(string) (func() error, error) {
			return func() error { return errors.New("the test release failed") }, nil
		}
	}}
}

func failingCompaction(*testing.T, string) []RootOption {
	return []RootOption{func(s *rootSettings) {
		s.rename = func(oldpath, newpath string) error {
			if strings.HasSuffix(oldpath, ".onesie.part") {
				return errors.New("the test rename failed")
			}

			return os.Rename(oldpath, newpath)
		}
	}}
}

func interruptedRelease(*testing.T, string) []RootOption {
	return []RootOption{func(s *rootSettings) {
		s.lock = func(string) (func() error, error) {
			return func() error { return context.Canceled }, nil
		}
	}}
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
