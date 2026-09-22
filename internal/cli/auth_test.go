package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/jev-cli/internal/creds"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func TestAuthStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		env      map[string]string
		file     string
		fileMode os.FileMode
		unixOnly bool
		wantOut  string
		wantPath bool
		wantErr  string
		wantCode int
	}{
		{
			name:     "should report the environment",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			wantOut:  "source: env\n",
			wantCode: ExitOK,
		},
		{
			name:     "should report the file",
			file:     `{"api_key":"SECRET-FILE"}`,
			wantPath: true,
			wantCode: ExitOK,
		},
		{
			name:     "should report none and exit 3",
			wantOut:  "source: none\n",
			wantCode: ExitAuth,
		},
		{
			name:     "should refuse a file others can reach and exit 3",
			file:     `{"api_key":"SECRET-FILE"}`,
			fileMode: 0o644,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 644",
			wantCode: ExitAuth,
		},
		{
			name:     "should ignore a file others can reach when the environment has a key",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			file:     `{"api_key":"SECRET-FILE"}`,
			fileMode: 0o644,
			unixOnly: true,
			wantOut:  "source: env\n",
			wantCode: ExitOK,
		},
		{
			name:     "should prefer the environment over the file",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			file:     `{"api_key":"SECRET-FILE"}`,
			wantOut:  "source: env\n",
			wantCode: ExitOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode check does not apply")
			}

			path := credentialFixture(t, tc.file, tc.fileMode)

			out, errOut, code := runAuth(t, []string{"auth", "status"},
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(tc.env)))

			// First, because every other assertion below would pass on output that had the key
			// spliced into it.
			assertNoSecret(t, out, errOut)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}

			want := tc.wantOut
			if tc.wantPath {
				want = "source: file " + path + "\n"
			}

			if out != want {
				t.Errorf("stdout = %q, want %q", out, want)
			}

			if tc.wantErr == "" {
				if errOut != "" {
					t.Errorf("stderr = %q, want nothing", errOut)
				}

				return
			}

			if !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}
		})
	}
}

func TestAuthClear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
	}{
		{
			name: "should delete the credential file",
			file: `{"api_key":"SECRET-FILE"}`,
		},
		{
			name: "should exit zero when there is no credential file",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := credentialFixture(t, tc.file, 0)

			out, errOut, code := runAuth(t, []string{"auth", "clear"},
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(nil)))

			assertNoSecret(t, out, errOut)

			if code != ExitOK {
				t.Errorf("exit code = %d, want %d", code, ExitOK)
			}

			if out != "" || errOut != "" {
				t.Errorf("wrote stdout %q and stderr %q, want nothing on either", out, errOut)
			}

			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the credential file is still there, stat error = %v", err)
			}
		})
	}
}

func TestAuthSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		stdin        string
		tty          bool
		secret       string
		existing     string
		existingMode os.FileMode
		unixOnly     bool
		wantKey      string
		wantBase     string
		wantNoFile   bool
		wantErr      string
		wantCode     int
	}{
		{
			name:     "should store a key read from stdin",
			stdin:    "SECRET-STDIN\n",
			wantKey:  "SECRET-STDIN",
			wantCode: ExitOK,
		},
		{
			name:     "should accept stdin with no trailing newline",
			stdin:    "SECRET-STDIN",
			wantKey:  "SECRET-STDIN",
			wantCode: ExitOK,
		},
		{
			name:     "should strip the whitespace around the key",
			stdin:    "  \tSECRET-STDIN \n",
			wantKey:  "SECRET-STDIN",
			wantCode: ExitOK,
		},
		{
			name:       "should reject an empty stdin",
			stdin:      "",
			wantErr:    "jev: auth set: the key is empty",
			wantNoFile: true,
			wantCode:   ExitUsage,
		},
		{
			name:       "should reject a whitespace only key",
			stdin:      "   \n",
			wantErr:    "jev: auth set: the key is empty",
			wantNoFile: true,
			wantCode:   ExitUsage,
		},
		{
			name:  "should reject a metadata line after the secret",
			stdin: "SECRET-STDIN\nlogin: someone\n",
			wantErr: "jev: auth set: stdin carries more than one line. " +
				"The key is the first line",
			wantNoFile: true,
			wantCode:   ExitUsage,
		},
		{
			name:     "should accept blank lines after the secret",
			stdin:    "SECRET-STDIN\n\n   \n",
			wantKey:  "SECRET-STDIN",
			wantCode: ExitOK,
		},
		{
			name:     "should store the base url when one is given",
			args:     []string{"--base-url", "https://proxy.example"},
			stdin:    "SECRET-STDIN\n",
			wantKey:  "SECRET-STDIN",
			wantBase: "https://proxy.example",
			wantCode: ExitOK,
		},
		{
			name:       "should reject a base url with no scheme",
			args:       []string{"--base-url", "proxy.example"},
			stdin:      "SECRET-STDIN\n",
			wantErr:    "base URL must be an absolute http or https URL, got proxy.example",
			wantNoFile: true,
			wantCode:   ExitUsage,
		},
		{
			name:       "should reject a base url with no host",
			args:       []string{"--base-url", "https://"},
			stdin:      "SECRET-STDIN\n",
			wantErr:    "base URL must be an absolute http or https URL, got https://",
			wantNoFile: true,
			wantCode:   ExitUsage,
		},
		{
			name:     "should overwrite an existing file",
			stdin:    "SECRET-STDIN\n",
			existing: `{"api_key":"SECRET-OLD","base_url":"https://old.example"}`,
			wantKey:  "SECRET-STDIN",
			wantCode: ExitOK,
		},
		{
			name:         "should overwrite an existing file others can reach",
			stdin:        "SECRET-STDIN\n",
			existing:     `{"api_key":"SECRET-OLD"}`,
			existingMode: 0o644,
			unixOnly:     true,
			wantKey:      "SECRET-STDIN",
			wantCode:     ExitOK,
		},
		{
			name:     "should read the key from the hidden prompt when stdin is a tty",
			stdin:    "SECRET-STDIN\n",
			tty:      true,
			secret:   "SECRET-PROMPT\n",
			wantKey:  "SECRET-PROMPT",
			wantCode: ExitOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode does not apply")
			}

			path := credentialFixture(t, tc.existing, tc.existingMode)

			out, errOut, code := runAuth(t, append([]string{"auth", "set"}, tc.args...),
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(nil)),
				WithStdin(strings.NewReader(tc.stdin)),
				WithStdinTTY(tc.tty),
				WithSecretReader(func() (string, error) { return tc.secret, nil }))

			assertNoSecret(t, out, errOut)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}

			// auth set writes no output at all. The path notice is a notice, so a redirected
			// stdout stays empty.
			if out != "" {
				t.Errorf("stdout = %q, want nothing", out)
			}

			if tc.wantErr != "" {
				if !strings.Contains(errOut, tc.wantErr) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
				}
			}

			if tc.wantNoFile {
				assertMissing(t, path, tc.existing != "")

				return
			}

			if !strings.Contains(errOut, "jev: writing "+path) {
				t.Errorf("stderr = %q, want it to name the path being written", errOut)
			}

			if tc.tty && !strings.Contains(errOut, "API key: ") {
				t.Errorf("stderr = %q, want it to carry the prompt", errOut)
			}

			assertStored(t, path, tc.wantKey, tc.wantBase)
		})
	}
}

