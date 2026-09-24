package cli

import (
	"bufio"
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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/jev"
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
			wantOut:  "provider: typesafe\nsource: env TYPESAFE_API_KEY\n",
			wantCode: ExitOK,
		},
		{
			name:     "should report the file",
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantPath: true,
			wantCode: ExitOK,
		},
		{
			name:     "should report none and exit 3",
			wantOut:  "provider: typesafe\nsource: none\n",
			wantCode: ExitAuth,
		},
		{
			name:     "should refuse a file others can reach and exit 3",
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			fileMode: 0o644,
			unixOnly: true,
			wantErr:  "is accessible by others, mode 644",
			wantCode: ExitAuth,
		},
		{
			name:     "should ignore a file others can reach when the environment has a key",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			fileMode: 0o644,
			unixOnly: true,
			wantOut:  "provider: typesafe\nsource: env TYPESAFE_API_KEY\n",
			wantCode: ExitOK,
		},
		{
			name:     "should prefer the environment over the file",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantOut:  "provider: typesafe\nsource: env TYPESAFE_API_KEY\n",
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
				want = "provider: typesafe\nsource: file " + path + "\n"
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

	providers := []struct {
		name     string
		args     []string
		env      map[string]string
		file     string
		wantOut  string
		wantPath bool
		wantCode int
	}{
		{
			name:     "should report the OpenRouter env var under ONESIE_PROVIDER",
			args:     []string{"auth", "status"},
			env:      map[string]string{"ONESIE_PROVIDER": "openrouter", "OPENROUTER_API_KEY": "SECRET-OR"},
			wantOut:  "provider: openrouter\nsource: env OPENROUTER_API_KEY\n",
			wantCode: ExitOK,
		},
		{
			name:     "should accept --provider before the subcommand",
			args:     []string{"--provider", "openrouter", "auth", "status"},
			env:      map[string]string{"OPENROUTER_API_KEY": "SECRET-OR"},
			wantOut:  "provider: openrouter\nsource: env OPENROUTER_API_KEY\n",
			wantCode: ExitOK,
		},
		{
			name:     "should accept --provider after the subcommand",
			args:     []string{"auth", "status", "--provider", "openrouter"},
			env:      map[string]string{"OPENROUTER_API_KEY": "SECRET-OR"},
			wantOut:  "provider: openrouter\nsource: env OPENROUTER_API_KEY\n",
			wantCode: ExitOK,
		},
		{
			name:     "should let --provider beat ONESIE_PROVIDER",
			args:     []string{"--provider", "typesafe", "auth", "status"},
			env:      map[string]string{"ONESIE_PROVIDER": "openrouter", jev.EnvAPIKey: "SECRET-TS"},
			wantOut:  "provider: typesafe\nsource: env TYPESAFE_API_KEY\n",
			wantCode: ExitOK,
		},
		{
			name:     "should never fall back to the TypeSafe key under openrouter",
			args:     []string{"--provider", "openrouter", "auth", "status"},
			env:      map[string]string{jev.EnvAPIKey: "SECRET-TS"},
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantOut:  "provider: openrouter\nsource: none\n",
			wantCode: ExitAuth,
		},
		{
			name:     "should read the openrouter entry from the file",
			args:     []string{"--provider", "openrouter", "auth", "status"},
			file:     `{"providers":{"openrouter":{"api_key":"SECRET-OR"},"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantPath: true,
			wantCode: ExitOK,
		},
		{
			name:     "should fail an unknown provider from the flag with exit 2",
			args:     []string{"--provider", "nope", "auth", "status"},
			wantCode: ExitUsage,
		},
		{
			name:     "should fail an unknown provider from ONESIE_PROVIDER with exit 2",
			args:     []string{"auth", "status"},
			env:      map[string]string{"ONESIE_PROVIDER": "nope"},
			wantCode: ExitUsage,
		},
	}

	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := credentialFixture(t, tc.file, 0)

			out, errOut, code := runAuth(t, tc.args,
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(tc.env)))

			assertNoSecret(t, out, errOut)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
			}

			want := tc.wantOut
			if tc.wantPath {
				want = "provider: openrouter\nsource: file " + path + "\n"
			}

			if out != want {
				t.Errorf("stdout = %q, want %q", out, want)
			}
		})
	}

	t.Run("should report a key read from the keychain", func(t *testing.T) {
		t.Parallel()

		path := credentialFixture(t, `{"providers":{"typesafe":{"store":"keychain"}}}`, 0)

		out, errOut, code := runAuth(t, []string{"auth", "status"},
			WithCredentialPath(fixedPath(path)),
			WithLookupEnv(lookupFrom(nil)),
			WithKeychain(workingKeychain(map[string]string{"typesafe": "SECRET-KC"})))

		assertNoSecret(t, out, errOut)

		if code != ExitOK {
			t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
		}

		if want := "provider: typesafe\nsource: keychain\n"; out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}
	})

	t.Run("should exit 3 when the file points at a keychain item that is gone", func(t *testing.T) {
		t.Parallel()

		path := credentialFixture(t, `{"providers":{"typesafe":{"store":"keychain"}}}`, 0)

		out, errOut, code := runAuth(t, []string{"auth", "status"},
			WithCredentialPath(fixedPath(path)),
			WithLookupEnv(lookupFrom(nil)),
			WithKeychain(workingKeychain(nil)))

		assertNoSecret(t, out, errOut)

		if code != ExitAuth {
			t.Errorf("exit code = %d, want %d", code, ExitAuth)
		}

		if !strings.Contains(errOut, "the keychain holds no key for this provider") {
			t.Errorf("stderr = %q, want it to say the keychain has no key", errOut)
		}
	})
}

func TestAuthClear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
	}{
		{
			name: "should delete the credential file",
			file: `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
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

	entries := []struct {
		name     string
		args     []string
		file     string
		wantFile string
	}{
		{
			name:     "should remove only the chosen provider's entry",
			args:     []string{"--provider", "openrouter", "auth", "clear"},
			file:     `{"providers":{"openrouter":{"api_key":"SECRET-OR"},"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantFile: `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
		},
		{
			name:     "should leave the file alone when the provider has no entry",
			args:     []string{"--provider", "openrouter", "auth", "clear"},
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantFile: `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
		},
		{
			name: "should delete the file once the last entry is removed",
			args: []string{"--provider", "openrouter", "auth", "clear"},
			file: `{"providers":{"openrouter":{"api_key":"SECRET-OR"}}}`,
		},
	}

	for _, tc := range entries {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := credentialFixture(t, tc.file, 0)

			out, errOut, code := runAuth(t, tc.args,
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(nil)))

			assertNoSecret(t, out, errOut)

			if code != ExitOK {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			data, err := os.ReadFile(path)
			if tc.wantFile == "" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("the credential file is still there, read error = %v", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("reading the credential file: %v", err)
			}

			if strings.TrimSpace(string(data)) != tc.wantFile {
				t.Error("the credential file does not hold the expected entries")
			}
		})
	}

	t.Run("should remove the keychain item along with the entry", func(t *testing.T) {
		t.Parallel()

		path := credentialFixture(t, `{"providers":{"typesafe":{"store":"keychain"}}}`, 0)
		keychain := workingKeychain(map[string]string{"typesafe": "SECRET-KC"})

		out, errOut, code := runAuth(t, []string{"auth", "clear"},
			WithCredentialPath(fixedPath(path)),
			WithLookupEnv(lookupFrom(nil)),
			WithKeychain(keychain))

		assertNoSecret(t, out, errOut)

		if code != ExitOK {
			t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
		}

		if keychain.holds("typesafe") {
			t.Error("the keychain still holds the key")
		}

		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the credential file is still there, stat error = %v", err)
		}
	})
}

func TestAuthSet(t *testing.T) {
	t.Parallel()

	errNoChmod := errors.New("the test chmod failed")

	tests := []struct {
		name         string
		args         []string
		stdin        string
		tty          bool
		secret       string
		existing     string
		existingMode os.FileMode
		unixOnly     bool
		chmodFails   bool
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
			wantErr:    "onesie: auth set: the key is empty",
			wantNoFile: true,
			wantCode:   ExitUsage,
		},
		{
			name:       "should reject a whitespace only key",
			stdin:      "   \n",
			wantErr:    "onesie: auth set: the key is empty",
			wantNoFile: true,
			wantCode:   ExitUsage,
		},
		{
			name:  "should reject a metadata line after the secret",
			stdin: "SECRET-STDIN\nlogin: someone\n",
			wantErr: "onesie: auth set: stdin carries more than one line. " +
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
			existing: `{"providers":{"typesafe":{"api_key":"SECRET-OLD","base_url":"https://old.example"}}}`,
			wantKey:  "SECRET-STDIN",
			wantCode: ExitOK,
		},
		{
			name:         "should overwrite an existing file others can reach",
			stdin:        "SECRET-STDIN\n",
			existing:     `{"providers":{"typesafe":{"api_key":"SECRET-OLD"}}}`,
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
		{
			// The store is injected rather than the filesystem steered, since no temporary
			// directory refuses a chmod. Only this case reaches the warning branch, so the whole
			// of stderr is compared below rather than searched.
			name:       "should warn when the mode cannot be set and still store the key",
			args:       []string{"--file"},
			stdin:      "SECRET-STDIN\n",
			chmodFails: true,
			wantKey:    "SECRET-STDIN",
			wantCode:   ExitOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode does not apply")
			}

			path := credentialFixture(t, tc.existing, tc.existingMode)

			opts := []RootOption{
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(nil)),
				WithStdin(strings.NewReader(tc.stdin)),
				WithStdinTTY(tc.tty),
				WithSecretReader(func() (string, error) { return tc.secret, nil }),
			}

			if tc.chmodFails {
				opts = append(opts, WithCredentialStore(creds.NewStore(
					creds.WithChmod(func(string, os.FileMode) error { return errNoChmod }))))
			}

			out, errOut, code := runAuth(t, append([]string{"auth", "set"}, tc.args...), opts...)

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

			if !strings.Contains(errOut, "onesie: writing "+path) {
				t.Errorf("stderr = %q, want it to name the path being written", errOut)
			}

			// Section 11 quotes the warning without the onesie prefix, which the word warning already
			// stands in for. Compared whole, so a second prefix in front of it fails here.
			if tc.chmodFails {
				want := "onesie: writing " + path + "\n" +
					"warning: could not set mode 600 on " + path +
					". The key is not protected by the filesystem\n"
				if errOut != want {
					t.Errorf("stderr = %q, want %q", errOut, want)
				}
			}

			if tc.tty && !strings.Contains(errOut, "API key: ") {
				t.Errorf("stderr = %q, want it to carry the prompt", errOut)
			}

			assertStored(t, path, tc.wantKey, tc.wantBase)
		})
	}

	providers := []struct {
		name     string
		args     []string
		existing string
		wantFile string
		wantErr  string
	}{
		{
			name:     "should add the chosen provider's entry and keep the others",
			args:     []string{"--provider", "openrouter", "auth", "set"},
			existing: `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantFile: `{"providers":{"openrouter":{"api_key":"SECRET-OR"},"typesafe":{"api_key":"SECRET-FILE"}}}`,
		},
		{
			name:     "should store a base url in the chosen provider's entry",
			args:     []string{"--provider", "openrouter", "auth", "set", "--base-url", "https://proxy.example"},
			wantFile: `{"providers":{"openrouter":{"api_key":"SECRET-OR","base_url":"https://proxy.example"}}}`,
		},
		{
			name:     "should replace a file it cannot read and say so",
			args:     []string{"--provider", "openrouter", "auth", "set"},
			existing: `not json`,
			wantFile: `{"providers":{"openrouter":{"api_key":"SECRET-OR"}}}`,
			wantErr:  "which could not be read. Any other provider's key in it was dropped",
		},
	}

	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := credentialFixture(t, tc.existing, 0)

			out, errOut, code := runAuth(t, tc.args,
				WithStdin(strings.NewReader("SECRET-OR\n")),
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(nil)))

			assertNoSecret(t, out, errOut)

			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the credential file: %v", err)
			}

			if strings.TrimSpace(string(data)) != tc.wantFile {
				t.Error("the credential file does not hold the expected entries")
			}

			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}
		})
	}

	keychains := []struct {
		name         string
		args         []string
		existing     string
		keychain     map[string]string
		wantFile     string
		wantKeychain bool
		wantErr      string
	}{
		{
			name:         "should store the key in the keychain and point the file at it",
			args:         []string{"auth", "set"},
			wantFile:     `{"providers":{"typesafe":{"store":"keychain"}}}`,
			wantKeychain: true,
			wantErr:      "onesie: stored the typesafe key in the OS keychain",
		},
		{
			name:         "should keep a base url beside a keychain entry",
			args:         []string{"auth", "set", "--base-url", "https://proxy.example"},
			wantFile:     `{"providers":{"typesafe":{"store":"keychain","base_url":"https://proxy.example"}}}`,
			wantKeychain: true,
		},
		{
			name:     "should store the key in the file under --file",
			args:     []string{"auth", "set", "--file"},
			wantFile: `{"providers":{"typesafe":{"api_key":"SECRET-NEW"}}}`,
		},
		{
			name:     "should remove the keychain copy when a key moves to the file",
			args:     []string{"auth", "set", "--file"},
			existing: `{"providers":{"typesafe":{"store":"keychain"}}}`,
			keychain: map[string]string{"typesafe": "SECRET-OLD"},
			wantFile: `{"providers":{"typesafe":{"api_key":"SECRET-NEW"}}}`,
		},
	}

	for _, tc := range keychains {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := credentialFixture(t, tc.existing, 0)
			keychain := workingKeychain(tc.keychain)

			out, errOut, code := runAuth(t, tc.args,
				WithStdin(strings.NewReader("SECRET-NEW\n")),
				WithCredentialPath(fixedPath(path)),
				WithLookupEnv(lookupFrom(nil)),
				WithKeychain(keychain))

			assertNoSecret(t, out, errOut)

			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the credential file: %v", err)
			}

			if strings.TrimSpace(string(data)) != tc.wantFile {
				t.Error("the credential file does not hold the expected entries")
			}

			if keychain.holds("typesafe") != tc.wantKeychain {
				t.Errorf("keychain holds the key = %v, want %v", keychain.holds("typesafe"), tc.wantKeychain)
			}

			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}
		})
	}

	t.Run("should fall back to the file and say so when there is no keychain", func(t *testing.T) {
		t.Parallel()

		path := credentialFixture(t, "", 0)

		out, errOut, code := runAuth(t, []string{"auth", "set"},
			WithStdin(strings.NewReader("SECRET-NEW\n")),
			WithCredentialPath(fixedPath(path)),
			WithLookupEnv(lookupFrom(nil)))

		assertNoSecret(t, out, errOut)

		if code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
		}

		want := "warning: could not store the key in the OS keychain: no keychain in tests. The key is in " +
			path + " instead"
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr = %q, want it to contain %q", errOut, want)
		}

		assertStored(t, path, "SECRET-NEW", "")
	})
}