func TestAuthTest(t *testing.T) {
	t.Parallel()

	const listing = `{"models":[` +
		`{"name":"jev-latest","description":"alias","release_date":"2026-08-01"},` +
		`{"name":"jev-1.13.0","description":"current","release_date":"2026-08-01"}]}`

	tests := []struct {
		name      string
		env       map[string]string
		file      string
		fileBase  bool
		unreached bool
		status    int
		response  string
		wantOut   string
		wantPath  bool
		wantCode  int
	}{
		{
			name:     "should print the source and the model count",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			response: listing,
			wantOut:  "source: env\nmodels: 2\n",
			wantCode: ExitOK,
		},
		{
			name:     "should reach the base url stored beside the key",
			file:     `{"api_key":"SECRET-FILE"}`,
			fileBase: true,
			response: listing,
			wantPath: true,
			wantCode: ExitOK,
		},
		{
			name:     "should exit 3 on a rejected key",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			status:   http.StatusUnauthorized,
			response: `{"error":{"message":"invalid api key"}}`,
			wantCode: ExitAuth,
		},
		{
			name:     "should exit 3 on a forbidden key",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			status:   http.StatusForbidden,
			response: `{"error":{"message":"forbidden"}}`,
			wantCode: ExitAuth,
		},
		{
			name:      "should exit 5 when the server cannot be reached",
			env:       map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			unreached: true,
			wantCode:  ExitTransport,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")

				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}

				if _, err := io.WriteString(w, tc.response); err != nil {
					t.Errorf("writing the stub response: %v", err)
				}
			}))
			defer srv.Close()

			base := srv.URL
			if tc.unreached {
				// Closed before the run, so the connection is refused rather than answered.
				srv.Close()
			}

			file := tc.file
			if tc.fileBase {
				file = `{"api_key":"SECRET-FILE","base_url":"` + base + `"}`
			}

			path := credentialFixture(t, file, 0)

			out, errOut, code := runAuth(t, []string{"auth", "test"},
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(tc.env)),
				WithClientFactory(stubFactory(base)))

			assertNoSecret(t, out, errOut)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
			}

			want := tc.wantOut
			if tc.wantPath {
				want = "source: file " + path + "\nmodels: 2\n"
			}

			if out != want {
				t.Errorf("stdout = %q, want %q", out, want)
			}
		})
	}
}

func TestStoredOptions(t *testing.T) {
	t.Parallel()

	const listing = `{"models":[{"name":"jev-latest","description":"alias",` +
		`"release_date":"2026-08-01"}]}`

	tests := []struct {
		name string
		flag bool
		env  bool
	}{
		{
			name: "should keep --base-url ahead of the one stored beside the key",
			flag: true,
		},
		{
			name: "should keep the environment ahead of the one stored beside the key",
			env:  true,
		},
		{
			name: "should reach the stored base url when nothing outranks it",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var reached atomic.Bool

			wanted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached.Store(true)
				w.Header().Set("Content-Type", "application/json")

				if _, err := io.WriteString(w, listing); err != nil {
					t.Errorf("writing the stub response: %v", err)
				}
			}))
			defer wanted.Close()

			unwanted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("the request reached the base url that should have been outranked")
				w.Header().Set("Content-Type", "application/json")

				if _, err := io.WriteString(w, listing); err != nil {
					t.Errorf("writing the stub response: %v", err)
				}
			}))
			defer unwanted.Close()

			stored := unwanted.URL
			if !tc.flag && !tc.env {
				stored = wanted.URL
			}

			env := map[string]string{}
			if tc.env {
				env[jev.EnvBaseURL] = wanted.URL
			}

			flags := &runFlags{}
			if tc.flag {
				flags.baseURL = wanted.URL
			}

			settings := rootSettings{lookupEnv: lookupFrom(env)}
			source := keySource{name: sourceFile, key: "SECRET-FILE", baseURL: stored}

			// The two options defaultClientFactory installs from the flags, so the unit under test
			// sees the same precedence a real run would build.
			opts := []jev.Option{jev.WithAPIKey("stub"), jev.WithEnv(lookupFrom(env))}
			if flags.baseURL != "" {
				opts = append(opts, jev.WithBaseURL(flags.baseURL))
			}

			client, err := jev.New(append(opts, storedOptions(settings, flags, source)...)...)
			if err != nil {
				t.Fatalf("building the client: %v", err)
			}

			if _, listErr := client.ListModels(t.Context()); listErr != nil {
				t.Fatalf("listing the models: %v", listErr)
			}

			if !reached.Load() {
				t.Error("the request never reached the base url that should have won")
			}
		})
	}
}

func TestNewAuthCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		wantOut  string
		wantErr  string
		wantCode int
	}{
		{
			name:     "should print the subcommands when given no subcommand",
			args:     []string{"auth"},
			wantOut:  "Available Commands:",
			wantCode: ExitOK,
		},
		{
			name:     "should reject an unknown subcommand",
			args:     []string{"auth", "nonsense"},
			wantErr:  `jev: unknown command "nonsense" for "jev auth"`,
			wantCode: ExitUsage,
		},
		{
			name: "should reject a question given to auth set",
			args: []string{"auth", "set", "is this urgent"},
			wantErr: "jev: auth set takes no question or state. " +
				"It reads the key from a prompt or stdin",
			wantCode: ExitUsage,
		},
		{
			name: "should reject a question given to auth status",
			args: []string{"auth", "status", "is this urgent"},
			wantErr: "jev: auth status takes no question or state. " +
				"It reports which source holds the key",
			wantCode: ExitUsage,
		},
		{
			name: "should reject a question given to auth test",
			args: []string{"auth", "test", "is this urgent"},
			wantErr: "jev: auth test takes no question or state. " +
				"It calls the models endpoint with the resolved key",
			wantCode: ExitUsage,
		},
		{
			name: "should reject a question given to auth clear",
			args: []string{"auth", "clear", "is this urgent"},
			wantErr: "jev: auth clear takes no question or state. " +
				"It deletes the credential file",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject a root question flag on a subcommand",
			args:     []string{"auth", "set", "--pick", "a,b"},
			wantErr:  "jev: unknown flag: --pick",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject a root question flag placed before the subcommand",
			args:     []string{"--pick", "a,b", "auth", "set"},
			wantErr:  "jev: unknown flag: --pick",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject an output flag on a subcommand",
			args:     []string{"auth", "status", "-o", "json"},
			wantErr:  "jev: unknown shorthand flag: 'o' in -o",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject a state flag on a subcommand",
			args:     []string{"auth", "clear", "--state", "x"},
			wantErr:  "jev: unknown flag: --state",
			wantCode: ExitUsage,
		},
		{
			name:     "should keep --base-url off the subcommands that store nothing",
			args:     []string{"auth", "status", "--base-url", "https://proxy.example"},
			wantErr:  "jev: unknown flag: --base-url",
			wantCode: ExitUsage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := credentialFixture(t, "", 0)

			out, errOut, code := runAuth(t, tc.args,
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(nil)))

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
			}

			if tc.wantOut != "" && !strings.Contains(out, tc.wantOut) {
				t.Errorf("stdout = %q, want it to contain %q", out, tc.wantOut)
			}

			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}
		})
	}
}

func TestResolveKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		apiKey   string
		env      map[string]string
		file     string
		wantName string
		wantKey  string
		wantBase string
	}{
		{
			name:     "should report the flag when one is passed",
			apiKey:   "SECRET-FLAG",
			wantName: sourceFlag,
			wantKey:  "SECRET-FLAG",
		},
		{
			name:     "should prefer the flag over the environment and the file",
			apiKey:   "SECRET-FLAG",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			file:     `{"api_key":"SECRET-FILE"}`,
			wantName: sourceFlag,
			wantKey:  "SECRET-FLAG",
		},
		{
			name:     "should prefer the environment over the file",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			file:     `{"api_key":"SECRET-FILE"}`,
			wantName: sourceEnv,
			wantKey:  "SECRET-ENV",
		},
		{
			name:     "should ignore a blank environment variable",
			env:      map[string]string{jev.EnvAPIKey: "   "},
			file:     `{"api_key":"SECRET-FILE"}`,
			wantName: sourceFile,
			wantKey:  "SECRET-FILE",
		},
		{
			name:     "should carry the base url stored beside the key",
			file:     `{"api_key":"SECRET-FILE","base_url":"https://proxy.example"}`,
			wantName: sourceFile,
			wantKey:  "SECRET-FILE",
			wantBase: "https://proxy.example",
		},
		{
			name:     "should report none when no source has a key",
			wantName: sourceNone,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := credentialFixture(t, tc.file, 0)

			settings := rootSettings{
				lookupEnv: lookupFrom(tc.env),
				credPath:  fixedPath(path),
				credStore: creds.NewStore(),
			}

			source, err := resolveKey(settings, &runFlags{apiKey: tc.apiKey})
			if err != nil {
				t.Fatalf("resolveKey returned an error: %v", err)
			}

			if source.name != tc.wantName {
				t.Errorf("source name = %q, want %q", source.name, tc.wantName)
			}

			if source.key != tc.wantKey {
				t.Error("the resolved key is not the one the source holds")
			}

			if source.baseURL != tc.wantBase {
				t.Errorf("base url = %q, want %q", source.baseURL, tc.wantBase)
			}
		})
	}
}

func TestKeySourceString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source keySource
		want   string
	}{
		{
			name:   "should name the flag",
			source: keySource{name: sourceFlag, key: "SECRET-FLAG"},
			want:   "source: flag",
		},
		{
			name:   "should name the file and its path",
			source: keySource{name: sourceFile, path: "/tmp/credentials.json", key: "SECRET-FILE"},
			want:   "source: file /tmp/credentials.json",
		},
		{
			name:   "should name none",
			source: keySource{name: sourceNone},
			want:   "source: none",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Through the verb a caller would reach for, since a struct printed with %v and no
			// String method would spell out every field including the key.
			if got := fmt.Sprintf("%+v", tc.source); got != tc.want {
				t.Errorf("formatted = %q, want %q", got, tc.want)
			}
		})
	}
}

func runAuth(t *testing.T, args []string, opts ...RootOption) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, append([]RootOption{
		WithStdin(strings.NewReader("")),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithClientFactory(func(context.Context, ...jev.Option) (*jev.Client, error) {
			return nil, errors.New("jev: no client should be built on this path")
		}),
	}, opts...)...)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}

func stubFactory(base string) func(context.Context, ...jev.Option) (*jev.Client, error) {
	return func(_ context.Context, extra ...jev.Option) (*jev.Client, error) {
		policy := jev.DefaultRetryPolicy()
		// A refused connection is retried by default, which would spend seconds proving a point
		// the first attempt already made.
		policy.MaxRetries = 0

		return jev.New(append([]jev.Option{
			jev.WithAPIKey("stub"),
			jev.WithBaseURL(base),
			jev.WithRetry(policy),
			jev.WithEnv(func(string) (string, bool) { return "", false }),
		}, extra...)...)
	}
}

func credentialFixture(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "credentials.json")
	if content == "" {
		return path
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the credential fixture: %v", err)
	}

	if mode == 0 {
		mode = 0o600
	}

	// WriteFile respects the umask, so the mode the case asked for is set explicitly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("setting the credential fixture mode: %v", err)
	}

	return path
}

func fixedPath(path string) func() (string, error) {
	return func() (string, error) {
		return path, nil
	}
}

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := env[name]

		return value, ok
	}
}

// assertNoSecret is the assertion the whole subcommand exists to satisfy. Every fixture key in this
// file carries the SECRET- prefix, so one scan covers the flag, the environment, the file and the
// prompt at once.
func assertNoSecret(t *testing.T, out, errOut string) {
	t.Helper()

	// Neither stream is printed on failure, since a failure message that quoted it would leak the
	// key the assertion is about.
	if strings.Contains(out, "SECRET-") {
		t.Error("stdout carries a key")
	}

	if strings.Contains(errOut, "SECRET-") {
		t.Error("stderr carries a key")
	}
}

func assertStored(t *testing.T, path, key, base string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the written credential file: %v", err)
	}

	var stored creds.File
	if decodeErr := json.Unmarshal(data, &stored); decodeErr != nil {
		t.Fatalf("the written credential file is not JSON: %v", decodeErr)
	}

	if stored.APIKey != key {
		t.Error("the stored key is not the one that was piped in")
	}

	if stored.BaseURL != base {
		t.Errorf("stored base url = %q, want %q", stored.BaseURL, base)
	}

	// base_url is omitted rather than written empty, so a file written without the flag carries no
	// key for a later reader to find.
	if base == "" && strings.Contains(string(data), "base_url") {
		t.Error("the written file carries a base_url key it was never given")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stating the written credential file: %v", err)
	}

	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("written mode = %o, want 600", info.Mode().Perm())
	}
}

func assertMissing(t *testing.T, path string, existed bool) {
	t.Helper()

	_, err := os.Stat(path)
	if existed {
		if err != nil {
			t.Errorf("the existing credential file was removed by a run that failed: %v", err)
		}

		return
	}

	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a failed run left a credential file behind, stat error = %v", err)
	}
}