func TestFirstLine(t *testing.T) {
	t.Parallel()

	// The first length bufio.Scanner refuses to hand back as a token.
	const overCap = bufio.MaxScanTokenSize

	tests := []struct {
		name    string
		stdin   string
		wantKey string
		wantErr string
	}{
		{
			name:    "should return the longest single line the scanner can hand back",
			stdin:   strings.Repeat("A", overCap-1),
			wantKey: strings.Repeat("A", overCap-1),
		},
		{
			name:    "should report the read failure for one line over the cap",
			stdin:   strings.Repeat("A", overCap),
			wantErr: "onesie: reading the key from stdin: bufio.Scanner: token too long",
		},
		{
			// The tail past the cap is blank, so nothing here is a second line. Before the read
			// failure was reported this case was rejected as one.
			name:    "should report the read failure when blank lines follow the long one",
			stdin:   strings.Repeat("A", overCap) + "\n\n   \n",
			wantErr: "onesie: reading the key from stdin: bufio.Scanner: token too long",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, err := firstLine(strings.NewReader(tc.stdin))

			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Errorf("error = %v, want %q", err, tc.wantErr)
				}

				if key != "" {
					t.Errorf("key length = %d, want a rejected read to hand back nothing", len(key))
				}

				return
			}

			if err != nil {
				t.Fatalf("firstLine returned an error: %v", err)
			}

			// Lengths rather than the keys, since a failure message carrying 64KiB helps nobody.
			if key != tc.wantKey {
				t.Errorf("key length = %d, want %d", len(key), len(tc.wantKey))
			}
		})
	}
}

func TestAuthTest(t *testing.T) {
	t.Parallel()

	const listing = `{"models":[` +
		`{"name":"jev-latest","description":"alias","release_date":"2026-08-01"},` +
		`{"name":"onesie-1.13.0","description":"current","release_date":"2026-08-01"}]}`

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
		wantErr   string
		wantCode  int
	}{
		{
			// The message is jev.New's. auth test carries no copy of it, so the two cannot drift.
			name:     "should exit 2 when no source holds a key",
			wantErr:  "onesie: no API key. Pass --api-key or set " + jev.EnvAPIKey,
			wantCode: ExitUsage,
		},
		{
			name:     "should print the source and the model count",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			response: listing,
			wantOut:  "provider: typesafe\nsource: env TYPESAFE_API_KEY\nmodels: 2\n",
			wantCode: ExitOK,
		},
		{
			name:     "should reach the base url stored beside the key",
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
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
			name:     "should exit 3 and suggest credits on a 402",
			env:      map[string]string{"ONESIE_PROVIDER": "openrouter", "OPENROUTER_API_KEY": "SECRET-OR"},
			status:   http.StatusPaymentRequired,
			response: `{"error":{"message":"Insufficient credits","code":402}}`,
			wantCode: ExitAuth,
			wantErr:  "onesie: 402 Insufficient credits. Add credits to the account this key belongs to",
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
				file = `{"providers":{"typesafe":{"api_key":"SECRET-FILE","base_url":"` + base + `"}}}`
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
				want = "provider: typesafe\nsource: file " + path + "\nmodels: 2\n"
			}

			if out != want {
				t.Errorf("stdout = %q, want %q", out, want)
			}

			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
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
			source := keySource{name: sourceFile, provider: jev.TypeSafe(), key: "SECRET-FILE", baseURL: stored}

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

	t.Run("should ignore TYPESAFE_BASE_URL under openrouter and keep the stored base url", func(t *testing.T) {
		t.Parallel()

		settings := rootSettings{lookupEnv: lookupFrom(map[string]string{jev.EnvBaseURL: "https://typesafe.example"})}
		source := keySource{
			name: sourceFile, provider: jev.OpenRouter(), key: "SECRET-FILE", baseURL: "https://proxy.example",
		}

		if got := len(storedOptions(settings, &runFlags{}, source)); got != 2 {
			t.Errorf("storedOptions returned %d options, want the key and the stored base url", got)
		}
	})
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
			wantErr:  `onesie: unknown command "nonsense" for "onesie auth"`,
			wantCode: ExitUsage,
		},
		{
			name: "should reject a question given to auth set",
			args: []string{"auth", "set", "is this urgent"},
			wantErr: "onesie: auth set takes no question or state. " +
				"It reads the key from a prompt or stdin",
			wantCode: ExitUsage,
		},
		{
			name: "should reject a question given to auth status",
			args: []string{"auth", "status", "is this urgent"},
			wantErr: "onesie: auth status takes no question or state. " +
				"It reports which source holds the key",
			wantCode: ExitUsage,
		},
		{
			name: "should reject a question given to auth test",
			args: []string{"auth", "test", "is this urgent"},
			wantErr: "onesie: auth test takes no question or state. " +
				"It calls the models endpoint with the resolved key",
			wantCode: ExitUsage,
		},
		{
			name: "should reject a question given to auth clear",
			args: []string{"auth", "clear", "is this urgent"},
			wantErr: "onesie: auth clear takes no question or state. " +
				"It removes the provider's key from the credential file",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject a root question flag on a subcommand",
			args:     []string{"auth", "set", "--pick", "a,b"},
			wantErr:  "onesie: unknown flag: --pick",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject a root question flag placed before the subcommand",
			args:     []string{"--pick", "a,b", "auth", "set"},
			wantErr:  "onesie: unknown flag: --pick",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject an output flag on a subcommand",
			args:     []string{"auth", "status", "-o", "json"},
			wantErr:  "onesie: unknown shorthand flag: 'o' in -o",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject a state flag on a subcommand",
			args:     []string{"auth", "clear", "--state", "x"},
			wantErr:  "onesie: unknown flag: --state",
			wantCode: ExitUsage,
		},
		{
			name:     "should keep --base-url off the subcommands that store nothing",
			args:     []string{"auth", "status", "--base-url", "https://proxy.example"},
			wantErr:  "onesie: unknown flag: --base-url",
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
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantName: sourceFlag,
			wantKey:  "SECRET-FLAG",
		},
		{
			name:     "should prefer the environment over the file",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantName: sourceEnv,
			wantKey:  "SECRET-ENV",
		},
		{
			name:     "should ignore a blank environment variable",
			env:      map[string]string{jev.EnvAPIKey: "   "},
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`,
			wantName: sourceFile,
			wantKey:  "SECRET-FILE",
		},
		{
			name:     "should carry the base url stored beside the key",
			file:     `{"providers":{"typesafe":{"api_key":"SECRET-FILE","base_url":"https://proxy.example"}}}`,
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
		{
			name:   "should name the typesafe env var",
			source: keySource{name: sourceEnv, provider: jev.TypeSafe(), key: "SECRET-ENV"},
			want:   "source: env TYPESAFE_API_KEY",
		},
		{
			name:   "should name the openrouter env var",
			source: keySource{name: sourceEnv, provider: jev.OpenRouter(), key: "SECRET-ENV"},
			want:   "source: env OPENROUTER_API_KEY",
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

func TestCredentialPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		variable string
		// nested names the directories creds.Path appends to the one the variable holds.
		nested []string
	}{
		{
			name:     "should resolve the credential file from ONESIE_CONFIG_DIR",
			variable: "ONESIE_CONFIG_DIR",
		},
		{
			name:     "should resolve the credential file from XDG_CONFIG_HOME",
			variable: "XDG_CONFIG_HOME",
			nested:   []string{"onesie"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			holding := filepath.Join(append([]string{dir}, tc.nested...)...)
			if err := os.MkdirAll(holding, 0o700); err != nil {
				t.Fatalf("creating the config directory: %v", err)
			}

			path := filepath.Join(holding, "credentials.json")
			if err := os.WriteFile(path, []byte(`{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`), 0o600); err != nil {
				t.Fatalf("writing the credential fixture: %v", err)
			}

			// Only the environment is replaced. WithCredentialPath would stand in for the resolver
			// under test, and with it the ordering in NewRootCmd that hands the resolver the
			// injected lookup rather than the real environment.
			out, errOut, code := runAuth(t, []string{"auth", "status"},
				WithLookupEnv(lookupFrom(map[string]string{tc.variable: dir})))

			assertNoSecret(t, out, errOut)

			if code != ExitOK {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			if want := "provider: typesafe\nsource: file " + path + "\n"; out != want {
				t.Errorf("stdout = %q, want %q", out, want)
			}
		})
	}
}

func TestNewRootCmdFileDrivenClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// fileBase names the server the credential file points at, WANTED or UNWANTED, and is
		// empty for a file carrying no base_url. Every arg and environment value is substituted
		// the same way, since the addresses are only known once the case is running.
		fileBase string
		fileKey  string
		fileMode os.FileMode
		unixOnly bool
		env      map[string]string
		args     []string
		status   int
		wantCode int
		wantAuth string
		wantErr  string
		wanted   int
	}{
		{
			name:     "should take the key and the base url from the file",
			fileKey:  "SECRET-FILE",
			fileBase: "WANTED",
			args:     []string{"is this urgent", "-r"},
			wantCode: ExitOK,
			wantAuth: "Bearer SECRET-FILE",
			wanted:   1,
		},
		{
			// The mode is the assertion. A run that opened this file would exit 3 rather than
			// reach either server, so passing proves the file was never opened at all.
			name:     "should leave a file others can reach unopened when the environment has a key",
			fileKey:  "SECRET-FILE",
			fileBase: "UNWANTED",
			fileMode: 0o644,
			unixOnly: true,
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			args:     []string{"is this urgent", "-r", "--base-url", "WANTED"},
			wantCode: ExitOK,
			wantAuth: "Bearer SECRET-ENV",
			wanted:   1,
		},
		{
			name:     "should send the refused environment key rather than falling back to the file",
			fileKey:  "SECRET-FILE",
			fileBase: "UNWANTED",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			args:     []string{"is this urgent", "-r", "--base-url", "WANTED"},
			status:   http.StatusUnauthorized,
			wantCode: ExitAuth,
			wantAuth: "Bearer SECRET-ENV",
			wanted:   1,
		},
		{
			name:     "should refuse a file others can reach when no earlier source has a key",
			fileKey:  "SECRET-FILE",
			fileBase: "WANTED",
			fileMode: 0o644,
			unixOnly: true,
			args:     []string{"is this urgent", "-r"},
			wantCode: ExitAuth,
			wantErr:  "is accessible by others, mode 644",
		},
		{
			name: "should send the OpenRouter key under ONESIE_PROVIDER",
			env: map[string]string{
				"ONESIE_PROVIDER": "openrouter", "OPENROUTER_API_KEY": "SECRET-OR", jev.EnvAPIKey: "SECRET-ENV",
			},
			args:     []string{"is this urgent", "-r", "--base-url", "WANTED"},
			wantCode: ExitOK,
			wantAuth: "Bearer SECRET-OR",
			wanted:   1,
		},
		{
			name:     "should not send the TypeSafe file key under openrouter",
			fileKey:  "SECRET-FILE",
			fileBase: "UNWANTED",
			args:     []string{"is this urgent", "-r", "--provider", "openrouter", "--base-url", "WANTED"},
			wantCode: ExitUsage,
			wantErr:  "OPENROUTER_API_KEY",
		},
		{
			name:     "should let --api-key outrank the file",
			fileKey:  "SECRET-FILE",
			fileBase: "UNWANTED",
			args: []string{
				"is this urgent", "-r", "--api-key", "SECRET-FLAG", "--base-url", "WANTED",
			},
			wantCode: ExitOK,
			wantAuth: "Bearer SECRET-FLAG",
			wanted:   1,
		},
		{
			name:     "should let --api-key outrank the environment and the file",
			fileKey:  "SECRET-FILE",
			fileBase: "UNWANTED",
			env:      map[string]string{jev.EnvAPIKey: "SECRET-ENV"},
			args: []string{
				"is this urgent", "-r", "--api-key", "SECRET-FLAG", "--base-url", "WANTED",
			},
			wantCode: ExitOK,
			wantAuth: "Bearer SECRET-FLAG",
			wanted:   1,
		},
		{
			name:     "should let --base-url outrank the file's",
			fileKey:  "SECRET-FILE",
			fileBase: "UNWANTED",
			args:     []string{"is this urgent", "-r", "--base-url", "WANTED"},
			wantCode: ExitOK,
			wantAuth: "Bearer SECRET-FILE",
			wanted:   1,
		},
		{
			// A whitespace only value is nothing to resolveKey and to storedOptions, so the file's
			// base URL is what the request goes to. Passing the flag on raw would install an
			// option jev.New trims back to empty, which reads as a flag that was honoured.
			name:     "should ignore a whitespace only --base-url and keep the file's",
			fileKey:  "SECRET-FILE",
			fileBase: "WANTED",
			args:     []string{"is this urgent", "-r", "--base-url", "   "},
			wantCode: ExitOK,
			wantAuth: "Bearer SECRET-FILE",
			wanted:   1,
		},
		{
			name:     "should let TYPESAFE_BASE_URL outrank the file's",
			fileKey:  "SECRET-FILE",
			fileBase: "UNWANTED",
			env:      map[string]string{jev.EnvBaseURL: "WANTED"},
			args:     []string{"is this urgent", "-r"},
			wantCode: ExitOK,
			wantAuth: "Bearer SECRET-FILE",
			wanted:   1,
		},
		{
			name:     "should report no key when the file is absent",
			args:     []string{"is this urgent", "-r", "--base-url", "WANTED"},
			wantCode: ExitUsage,
			wantErr:  "onesie: no API key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.unixOnly && runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode check does not apply")
			}

			wanted, wantedAuths := recordingServer(t, tc.status)
			unwanted, unwantedAuths := recordingServer(t, 0)

			urls := map[string]string{"WANTED": wanted.URL, "UNWANTED": unwanted.URL}

			env := map[string]string{
				"ONESIE_CONFIG_DIR": credentialDir(
					t, credentialContent(t, tc.fileKey, urls[tc.fileBase]), tc.fileMode),
			}

			for name, value := range tc.env {
				env[name] = substituted(value, urls)
			}

			args := make([]string, 0, len(tc.args))
			for _, arg := range tc.args {
				args = append(args, substituted(arg, urls))
			}

			out, errOut, code := runCredentialFile(t, args, "the server is down", env)

			// First, because every other assertion below would pass on output that had the key
			// spliced into it.
			assertNoSecret(t, out, errOut)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
			}

			if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr missing %q\ngot:\n%s", tc.wantErr, errOut)
			}

			auths := wantedAuths()
			if len(auths) != tc.wanted {
				t.Errorf("the wanted base url took %d requests, want %d", len(auths), tc.wanted)
			}

			for _, auth := range auths {
				// The header is compared rather than reported. Naming what it held would print the
				// key the whole subcommand exists to keep out of every stream.
				if auth != tc.wantAuth {
					t.Error("a request carried a key from the wrong source")
				}
			}

			if got := len(unwantedAuths()); got != 0 {
				t.Errorf("the unwanted base url took %d requests, want 0", got)
			}
		})
	}

	t.Run("should send the keychain key to the base url stored beside it", func(t *testing.T) {
		t.Parallel()

		srv, auths := recordingServer(t, 0)
		path := credentialFixture(t, `{"providers":{"typesafe":{"store":"keychain","base_url":"`+srv.URL+`"}}}`, 0)

		var out, errOut bytes.Buffer

		root := NewRootCmd(
			BuildInfo{Version: "1.2.3"},
			WithStdin(strings.NewReader("the server is down")),
			WithStdinTTY(false),
			WithStdoutTTY(false),
			WithLookupEnv(lookupFrom(nil)),
			WithCredentialPath(fixedPath(path)),
			WithKeychain(workingKeychain(map[string]string{"typesafe": "SECRET-KC"})),
		)

		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{"is this urgent", "-r"})

		if code := Execute(t.Context(), root); code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut.String())
		}

		if got := auths(); len(got) != 1 || got[0] != "Bearer SECRET-KC" {
			t.Error("the stored base url did not receive exactly one request carrying the keychain key")
		}
	})
}

func TestNewRootCmdDryRunWithACredentialFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  []string
		stdin string
	}{
		{
			name:  "should not open the file for --print-request",
			args:  []string{"is this urgent", "--print-request"},
			stdin: "the server is down",
		},
		{
			name: "should not open the file for --print-questions",
			args: []string{"--ask", "urgent=is this urgent", "--print-questions"},
		},
		{
			name:  "should not open the file for -i request --print-request",
			args:  []string{"-i", "request", "--print-request"},
			stdin: `{"state":"the server is down"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode check does not apply")
			}

			// Mode 0644 is the assertion. Section 16.1 opens the file only when the run needs a
			// key, so a dry run that opened this one would exit 3 instead of writing its body.
			dir := credentialDir(t, `{"providers":{"typesafe":{"api_key":"SECRET-FILE"}}}`, 0o644)

			out, errOut, code := runCredentialFile(t, tc.args, tc.stdin,
				map[string]string{"ONESIE_CONFIG_DIR": dir})

			assertNoSecret(t, out, errOut)

			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			if errOut != "" {
				t.Errorf("stderr = %q, want nothing", errOut)
			}

			if out == "" {
				t.Error("stdout is empty, want the dry run's body")
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
			return nil, errors.New("onesie: no client should be built on this path")
		}),
		// Never the real keychain. A test that wants one working passes its own.
		WithKeychain(noKeychain()),
	}, opts...)...)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}

func runCredentialFile(
	t *testing.T,
	args []string,
	stdin string,
	env map[string]string,
) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	// Neither WithClientFactory nor WithCredentialPath, so the production resolver and the
	// production factory both run. The injected lookup carrying ONESIE_CONFIG_DIR is the only thing
	// standing between the resolver and the developer's own credential file.
	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(env)),
		WithKeychain(noKeychain()),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}

// recordingServer answers one question and hands back every Authorization header it was sent, so a
// case can name which source the key came from without printing it.
func recordingServer(t *testing.T, status int) (*httptest.Server, func() []string) {
	t.Helper()

	var mu sync.Mutex

	var auths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")

		body := `{"model":"onesie-1.13.0","answers":{"answer":{"type":"noul","noul":0.5}}}`
		if status != 0 {
			w.WriteHeader(status)

			body = `{"error":{"message":"the key was refused"}}`
		}

		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))

	t.Cleanup(srv.Close)

	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return append([]string(nil), auths...)
	}
}

func credentialContent(t *testing.T, key, baseURL string) string {
	t.Helper()

	if key == "" {
		return ""
	}

	encoded, err := json.Marshal(creds.File{Providers: map[string]creds.Entry{"typesafe": {APIKey: key, BaseURL: baseURL}}})
	if err != nil {
		t.Fatalf("encoding the credential fixture: %v", err)
	}

	return string(encoded)
}

func substituted(value string, urls map[string]string) string {
	if url, ok := urls[value]; ok {
		return url
	}

	return value
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

	return filepath.Join(credentialDir(t, content, mode), "credentials.json")
}

// credentialDir returns the directory ONESIE_CONFIG_DIR names, holding the file when there is content
// for one, so a case can exercise the production path resolver rather than stand in for it.
func credentialDir(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()

	dir := t.TempDir()
	if content == "" {
		return dir
	}

	path := filepath.Join(dir, "credentials.json")
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

	return dir
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

	entry := stored.Providers["typesafe"]

	if entry.APIKey != key {
		t.Error("the stored key is not the one that was piped in")
	}

	if entry.BaseURL != base {
		t.Errorf("stored base url = %q, want %q", entry.BaseURL, base)
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

type fakeKeychain struct {
	mu          sync.Mutex
	items       map[string]string
	unavailable bool
}

func noKeychain() *fakeKeychain {
	return &fakeKeychain{items: map[string]string{}, unavailable: true}
}

func workingKeychain(items map[string]string) *fakeKeychain {
	if items == nil {
		items = map[string]string{}
	}

	return &fakeKeychain{items: items}
}

func (k *fakeKeychain) Get(provider string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.unavailable {
		return "", &creds.KeychainError{Op: "read the key", Err: errors.New("no keychain in tests")}
	}

	key, ok := k.items[provider]
	if !ok {
		return "", &creds.KeychainError{Op: "read the key", Err: creds.ErrKeychainMissing}
	}

	return key, nil
}

func (k *fakeKeychain) Set(provider, key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.unavailable {
		return &creds.KeychainError{Op: "store the key", Err: errors.New("no keychain in tests")}
	}

	k.items[provider] = key

	return nil
}

func (k *fakeKeychain) Delete(provider string) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.unavailable {
		return &creds.KeychainError{Op: "remove the key", Err: errors.New("no keychain in tests")}
	}

	delete(k.items, provider)

	return nil
}

func (k *fakeKeychain) holds(provider string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	_, ok := k.items[provider]

	return ok
}
