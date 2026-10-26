package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

const (
	key15 = "SECRET-01234567"
	key16 = "SECRET-012345678"

	keychainNone         = "none"
	keychainTimeout      = "timeout"
	keychainStalled      = "stalled"
	keychainUnreadable   = "unreadable"
	keychainUndeletable  = "undeletable"
	keychainUnrestorable = "unrestorable"
)

func TestNewProfileCmd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		env      map[string]string
		wantOut  string
		wantErr  string
		wantCode int
	}{
		{
			name:     "should print the subcommands when given no subcommand",
			args:     []string{"profile"},
			wantOut:  "Available Commands:",
			wantCode: ExitOK,
		},
		{
			name:     "should say in the create help to pipe a password manager entry through head -n 1",
			args:     []string{"profile", "create", "--help"},
			wantOut:  "pass show openrouter | head -n 1 | onesie profile create work --provider openrouter",
			wantCode: ExitOK,
		},
		{
			name:     "should say in the create help that a name is not none",
			args:     []string{"profile", "create", "--help"},
			wantOut:  "does not start with -, and is not none",
			wantCode: ExitOK,
		},
		{
			name:     "should say in the create help that a run on a plain http base url needs --allow-http",
			args:     []string{"profile", "create", "--help"},
			wantOut:  "Every run on the profile then needs --allow-http too",
			wantCode: ExitOK,
		},
		{
			name:     "should say in the test help that openrouter also reads /v1/key",
			args:     []string{"profile", "test", "--help"},
			wantOut:  "On openrouter profile test also reads /v1/key under the base URL",
			wantCode: ExitOK,
		},
		{
			name:     "should reject an unknown subcommand",
			args:     []string{"profile", "nonsense"},
			wantErr:  `onesie: unknown command "nonsense" for "onesie profile"`,
			wantCode: ExitUsage,
		},
		{
			name:     "should reject create with no name",
			args:     []string{"profile", "create", "--provider", "typesafe"},
			wantErr:  "onesie: profile create takes one profile name",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject show with two names",
			args:     []string{"profile", "show", "a", "b"},
			wantErr:  "onesie: profile show takes at most one profile name",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject list with a name",
			args:     []string{"profile", "list", "a"},
			wantErr:  "onesie: profile list takes no argument",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse to run under --mock",
			args:     []string{"profile", "list", "--mock", "answers.json"},
			wantErr:  "onesie: profile reads and writes real configuration, so it does not run under --mock. Drop it",
			wantCode: ExitUsage,
		},
		{
			name: "should refuse to run under ONESIE_MOCK",
			args: []string{"profile", "list"},
			env:  map[string]string{"ONESIE_MOCK": "answers.json"},
			wantErr: "onesie: profile reads and writes real configuration, so it does not run under ONESIE_MOCK. " +
				"Unset it",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --profile on create",
			args:     []string{"profile", "create", "work", "--provider", "typesafe", "--profile", "x"},
			wantErr:  "onesie: --profile does not apply to profile create",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --profile on update",
			args:     []string{"profile", "update", "work", "--model", "m", "--profile", "x"},
			wantErr:  "onesie: --profile does not apply to profile update",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --profile on delete",
			args:     []string{"--profile", "x", "profile", "delete", "work"},
			wantErr:  "onesie: --profile does not apply to profile delete",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --provider on delete",
			args:     []string{"profile", "delete", "work", "--provider", "typesafe"},
			wantErr:  "onesie: --provider does not apply to profile delete",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --profile on use",
			args:     []string{"profile", "use", "work", "--profile", "x"},
			wantErr:  "onesie: --profile does not apply to profile use",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --provider on use",
			args:     []string{"profile", "use", "work", "--provider", "typesafe"},
			wantErr:  "onesie: --provider does not apply to profile use",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --profile on list",
			args:     []string{"profile", "list", "--profile", "x"},
			wantErr:  "onesie: --profile does not apply to profile list",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --provider on list",
			args:     []string{"profile", "list", "--provider", "typesafe"},
			wantErr:  "onesie: --provider does not apply to profile list",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --profile on show with a name",
			args:     []string{"profile", "show", "work", "--profile", "x"},
			wantErr:  "onesie: --profile does not apply to profile show",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --provider on show with a name",
			args:     []string{"profile", "show", "work", "--provider", "typesafe"},
			wantErr:  "onesie: --provider does not apply to profile show",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --profile on test with a name",
			args:     []string{"profile", "test", "work", "--profile", "x"},
			wantErr:  "onesie: --profile does not apply to profile test",
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --provider on test with a name",
			args:     []string{"profile", "test", "work", "--provider", "typesafe"},
			wantErr:  "onesie: --provider does not apply to profile test",
			wantCode: ExitUsage,
		},
		{
			name:     "should reject a root question flag on a subcommand",
			args:     []string{"profile", "list", "--pick", "a,b"},
			wantErr:  "onesie: unknown flag: --pick",
			wantCode: ExitUsage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runInConfig(t, t.TempDir(), tc.args, "", tc.env)

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

func TestProfileCreate(t *testing.T) {
	t.Parallel()

	failingWrite := []creds.StoreOption{creds.WithCreateTemp(func(string, string) (*os.File, error) {
		return nil, errors.New("the disk is full")
	})}
	failingChmod := []creds.StoreOption{creds.WithChmod(func(string, os.FileMode) error {
		return errors.New("the test chmod failed")
	})}

	const (
		existing  = `{"default":"home","profiles":{"home":{"provider":"typesafe","api_key":"SECRET-HOME"}}}`
		takenItem = `{"default":"home","profiles":{"home":{"provider":"typesafe","store":"keychain",` +
			`"account":"work@{hash}"}}}`
	)

	tests := []profileCase{
		{
			name:  "should store the key in the keychain under the profile's own item",
			args:  []string{"profile", "create", "work", "--provider", "openrouter"},
			stdin: key16 + "\n",
			wantErr: []string{
				"stored the key in the OS keychain as work@{hash}",
				"onesie: 'work' is the default profile, since it is the first",
			},
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
				`"account":"work@{hash}","key_end":"5678"}}}`,
			wantItems: map[string]string{"work@{hash}": key16},
		},
		{
			name:  "should leave key_end out for a key under 16 characters",
			args:  []string{"profile", "create", "work", "--provider", "openrouter"},
			stdin: key15 + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
				`"account":"work@{hash}"}}}`,
			wantItems: map[string]string{"work@{hash}": key15},
		},
		{
			name:      "should store the key in the file under --file",
			args:      []string{"profile", "create", "work", "--provider", "typesafe", "--file"},
			stdin:     key16 + "\n",
			wantFile:  `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"` + key16 + `"}}}`,
			wantItems: map[string]string{},
		},
		{
			name: "should keep the model and the account id",
			args: []string{
				"profile", "create", "cf", "--provider", "cloudflare", "--model", "clef-flash",
				"--account-id", testAccountID, "--file",
			},
			stdin: key16 + "\n",
			wantFile: `{"default":"cf","profiles":{"cf":{"provider":"cloudflare","api_key":"` + key16 +
				`","account_id":"` + testAccountID + `","model":"clef-flash"}}}`,
		},
		{
			name:  "should keep the base url",
			args:  []string{"profile", "create", "proxy", "--provider", "typesafe", "--base-url", "https://proxy.example", "--file"},
			stdin: key16 + "\n",
			wantFile: `{"default":"proxy","profiles":{"proxy":{"provider":"typesafe","api_key":"` + key16 +
				`","base_url":"https://proxy.example"}}}`,
		},
		{
			name:     "should take --provider given before the subcommand as the profile's provider",
			args:     []string{"--provider", "berget", "profile", "create", "work", "--file"},
			stdin:    key16 + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"berget","api_key":"` + key16 + `"}}}`,
		},
		{
			name:  "should keep the default when another profile exists",
			file:  existing,
			args:  []string{"profile", "create", "work", "--provider", "typesafe", "--file"},
			stdin: key16 + "\n",
			wantFile: `{"default":"home","profiles":{"home":{"provider":"typesafe","api_key":"SECRET-HOME"},` +
				`"work":{"provider":"typesafe","api_key":"` + key16 + `"}}}`,
			notErr: []string{"is the default profile"},
		},
		{
			name:     "should fall back to the file and say so when there is no keychain",
			keychain: keychainNone,
			args:     []string{"profile", "create", "work", "--provider", "typesafe"},
			stdin:    key16 + "\n",
			wantErr: []string{"warning: could not store the key in the OS keychain: no keychain in tests. " +
				"The key is in {path} instead"},
			wantFile: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"` + key16 + `"}}}`,
		},
		{
			name:     "should fail rather than fall back when the keychain times out",
			keychain: keychainTimeout,
			args:     []string{"profile", "create", "work", "--provider", "typesafe"},
			stdin:    key16 + "\n",
			wantCode: ExitAuth,
			wantErr: []string{
				"the keychain did not answer in time",
				"Answer the keychain prompt, or run the command again with --file",
			},
			wantNoFile: true,
		},
		{
			name:        "should refuse a name that exists",
			file:        existing,
			args:        []string{"profile", "create", "home", "--provider", "typesafe", "--file"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: profile 'home' exists. Change it with onesie profile update 'home'"},
			wantFile:    existing,
		},
		{
			name:        "should refuse an uppercase name",
			args:        []string{"profile", "create", "Work", "--provider", "typesafe", "--file"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: 'Work' is not a profile name"},
			wantNoFile:  true,
		},
		{
			name:        "should refuse a name that starts with a dash",
			args:        []string{"profile", "create", "--provider", "typesafe", "--file", "--", "-work"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: '-work' is not a profile name"},
			wantNoFile:  true,
		},
		{
			name:        "should refuse a create with no provider",
			args:        []string{"profile", "create", "work", "--file"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: profile create needs --provider"},
			wantNoFile:  true,
		},
		{
			name:        "should refuse an unknown provider before reading the key",
			args:        []string{"profile", "create", "work", "--provider", "nope", "--file"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr: []string{"onesie: --provider names 'nope', which is not typesafe, openrouter, berget, " +
				"levanto or cloudflare"},
			wantNoFile: true,
		},
		{
			name:        "should refuse a base url with no scheme before reading the key",
			args:        []string{"profile", "create", "work", "--provider", "typesafe", "--base-url", "proxy.example"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"base URL must be an absolute http or https URL, got proxy.example"},
			wantNoFile:  true,
		},
		{
			name:        "should refuse a base url with credentials on a scheme it does not take",
			args:        []string{"profile", "create", "work", "--provider", "typesafe", "--base-url", "ftp://u:SECRET-pw@proxy.example"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: " + refusedBaseURL},
			wantNoFile:  true,
		},
		{
			name:        "should refuse a plain http base url to another host before reading the key",
			args:        []string{"profile", "create", "work", "--provider", "typesafe", "--base-url", "http://proxy.example"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr: []string{"onesie: --base-url http://proxy.example is plain http to a host other than this machine, " +
				"so the API key would cross the network unencrypted. Use https, or pass --allow-http to store it, " +
				"and every run on the profile then needs --allow-http too"},
			wantNoFile: true,
		},
		{
			name: "should store a plain http base url to another host under --allow-http",
			args: []string{
				"profile", "create", "work", "--provider", "typesafe", "--base-url", "http://proxy.example",
				"--allow-http", "--file",
			},
			stdin: key16 + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"` + key16 +
				`","base_url":"http://proxy.example"}}}`,
		},
		{
			name:  "should store a plain http base url to this machine",
			args:  []string{"profile", "create", "work", "--provider", "typesafe", "--base-url", "http://127.0.0.1:8080", "--file"},
			stdin: key16 + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"` + key16 +
				`","base_url":"http://127.0.0.1:8080"}}}`,
		},
		{
			name:  "should accept an escaped @ in the path of a base url",
			args:  []string{"profile", "create", "work", "--provider", "typesafe", "--base-url", "https://proxy.example/a%40b", "--file"},
			stdin: key16 + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"` + key16 +
				`","base_url":"https://proxy.example/a%40b"}}}`,
		},
		{
			name:        "should refuse an account id on a provider other than cloudflare",
			args:        []string{"profile", "create", "work", "--provider", "openrouter", "--account-id", testAccountID},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: --account-id applies only to cloudflare, and this profile is on openrouter"},
			wantNoFile:  true,
		},
		{
			name:        "should refuse an account id that is not 32 hexadecimal characters",
			args:        []string{"profile", "create", "cf", "--provider", "cloudflare", "--account-id", "abc"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: --account-id must be 32 hexadecimal characters"},
			wantNoFile:  true,
		},
		{
			name: "should refuse both a base url and an account id",
			args: []string{
				"profile", "create", "cf", "--provider", "cloudflare", "--account-id", testAccountID,
				"--base-url", "https://proxy.example",
			},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: --base-url and --account-id both name the account. Pass one"},
			wantNoFile:  true,
		},
		{
			name:        "should refuse an empty --account-id",
			args:        []string{"profile", "create", "cf", "--provider", "cloudflare", "--account-id", " "},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: --account-id is empty. Name one, or leave the flag out"},
			wantNoFile:  true,
		},
		{
			name:        "should refuse an empty --base-url",
			args:        []string{"profile", "create", "work", "--provider", "typesafe", "--base-url", ""},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: --base-url is empty. Name one, or leave the flag out"},
			wantNoFile:  true,
		},
		{
			name:       "should refuse an empty key",
			args:       []string{"profile", "create", "work", "--provider", "typesafe"},
			stdin:      "   \n",
			wantCode:   ExitUsage,
			wantErr:    []string{"onesie: profile create: the key is empty"},
			wantNoFile: true,
		},
		{
			name:       "should refuse a second line on stdin",
			args:       []string{"profile", "create", "work", "--provider", "typesafe"},
			stdin:      key16 + "\nlogin: someone\n",
			wantCode:   ExitUsage,
			wantErr:    []string{"onesie: profile create: stdin carries more than one line"},
			wantNoFile: true,
		},
		{
			name:      "should read the key from the hidden prompt on a terminal",
			args:      []string{"profile", "create", "work", "--provider", "typesafe"},
			tty:       true,
			secret:    key16 + "\n",
			wantErr:   []string{"API key: "},
			wantFile:  `{"default":"work","profiles":{"work":{"provider":"typesafe","store":"keychain","account":"work@{hash}","key_end":"5678"}}}`,
			wantItems: map[string]string{"work@{hash}": key16},
		},
		{
			name:     "should refuse a profiles file others can reach",
			file:     existing,
			mode:     0o644,
			unixOnly: true,
			args:     []string{"profile", "create", "work", "--provider", "typesafe", "--file"},
			stdin:    key16 + "\n",
			wantCode: ExitAuth,
			wantErr:  []string{"is readable or writable by its group or others, mode 644"},
		},
		{
			name: "should refuse a profiles file with an unknown member",
			// Split, since misspell flags the misspelled member as a typo of model.
			file:     `{"profiles":{"home":{"provider":"typesafe","api_key":"SECRET-HOME","mod` + `le":"x"}}}`,
			args:     []string{"profile", "create", "work", "--provider", "typesafe", "--file"},
			stdin:    key16 + "\n",
			wantCode: ExitUsage,
			wantErr:  []string{"is invalid. Profile 'home' has an unknown member 'mod" + "le'"},
		},
		{
			name:  "should convert credentials.json and keep its profiles beside the new one",
			old:   `{"providers":{"typesafe":{"api_key":"SECRET-OLD"}}}`,
			args:  []string{"profile", "create", "work", "--provider", "openrouter", "--file"},
			stdin: key16 + "\n",
			wantErr: []string{"onesie: converted credentials.json into profiles.json, with profile 'typesafe'. " +
				"'typesafe' is the default"},
			wantFile: `{"default":"typesafe","profiles":{"typesafe":{"provider":"typesafe","api_key":"SECRET-OLD"},` +
				`"work":{"provider":"openrouter","api_key":"` + key16 + `"}}}`,
			wantNoOld: true,
		},
		{
			name:       "should remove the new keychain item when the file cannot be written",
			store:      failingWrite,
			args:       []string{"profile", "create", "work", "--provider", "typesafe"},
			stdin:      key16 + "\n",
			wantCode:   ExitUsage,
			wantErr:    []string{"the disk is full"},
			notErr:     []string{"stored the key"},
			wantNoFile: true,
			wantItems:  map[string]string{},
		},
		{
			name:        "should refuse an item another profile names before reading the key",
			file:        takenItem,
			items:       map[string]string{"work@{hash}": key16},
			args:        []string{"profile", "create", "work", "--provider", "openrouter"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr: []string{"onesie: profile 'home' already uses the keychain item work@{hash}. " +
				"Delete or update it first"},
			wantFile:  takenItem,
			wantItems: map[string]string{"work@{hash}": key16},
		},
		{
			name:  "should store the key in the file under --file when another profile names the item",
			file:  takenItem,
			items: map[string]string{"work@{hash}": key16},
			args:  []string{"profile", "create", "work", "--provider", "openrouter", "--file"},
			stdin: key15 + "\n",
			wantFile: `{"default":"home","profiles":{` +
				`"home":{"provider":"typesafe","store":"keychain","account":"work@{hash}"},` +
				`"work":{"provider":"openrouter","api_key":"` + key15 + `"}}}`,
			wantItems: map[string]string{"work@{hash}": key16},
		},
		{
			name:       "should keep exit 2 and name the item left behind when its removal also fails",
			keychain:   keychainUndeletable,
			store:      failingWrite,
			args:       []string{"profile", "create", "work", "--provider", "typesafe"},
			stdin:      key16 + "\n",
			wantCode:   ExitUsage,
			wantErr:    []string{"the disk is full", "The keychain item work@{hash} is left behind"},
			wantNoFile: true,
			wantItems:  map[string]string{"work@{hash}": key16},
		},
		{
			name:  "should ignore ONESIE_PROFILE and ONESIE_PROVIDER",
			env:   map[string]string{"ONESIE_PROFILE": "home", "ONESIE_PROVIDER": "berget"},
			args:  []string{"profile", "create", "work", "--provider", "openrouter", "--file"},
			stdin: key16 + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","api_key":"` +
				key16 + `"}}}`,
		},
		{
			name:     "should warn when the mode cannot be set and still store the key",
			store:    failingChmod,
			args:     []string{"profile", "create", "work", "--provider", "typesafe", "--file"},
			stdin:    key16 + "\n",
			wantErr:  []string{"warning: could not set mode 600 on {path}. The key is not protected by the filesystem"},
			wantFile: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"` + key16 + `"}}}`,
		},
	}

	for _, unclear := range unclearBaseURLs() {
		tests = append(tests, profileCase{
			name:        "should refuse a base url " + unclear.name + " before reading the key",
			args:        []string{"profile", "create", "work", "--provider", "typesafe", "--base-url", unclear.url},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: " + refusedBaseURL},
			wantNoFile:  true,
		})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runProfileCase(t, tc)
		})
	}

	t.Run("should keep exit 2 for a failed save when stderr is a closed pipe", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		root := NewRootCmd(BuildInfo{Version: "1.2.3"},
			WithStdin(strings.NewReader(key16+"\n")),
			WithStdinTTY(false),
			WithStdoutTTY(false),
			WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": dir})),
			WithKeychain(undeletableKeychain{workingKeychain(nil)}),
			WithCredentialStore(creds.NewStore(failingWrite...)))

		root.SetOut(io.Discard)
		root.SetErr(closedPipe{})
		root.SetArgs([]string{"profile", "create", "work", "--provider", "typesafe"})

		if code := Execute(t.Context(), root); code != ExitUsage {
			t.Errorf("exit code = %d, want %d", code, ExitUsage)
		}
	})

	t.Run("should exit 130 on an interrupt while stdin has not ended", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		held, _ := io.Pipe()

		code := interruptedRun(t, []string{"profile", "create", "work", "--provider", "typesafe", "--file"},
			WithStdin(held),
			WithStdinTTY(false),
			WithStdoutTTY(false),
			WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": dir})),
			WithKeychain(noKeychain()))

		if code != ExitInterrupt {
			t.Errorf("exit code = %d, want %d", code, ExitInterrupt)
		}

		if _, err := os.Stat(filepath.Join(dir, "profiles.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("profiles.json was written, stat error = %v", err)
		}
	})
}

func TestProfileUpdate(t *testing.T) {
	t.Parallel()

	failingWrite := []creds.StoreOption{creds.WithCreateTemp(func(string, string) (*os.File, error) {
		return nil, errors.New("the disk is full")
	})}

	const (
		newKey = "SECRET-ABCDEFGHI"
		work   = `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
			`"account":"work@{hash}","key_end":"5678","model":"typesafe/jev"}}}`
		converted = `{"default":"typesafe","profiles":{"typesafe":{"provider":"typesafe","store":"keychain",` +
			`"account":"typesafe"}}}`
		takenItem = `{"default":"work","profiles":{"work":{"provider":"openrouter","api_key":"SECRET-WORK"},` +
			`"home":{"provider":"openrouter","store":"keychain","account":"work@{hash}"}}}`
		cfx = `{"default":"cfx","profiles":{"cfx":{"provider":"cloudflare","api_key":"SECRET-CF",` +
			`"account_id":"` + testAccountID + `"}}}`
		cfBase = `{"default":"cfb","profiles":{"cfb":{"provider":"cloudflare","api_key":"SECRET-CF",` +
			`"base_url":"https://proxy.example"}}}`
		levanto = `{"default":"lv","profiles":{"lv":{"provider":"levanto","api_key":"SECRET-LV"}}}`
		shared  = `{"default":"work","profiles":{` +
			`"work":{"provider":"openrouter","store":"keychain","account":"shared"},` +
			`"home":{"provider":"openrouter","store":"keychain","account":"shared"}}}`
	)

	workItems := map[string]string{"work@{hash}": key16}

	tests := []profileCase{
		{
			name:  "should change only the model",
			file:  work,
			items: workItems,
			args:  []string{"profile", "update", "work", "--model", "typesafe/jev-1.13"},
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
				`"account":"work@{hash}","key_end":"5678","model":"typesafe/jev-1.13"}}}`,
			wantItems: workItems,
		},
		{
			name:  "should clear the model with an empty value",
			file:  work,
			items: workItems,
			args:  []string{"profile", "update", "work", "--model", ""},
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
				`"account":"work@{hash}","key_end":"5678"}}}`,
		},
		{
			name:     "should refuse a plain http base url to another host",
			file:     work,
			args:     []string{"profile", "update", "work", "--base-url", "http://proxy.example"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: --base-url http://proxy.example is plain http", "pass --allow-http to store it"},
			wantFile: work,
		},
		{
			name:  "should store a plain http base url to another host under --allow-http",
			file:  work,
			items: workItems,
			args:  []string{"profile", "update", "work", "--base-url", "http://proxy.example", "--allow-http"},
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
				`"account":"work@{hash}","key_end":"5678","model":"typesafe/jev","base_url":"http://proxy.example"}}}`,
		},
		{
			name:     "should name --account-id when its value is not 32 hexadecimal characters",
			file:     cfx,
			args:     []string{"profile", "update", "cfx", "--account-id", "0123"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: --account-id must be 32 hexadecimal characters"},
			notErr:   []string{"profile 'cfx' has"},
			wantFile: cfx,
		},
		{
			name:     "should name --account-id on a provider other than cloudflare",
			file:     levanto,
			args:     []string{"profile", "update", "lv", "--account-id", testAccountID},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: --account-id applies only to cloudflare, and this profile is on levanto"},
			wantFile: levanto,
		},
		{
			name:     "should name --base-url on a profile that holds an account id",
			file:     cfx,
			args:     []string{"profile", "update", "cfx", "--base-url", "https://proxy.example"},
			wantCode: ExitUsage,
			wantErr: []string{"onesie: --base-url cannot join the profile's account_id, since each names the " +
				"account. Clear it with --account-id \"\" in the same update"},
			wantFile: cfx,
		},
		{
			name:     "should name --account-id on a profile that holds a base url",
			file:     cfBase,
			args:     []string{"profile", "update", "cfb", "--account-id", testAccountID},
			wantCode: ExitUsage,
			wantErr: []string{"onesie: --account-id cannot join the profile's base_url, since each names the " +
				"account. Clear it with --base-url \"\" in the same update"},
			wantFile: cfBase,
		},
		{
			name: "should swap a base url for an account id in one update",
			file: cfBase,
			args: []string{"profile", "update", "cfb", "--account-id", testAccountID, "--base-url", ""},
			wantFile: `{"default":"cfb","profiles":{"cfb":{"provider":"cloudflare","api_key":"SECRET-CF",` +
				`"account_id":"` + testAccountID + `"}}}`,
		},
		{
			name:     "should exit 2 when nothing changes",
			file:     work,
			args:     []string{"profile", "update", "work"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: profile update 'work' changes nothing"},
			wantFile: work,
		},
		{
			name:     "should exit 2 when a member is set to the value it has",
			file:     work,
			args:     []string{"profile", "update", "work", "--model", "typesafe/jev"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: profile update 'work' changes nothing"},
		},
		{
			name:     "should refuse a missing profile",
			file:     work,
			args:     []string{"profile", "update", "nope", "--model", "x"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: there is no profile named 'nope'. The profiles are 'work'"},
		},
		{
			name:        "should refuse a missing profile before reading the key",
			file:        work,
			args:        []string{"profile", "update", "nope", "--key"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: there is no profile named 'nope'"},
			wantFile:    work,
		},
		{
			name:        "should refuse a name that is not a profile name before reading the key",
			file:        work,
			args:        []string{"profile", "update", "Work", "--key"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: there is no profile named 'Work'"},
			wantFile:    work,
		},
		{
			name:        "should refuse an unknown provider before reading the key",
			file:        work,
			items:       workItems,
			args:        []string{"profile", "update", "work", "--provider", "nope", "--key"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: --provider names 'nope', which is not typesafe"},
			wantFile:    work,
			wantItems:   workItems,
		},
		{
			name:        "should refuse an item another profile names before reading the key",
			file:        takenItem,
			items:       workItems,
			args:        []string{"profile", "update", "work", "--key"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr: []string{"onesie: profile 'home' already uses the keychain item work@{hash}. " +
				"Delete or update it first"},
			wantFile:  takenItem,
			wantItems: workItems,
		},
		{
			name:  "should store a new key in the file under --file when another profile names the item",
			file:  takenItem,
			items: workItems,
			args:  []string{"profile", "update", "work", "--key", "--file"},
			stdin: newKey + "\n",
			wantFile: `{"default":"work","profiles":{` +
				`"work":{"provider":"openrouter","api_key":"` + newKey + `"},` +
				`"home":{"provider":"openrouter","store":"keychain","account":"work@{hash}"}}}`,
			wantItems: workItems,
		},
		{
			name:     "should fall back to the file and name the item left behind when the keychain cannot store",
			file:     work,
			keychain: keychainNone,
			args:     []string{"profile", "update", "work", "--key"},
			stdin:    newKey + "\n",
			wantErr: []string{
				"warning: could not store the key in the OS keychain: no keychain in tests. The key is in {path} instead",
				"The keychain item work@{hash} is left behind",
			},
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","api_key":"` + newKey +
				`","model":"typesafe/jev"}}}`,
		},
		{
			name:     "should refuse rather than write when reading the old key times out",
			file:     work,
			items:    workItems,
			keychain: keychainStalled,
			args:     []string{"profile", "update", "work", "--key"},
			stdin:    newKey + "\n",
			wantCode: ExitAuth,
			wantErr: []string{
				"the keychain did not answer in time",
				"Answer the keychain prompt, or run the command again with --file",
			},
			wantFile:  work,
			wantItems: workItems,
		},
		{
			name:  "should ignore ONESIE_PROFILE and ONESIE_PROVIDER",
			file:  work,
			items: workItems,
			env:   map[string]string{"ONESIE_PROFILE": "nope", "ONESIE_PROVIDER": "berget"},
			args:  []string{"profile", "update", "work", "--model", "typesafe/jev-1.13"},
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
				`"account":"work@{hash}","key_end":"5678","model":"typesafe/jev-1.13"}}}`,
			wantItems: workItems,
		},
		{
			name:  "should take --provider given before the subcommand as the new provider",
			file:  work,
			items: workItems,
			args:  []string{"--provider", "berget", "profile", "update", "work", "--key", "--file"},
			stdin: newKey + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"berget","api_key":"` + newKey +
				`","model":"typesafe/jev"}}}`,
			wantItems: map[string]string{},
		},
		{
			name:     "should refuse --file without --key",
			file:     work,
			args:     []string{"profile", "update", "work", "--file"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: profile update: --file applies to a new key, so it needs --key"},
		},
		{
			name:     "should refuse --provider without --key",
			file:     work,
			args:     []string{"profile", "update", "work", "--provider", "berget"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: profile update: --provider needs --key, since a key belongs to one provider"},
		},
		{
			name:        "should refuse an empty --provider",
			file:        work,
			args:        []string{"profile", "update", "work", "--provider", "", "--key"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: profile update: --provider is empty"},
		},
		{
			name:  "should write a new key to the profile's item and rewrite key_end",
			file:  work,
			items: workItems,
			args:  []string{"profile", "update", "work", "--key"},
			stdin: newKey + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
				`"account":"work@{hash}","key_end":"FGHI","model":"typesafe/jev"}}}`,
			wantItems: map[string]string{"work@{hash}": newKey},
		},
		{
			name:  "should drop key_end for a new key under 16 characters",
			file:  work,
			items: workItems,
			args:  []string{"profile", "update", "work", "--key"},
			stdin: key15 + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain",` +
				`"account":"work@{hash}","model":"typesafe/jev"}}}`,
		},
		{
			name:  "should move the key to the file and delete the old item",
			file:  work,
			items: workItems,
			args:  []string{"profile", "update", "work", "--key", "--file"},
			stdin: newKey + "\n",
			wantFile: `{"default":"work","profiles":{"work":{"provider":"openrouter","api_key":"` + newKey +
				`","model":"typesafe/jev"}}}`,
			wantItems: map[string]string{},
		},
		{
			name:  "should write a converted profile's key to its own item and delete the old one",
			file:  converted,
			items: map[string]string{"typesafe": key15},
			args:  []string{"profile", "update", "typesafe", "--key"},
			stdin: newKey + "\n",
			wantFile: `{"default":"typesafe","profiles":{"typesafe":{"provider":"typesafe","store":"keychain",` +
				`"account":"typesafe@{hash}","key_end":"FGHI"}}}`,
			wantItems: map[string]string{"typesafe@{hash}": newKey},
		},
		{
			name:  "should keep an item another profile names",
			file:  shared,
			items: map[string]string{"shared": key16},
			args:  []string{"profile", "update", "work", "--key"},
			stdin: newKey + "\n",
			wantFile: `{"default":"work","profiles":{` +
				`"work":{"provider":"openrouter","store":"keychain","account":"work@{hash}","key_end":"FGHI"},` +
				`"home":{"provider":"openrouter","store":"keychain","account":"shared"}}}`,
			wantItems: map[string]string{"shared": key16, "work@{hash}": newKey},
		},
		{
			name:      "should keep exit 2 and name the item left behind when its removal also fails",
			file:      converted,
			items:     map[string]string{"typesafe": key15},
			keychain:  keychainUndeletable,
			store:     failingWrite,
			args:      []string{"profile", "update", "typesafe", "--key"},
			stdin:     newKey + "\n",
			wantCode:  ExitUsage,
			wantErr:   []string{"the disk is full", "The keychain item typesafe@{hash} is left behind"},
			wantFile:  converted,
			wantItems: map[string]string{"typesafe": key15, "typesafe@{hash}": newKey},
		},
		{
			name:     "should say the item holds the new key when the old one cannot be put back",
			file:     work,
			items:    workItems,
			keychain: keychainUnrestorable,
			store:    failingWrite,
			args:     []string{"profile", "update", "work", "--key"},
			stdin:    newKey + "\n",
			wantCode: ExitUsage,
			wantErr: []string{
				"the disk is full",
				"The keychain item work@{hash} now holds the new key, and its old key could not be put back",
			},
			notErr:    []string{"left behind"},
			wantFile:  work,
			wantItems: map[string]string{"work@{hash}": newKey},
		},
		{
			name:      "should delete the new item when the file cannot be written",
			file:      converted,
			items:     map[string]string{"typesafe": key15},
			store:     failingWrite,
			args:      []string{"profile", "update", "typesafe", "--key"},
			stdin:     newKey + "\n",
			wantCode:  ExitUsage,
			wantErr:   []string{"the disk is full"},
			wantFile:  converted,
			wantItems: map[string]string{"typesafe": key15},
		},
		{
			name:      "should restore the old key when the file cannot be written",
			file:      work,
			items:     workItems,
			store:     failingWrite,
			args:      []string{"profile", "update", "work", "--key"},
			stdin:     newKey + "\n",
			wantCode:  ExitUsage,
			wantErr:   []string{"the disk is full"},
			wantFile:  work,
			wantItems: workItems,
		},
		{
			name:      "should delete the new key when the item held none and the file cannot be written",
			file:      work,
			store:     failingWrite,
			args:      []string{"profile", "update", "work", "--key"},
			stdin:     newKey + "\n",
			wantCode:  ExitUsage,
			wantErr:   []string{"the disk is full"},
			wantFile:  work,
			wantItems: map[string]string{},
		},
		{
			name:     "should leave an item it could not read when the file cannot be written",
			file:     work,
			items:    workItems,
			keychain: keychainUnreadable,
			store:    failingWrite,
			args:     []string{"profile", "update", "work", "--key"},
			stdin:    newKey + "\n",
			wantCode: ExitUsage,
			wantErr: []string{
				"the disk is full",
				"warning: the keychain item work@{hash} now holds the new key, and its old key could not be read",
			},
			wantFile:  work,
			wantItems: map[string]string{"work@{hash}": newKey},
		},
		{
			name: "should change the provider with a new key and drop account_id",
			file: `{"default":"cf","profiles":{"cf":{"provider":"cloudflare","api_key":"SECRET-CF",` +
				`"account_id":"` + testAccountID + `","model":"clef-flash"}}}`,
			args:  []string{"profile", "update", "cf", "--provider", "openrouter", "--key", "--file"},
			stdin: newKey + "\n",
			wantFile: `{"default":"cf","profiles":{"cf":{"provider":"openrouter","api_key":"` + newKey +
				`","model":"clef-flash"}}}`,
		},
		{
			name: "should keep model and base_url when the provider changes",
			file: `{"default":"ts","profiles":{"ts":{"provider":"typesafe","api_key":"SECRET-TS",` +
				`"base_url":"https://proxy.example","model":"jev-1.13.0"}}}`,
			args:  []string{"profile", "update", "ts", "--provider", "openrouter", "--key", "--file"},
			stdin: newKey + "\n",
			wantFile: `{"default":"ts","profiles":{"ts":{"provider":"openrouter","api_key":"` + newKey +
				`","base_url":"https://proxy.example","model":"jev-1.13.0"}}}`,
		},
		{
			name:        "should refuse a base url that holds a fragment before reading the key",
			file:        work,
			items:       workItems,
			args:        []string{"profile", "update", "work", "--base-url", "https://proxy.example/v1#SECRET-x", "--key"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: " + refusedBaseURL},
			wantFile:    work,
			wantItems:   workItems,
		},
		{
			name:        "should refuse a result that breaks a rule before reading the key",
			file:        work,
			items:       workItems,
			args:        []string{"profile", "update", "work", "--account-id", testAccountID, "--key"},
			stdinUnread: true,
			wantCode:    ExitUsage,
			wantErr:     []string{"onesie: --account-id applies only to cloudflare, and this profile is on openrouter"},
			wantFile:    work,
			wantItems:   workItems,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runProfileCase(t, tc)
		})
	}
}

func TestProfileDelete(t *testing.T) {
	t.Parallel()

	failingWrite := []creds.StoreOption{creds.WithCreateTemp(func(string, string) (*os.File, error) {
		return nil, errors.New("the disk is full")
	})}

	const (
		two = `{"default":"work","profiles":{` +
			`"work":{"provider":"openrouter","store":"keychain","account":"work@{hash}"},` +
			`"home":{"provider":"typesafe","api_key":"SECRET-HOME"}}}`
		homeOnly = `{"profiles":{"home":{"provider":"typesafe","api_key":"SECRET-HOME"}}}`
		shared   = `{"default":"work","profiles":{` +
			`"work":{"provider":"openrouter","store":"keychain","account":"shared"},` +
			`"home":{"provider":"openrouter","store":"keychain","account":"shared"}}}`
	)

	tests := []profileCase{
		{
			name:      "should delete the profile, its keychain item and the default",
			file:      two,
			items:     map[string]string{"work@{hash}": key16},
			args:      []string{"profile", "delete", "work"},
			wantErr:   []string{"onesie: 'work' was the default, so no profile is the default now"},
			wantFile:  homeOnly,
			wantItems: map[string]string{},
		},
		{
			name:      "should keep the default when another profile goes",
			file:      two,
			items:     map[string]string{"work@{hash}": key16},
			args:      []string{"profile", "delete", "home"},
			notErr:    []string{"was the default"},
			wantFile:  `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain","account":"work@{hash}"}}}`,
			wantItems: map[string]string{"work@{hash}": key16},
		},
		{
			name:      "should keep an item another profile names",
			file:      shared,
			items:     map[string]string{"shared": key16},
			args:      []string{"profile", "delete", "work"},
			wantFile:  `{"profiles":{"home":{"provider":"openrouter","store":"keychain","account":"shared"}}}`,
			wantItems: map[string]string{"shared": key16},
		},
		{
			name:     "should warn and name the item left behind when the keychain cannot be reached",
			file:     two,
			keychain: keychainNone,
			args:     []string{"profile", "delete", "work"},
			wantErr:  []string{"The keychain item work@{hash} is left behind"},
			wantFile: homeOnly,
		},
		{
			name:       "should remove the file with the last profile",
			file:       `{"default":"home","profiles":{"home":{"provider":"typesafe","api_key":"SECRET-HOME"}}}`,
			args:       []string{"profile", "delete", "home"},
			wantNoFile: true,
		},
		{
			name:       "should remove a leftover credentials.json with the last profile",
			file:       `{"default":"home","profiles":{"home":{"provider":"typesafe","api_key":"SECRET-HOME"}}}`,
			old:        `{"providers":{"typesafe":{"api_key":"SECRET-OLD"}}}`,
			args:       []string{"profile", "delete", "home"},
			wantNoFile: true,
			wantNoOld:  true,
		},
		{
			name:       "should remove credentials.json when its conversion stayed in memory",
			old:        `{"providers":{"typesafe":{"api_key":"SECRET-OLD"}}}`,
			store:      failingWrite,
			args:       []string{"profile", "delete", "typesafe"},
			wantErr:    []string{"is converted for this run only"},
			wantNoFile: true,
			wantNoOld:  true,
		},
		{
			name:      "should keep the keychain item when the file cannot be written",
			file:      two,
			items:     map[string]string{"work@{hash}": key16},
			store:     failingWrite,
			args:      []string{"profile", "delete", "work"},
			wantCode:  ExitUsage,
			wantErr:   []string{"the disk is full"},
			wantFile:  two,
			wantItems: map[string]string{"work@{hash}": key16},
		},
		{
			name:      "should ignore ONESIE_PROFILE and ONESIE_PROVIDER",
			file:      two,
			items:     map[string]string{"work@{hash}": key16},
			env:       map[string]string{"ONESIE_PROFILE": "work", "ONESIE_PROVIDER": "openrouter"},
			args:      []string{"profile", "delete", "home"},
			wantFile:  `{"default":"work","profiles":{"work":{"provider":"openrouter","store":"keychain","account":"work@{hash}"}}}`,
			wantItems: map[string]string{"work@{hash}": key16},
		},
		{
			name:      "should refuse --provider given before the subcommand",
			file:      two,
			items:     map[string]string{"work@{hash}": key16},
			args:      []string{"--provider", "typesafe", "profile", "delete", "work"},
			wantCode:  ExitUsage,
			wantErr:   []string{"onesie: --provider does not apply to profile delete"},
			wantFile:  two,
			wantItems: map[string]string{"work@{hash}": key16},
		},
		{
			name:     "should refuse a missing profile",
			file:     two,
			args:     []string{"profile", "delete", "nope"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: there is no profile named 'nope'. The profiles are 'home', 'work'"},
			wantFile: two,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runProfileCase(t, tc)
		})
	}
}

func TestProfileUse(t *testing.T) {
	t.Parallel()

	failingWrite := []creds.StoreOption{creds.WithCreateTemp(func(string, string) (*os.File, error) {
		return nil, errors.New("the disk is full")
	})}

	const two = `{"default":"work","profiles":{` +
		`"work":{"provider":"openrouter","api_key":"SECRET-WORK"},` +
		`"home":{"provider":"typesafe","api_key":"SECRET-HOME"}}}`

	tests := []profileCase{
		{
			name: "should make the named profile the default",
			file: two,
			args: []string{"profile", "use", "home"},
			wantFile: `{"default":"home","profiles":{` +
				`"work":{"provider":"openrouter","api_key":"SECRET-WORK"},` +
				`"home":{"provider":"typesafe","api_key":"SECRET-HOME"}}}`,
		},
		{
			name: "should ignore ONESIE_PROFILE and ONESIE_PROVIDER",
			file: two,
			env:  map[string]string{"ONESIE_PROFILE": "work", "ONESIE_PROVIDER": "openrouter"},
			args: []string{"profile", "use", "home"},
			wantFile: `{"default":"home","profiles":{` +
				`"work":{"provider":"openrouter","api_key":"SECRET-WORK"},` +
				`"home":{"provider":"typesafe","api_key":"SECRET-HOME"}}}`,
		},
		{
			name:       "should fail when a conversion held in memory cannot be written",
			old:        `{"providers":{"typesafe":{"api_key":"SECRET-OLD"},"openrouter":{"api_key":"SECRET-OR"}}}`,
			store:      failingWrite,
			args:       []string{"profile", "use", "openrouter"},
			wantCode:   ExitUsage,
			wantErr:    []string{"the disk is full"},
			wantNoFile: true,
		},
		{
			name:     "should refuse a missing profile",
			file:     two,
			args:     []string{"profile", "use", "nope"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: there is no profile named 'nope'"},
			wantFile: two,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runProfileCase(t, tc)
		})
	}
}

func TestProfileList(t *testing.T) {
	t.Parallel()

	const (
		profiles = `"work":{"provider":"openrouter","store":"keychain","account":"work@{hash}","model":"typesafe/jev"},` +
			`"personal":{"provider":"openrouter","store":"keychain","account":"personal@{hash}"},` +
			`"cf":{"provider":"cloudflare","api_key":"SECRET-CF","model":"clef-flash"}`
		withDefault = `{"default":"work","profiles":{` + profiles + `}}`
		noDefault   = `{"profiles":{` + profiles + `}}`
		header      = "  NAME      PROVIDER    KEY       MODEL\n"
		cf          = "  cf        cloudflare  file      clef-flash\n"
		unmarked    = header + cf + "  personal  openrouter  keychain\n" + "  work      openrouter  keychain  typesafe/jev\n"
	)

	tests := []profileCase{
		{
			name:    "should mark the default a run would use",
			file:    withDefault,
			args:    []string{"profile", "list"},
			wantOut: header + cf + "  personal  openrouter  keychain\n" + "* work      openrouter  keychain  typesafe/jev\n",
			notErr:  []string{"active:"},
		},
		{
			name:    "should mark the profile ONESIE_PROFILE names",
			file:    withDefault,
			env:     map[string]string{"ONESIE_PROFILE": "personal"},
			args:    []string{"profile", "list"},
			wantOut: header + cf + "* personal  openrouter  keychain\n" + "  work      openrouter  keychain  typesafe/jev\n",
		},
		{
			name:    "should mark nothing and say why under ONESIE_PROVIDER",
			file:    withDefault,
			env:     map[string]string{"ONESIE_PROVIDER": "openrouter"},
			args:    []string{"profile", "list"},
			wantOut: unmarked,
			wantErr: []string{"active: ONESIE_PROVIDER=openrouter, no profile"},
		},
		{
			name:    "should mark nothing and say why when no profile is the default",
			file:    noDefault,
			args:    []string{"profile", "list"},
			wantOut: unmarked,
			wantErr: []string{"active: none, since no profile is the default and nothing chose one"},
		},
		{
			name:    "should mark nothing when both variables choose",
			file:    withDefault,
			env:     map[string]string{"ONESIE_PROFILE": "work", "ONESIE_PROVIDER": "openrouter"},
			args:    []string{"profile", "list"},
			wantOut: unmarked,
			wantErr: []string{"active: none, since ONESIE_PROFILE and ONESIE_PROVIDER are both set"},
		},
		{
			name:    "should mark nothing when ONESIE_PROFILE names a missing profile",
			file:    withDefault,
			env:     map[string]string{"ONESIE_PROFILE": "nope"},
			args:    []string{"profile", "list"},
			wantOut: unmarked,
			wantErr: []string{"active: none, since ONESIE_PROFILE names profile 'nope', which does not exist"},
		},
		{
			name:     "should refuse a profiles file others can reach",
			file:     withDefault,
			mode:     0o644,
			unixOnly: true,
			args:     []string{"profile", "list"},
			wantCode: ExitAuth,
			wantErr:  []string{"is readable or writable by its group or others, mode 644"},
		},
		{
			name: "should print a model with non-ASCII characters whole",
			file: `{"default":"ts","profiles":{"ts":{"provider":"typesafe","api_key":"SECRET-TS","model":"jév-ü"}}}`,
			args: []string{"profile", "list"},
			wantOut: "  NAME  PROVIDER  KEY   MODEL\n" +
				"* ts    typesafe  file  jév-ü\n",
		},
		{
			name:    "should print nothing and name profile create with no profiles",
			args:    []string{"profile", "list"},
			wantErr: []string{"onesie: no profiles. Run onesie profile create NAME --provider PROVIDER to store one"},
		},
		{
			name:    "should convert credentials.json without touching the keychain",
			old:     `{"providers":{"typesafe":{"store":"keychain","account":"typesafe@abc"}}}`,
			args:    []string{"profile", "list"},
			wantOut: "  NAME      PROVIDER  KEY       MODEL\n* typesafe  typesafe  keychain\n",
			wantErr: []string{"onesie: converted credentials.json into profiles.json, with profile 'typesafe'. " +
				"'typesafe' is the default"},
			wantFile:        `{"default":"typesafe","profiles":{"typesafe":{"provider":"typesafe","store":"keychain","account":"typesafe@abc"}}}`,
			wantNoOld:       true,
			noKeychainCalls: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runProfileCase(t, tc)
		})
	}
}

func TestWriteProfiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		profiles creds.Profiles
		active   string
		want     string
	}{
		{
			name: "should pad a column by its characters, not its bytes",
			profiles: creds.Profiles{Profiles: map[string]creds.Profile{
				"a": {Provider: "typesafé", Model: "m"},
				"b": {Provider: "openrouter", Model: "m"},
			}},
			active: "a",
			want: "  NAME  PROVIDER    KEY   MODEL\n" +
				"* a     typesafé    file  m\n" +
				"  b     openrouter  file  m\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			if err := writeProfiles(&out, tc.profiles, tc.active); err != nil {
				t.Fatalf("writeProfiles: %v", err)
			}

			if out.String() != tc.want {
				t.Errorf("output = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestProfileShow(t *testing.T) {
	t.Parallel()

	const (
		shown = `{"default":"work","profiles":{` +
			`"work":{"provider":"openrouter","store":"keychain","account":"work@{hash}","key_end":"3f9a","model":"typesafe/jev"},` +
			`"personal":{"provider":"openrouter","store":"keychain","account":"personal@{hash}"},` +
			`"cf":{"provider":"cloudflare","api_key":"SECRET-CF-0123456789"},` +
			`"cfa":{"provider":"cloudflare","api_key":"SECRET-CF-0123456789","account_id":"` + testAccountID + `"}}}`
		workShown = "profile: work\nprovider: openrouter\nchosen by: the default\nkey: keychain, ends in 3f9a\n" +
			"model: typesafe/jev\nbase_url: the provider's\n"
		cfShown = "profile: cf\nprovider: cloudflare\nchosen by: not active\nkey: file, ends in 6789\n" +
			"model: the provider's\nbase_url: the provider's\n"
	)

	tests := []profileCase{
		{
			name:    "should show what a run would use, chosen by the default",
			file:    shown,
			args:    []string{"profile", "show"},
			wantOut: workShown,
		},
		{
			name: "should show a named profile that is not active",
			file: shown,
			args: []string{"profile", "show", "personal"},
			wantOut: "profile: personal\nprovider: openrouter\nchosen by: not active\nkey: keychain\n" +
				"model: the provider's\nbase_url: the provider's\n",
		},
		{
			name: "should say a named profile is active through ONESIE_PROFILE",
			file: shown,
			env:  map[string]string{"ONESIE_PROFILE": "personal"},
			args: []string{"profile", "show", "personal"},
			wantOut: "profile: personal\nprovider: openrouter\nchosen by: ONESIE_PROFILE\nkey: keychain\n" +
				"model: the provider's\nbase_url: the provider's\n",
		},
		{
			name:     "should show a file key's ending and exit 2 for a cloudflare profile with no account",
			file:     shown,
			args:     []string{"profile", "show", "cf"},
			wantOut:  cfShown + "account: none\n",
			wantCode: ExitUsage,
		},
		{
			name:    "should name CLOUDFLARE_ACCOUNT_ID as the account",
			file:    shown,
			env:     map[string]string{"CLOUDFLARE_ACCOUNT_ID": testAccountID},
			args:    []string{"profile", "show", "cf"},
			wantOut: cfShown + "account: CLOUDFLARE_ACCOUNT_ID\n",
		},
		{
			name: "should name account_id as the account",
			file: shown,
			args: []string{"profile", "show", "cfa"},
			wantOut: "profile: cfa\nprovider: cloudflare\nchosen by: not active\nkey: file, ends in 6789\n" +
				"model: the provider's\nbase_url: the provider's\naccount: account_id\n",
		},
		{
			name: "should show a run under --provider with an environment key",
			file: shown,
			env:  map[string]string{"OPENROUTER_API_KEY": "SECRET-OR"},
			args: []string{"profile", "show", "--provider", "openrouter"},
			wantOut: "profile: none\nprovider: openrouter\nchosen by: --provider\nkey: env OPENROUTER_API_KEY\n" +
				"model: the provider's\nbase_url: the provider's\n",
		},
		{
			name: "should print key: none and exit 2 when nothing holds a key",
			args: []string{"profile", "show"},
			wantOut: "profile: none\nprovider: typesafe\nchosen by: nothing\nkey: none\n" +
				"model: the provider's\nbase_url: the provider's\n",
			wantCode: ExitUsage,
		},
		{
			name:     "should exit 2 when there are profiles and no default",
			file:     `{"profiles":{"personal":{"provider":"openrouter","store":"keychain","account":"personal@{hash}"}}}`,
			args:     []string{"profile", "show"},
			wantCode: ExitUsage,
			wantErr:  []string{"Run onesie profile use NAME, or pass --profile NAME or --provider NAME"},
		},
		{
			name:            "should never read the keychain",
			file:            shown,
			args:            []string{"profile", "show"},
			wantOut:         workShown,
			noKeychainCalls: true,
		},
		{
			name: "should accept an escaped @ in the path of base_url",
			file: `{"default":"ts","profiles":{"ts":{"provider":"typesafe","api_key":"SECRET-TS-0123456789",` +
				`"base_url":"https://proxy.example/a%40b"}}}`,
			args: []string{"profile", "show"},
			wantOut: "profile: ts\nprovider: typesafe\nchosen by: the default\nkey: file, ends in 6789\n" +
				"model: the provider's\nbase_url: https://proxy.example/a%40b\n",
		},
		{
			name:     "should refuse a missing profile",
			file:     shown,
			args:     []string{"profile", "show", "nope"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: there is no profile named 'nope'. The profiles are 'cf', 'cfa', 'personal', 'work'"},
		},
	}

	for _, unclear := range unclearBaseURLs() {
		tests = append(tests, profileCase{
			name: "should refuse a base url " + unclear.name,
			file: `{"default":"ts","profiles":{"ts":{"provider":"typesafe","api_key":"SECRET-TS-0123456789",` +
				`"base_url":"` + unclear.url + `"}}}`,
			args:     []string{"profile", "show"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: " + refusedBaseURL},
		})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runProfileCase(t, tc)
		})
	}
}

func TestProfileTest(t *testing.T) {
	t.Parallel()

	const (
		listing = `{"models":[` +
			`{"name":"jev-latest","description":"alias","release_date":"2026-08-01"},` +
			`{"name":"onesie-1.13.0","description":"current","release_date":"2026-08-01"}]}`
		fileKey  = "SECRET-0123456789"
		fileWork = `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"` + fileKey +
			`","base_url":"{url}"}}}`
		keychainWork = `{"default":"work","profiles":{"work":{"provider":"typesafe","store":"keychain",` +
			`"account":"work@abc","base_url":"{url}"}}}`
	)

	tests := []struct {
		name      string
		profiles  string
		args      []string
		env       map[string]string
		items     map[string]string
		status    int
		response  string
		unreached bool
		wantOut   string
		wantErr   string
		wantCode  int
		wantAuth  string
	}{
		{
			name:     "should list the models with the named profile's key",
			profiles: fileWork,
			args:     []string{"profile", "test", "work"},
			env:      map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
			wantOut:  "profile: work\nprovider: typesafe\nkey: file, ends in 6789\nmodels: 2\n",
			wantAuth: "Bearer " + fileKey,
		},
		{
			name:     "should test what a run would use when given no name",
			profiles: fileWork,
			args:     []string{"profile", "test"},
			wantOut:  "profile: work\nprovider: typesafe\nkey: file, ends in 6789\nmodels: 2\n",
			wantAuth: "Bearer " + fileKey,
		},
		{
			name:     "should send the key the keychain holds",
			profiles: keychainWork,
			args:     []string{"profile", "test"},
			items:    map[string]string{"work@abc": "SECRET-KC"},
			wantOut:  "profile: work\nprovider: typesafe\nkey: keychain\nmodels: 2\n",
			wantAuth: "Bearer SECRET-KC",
		},
		{
			name: "should test the environment key of a run without a profile",
			args: []string{"profile", "test"},
			env: map[string]string{
				"ONESIE_PROVIDER": "typesafe", onesie.EnvAPIKey: "SECRET-ENV", onesie.EnvBaseURL: "{url}",
			},
			wantOut:  "profile: none\nprovider: typesafe\nkey: env TYPESAFE_API_KEY\nmodels: 2\n",
			wantAuth: "Bearer SECRET-ENV",
		},
		{
			name:     "should exit 3 on a refused key",
			profiles: fileWork,
			args:     []string{"profile", "test", "work"},
			status:   http.StatusUnauthorized,
			wantCode: ExitAuth,
			wantErr:  "onesie: 401 the key was refused. Replace the key with onesie profile update 'work' --key",
		},
		{
			name:     "should name the key variable when the server refuses an environment key",
			args:     []string{"profile", "test"},
			env:      map[string]string{"ONESIE_PROVIDER": "typesafe", onesie.EnvAPIKey: "SECRET-ENV", onesie.EnvBaseURL: "{url}"},
			status:   http.StatusUnauthorized,
			wantCode: ExitAuth,
			wantErr:  "onesie: 401 the key was refused. Check the key in TYPESAFE_API_KEY",
		},
		{
			name:     "should exit 3 on a forbidden key",
			profiles: fileWork,
			args:     []string{"profile", "test", "work"},
			status:   http.StatusForbidden,
			wantCode: ExitAuth,
			wantErr:  "onesie: 403 the key was refused. Replace the key with onesie profile update 'work' --key",
		},
		{
			name: "should exit 3 and suggest credits on a 402",
			profiles: `{"default":"or","profiles":{"or":{"provider":"openrouter","api_key":"` + fileKey +
				`","base_url":"{url}"}}}`,
			args:     []string{"profile", "test"},
			status:   http.StatusPaymentRequired,
			response: `{"error":{"message":"Insufficient credits","code":402}}`,
			wantCode: ExitAuth,
			wantErr:  "onesie: 402 Insufficient credits. Add credits to the account this key belongs to",
		},
		{
			name:     "should exit 4 when the server answers with a body onesie cannot use",
			profiles: fileWork,
			args:     []string{"profile", "test", "work"},
			response: "not json",
			wantCode: ExitUnavailable,
			wantErr:  "onesie: 200 response could not be used: the response is not JSON",
		},
		{
			name:      "should exit 5 when the server cannot be reached",
			profiles:  fileWork,
			args:      []string{"profile", "test", "work"},
			unreached: true,
			wantCode:  ExitTransport,
			wantErr:   "onesie: connection error",
		},
		{
			name: "should only list the models on levanto, whose list needs the key",
			profiles: `{"default":"lv","profiles":{"lv":{"provider":"levanto","api_key":"` + fileKey +
				`","base_url":"{url}"}}}`,
			args:     []string{"profile", "test"},
			wantOut:  "profile: lv\nprovider: levanto\nkey: file, ends in 6789\nmodels: 2\n",
			wantAuth: "Bearer " + fileKey,
		},
		{
			name:     "should exit 3 when the keychain item is gone",
			profiles: keychainWork,
			args:     []string{"profile", "test"},
			wantCode: ExitAuth,
			wantErr:  "the keychain holds no key",
		},
		{
			name:     "should exit 3 for an empty keychain item rather than read the variable",
			profiles: keychainWork,
			args:     []string{"profile", "test"},
			env:      map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
			items:    map[string]string{"work@abc": ""},
			wantCode: ExitAuth,
			wantErr:  "the keychain holds no key",
		},
		{
			name: "should leave the userinfo out of a base url the client refuses",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"` + fileKey +
				`","base_url":"ftp://u:SECRET-pw@proxy.example"}}}`,
			args:     []string{"profile", "test"},
			wantCode: ExitUsage,
			wantErr:  "onesie: " + refusedBaseURL + "\n",
		},
		{
			name:     "should exit 2 with no key and name profile create",
			args:     []string{"profile", "test"},
			wantCode: ExitUsage,
			wantErr: "onesie: no API key. Set TYPESAFE_API_KEY, " +
				"or store one with onesie profile create NAME --provider typesafe",
		},
		{
			name:     "should name the profile that holds a key under ONESIE_PROVIDER",
			profiles: `{"default":"work","profiles":{"work":{"provider":"openrouter","api_key":"` + fileKey + `"}}}`,
			args:     []string{"profile", "test"},
			env:      map[string]string{"ONESIE_PROVIDER": "openrouter"},
			wantCode: ExitUsage,
			wantErr: "onesie: no API key. Set OPENROUTER_API_KEY, or unset ONESIE_PROVIDER and run " +
				"onesie profile use 'work', or run with --profile 'work'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex

			var auths []string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				auths = append(auths, r.Header.Get("Authorization"))
				mu.Unlock()

				w.Header().Set("Content-Type", "application/json")

				body := listing
				if tc.response != "" {
					body = tc.response
				}

				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}

				if tc.status != 0 && tc.response == "" {
					body = `{"error":{"message":"the key was refused"}}`
				}

				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()

			withURL := func(s string) string { return strings.ReplaceAll(s, "{url}", srv.URL) }

			env := map[string]string{}
			for name, value := range tc.env {
				env[name] = withURL(value)
			}

			opts := []RootOption{WithKeychain(workingKeychain(tc.items))}

			if tc.unreached {
				// Closed before the run, so the connection is refused rather than answered.
				srv.Close()

				// A stub with no retries, since a refused connection is retried with backoff by default.
				opts = append(opts, WithClientFactory(stubFactory(srv.URL)))
			}

			out, errOut, code := runInConfig(t, configWith(t, withURL(tc.profiles)), tc.args, "", env, opts...)

			assertNoSecret(t, out, errOut)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
			}

			if out != tc.wantOut {
				t.Errorf("stdout = %q, want %q", out, tc.wantOut)
			}

			if tc.wantErr == "" && errOut != "" {
				t.Errorf("stderr = %q, want it empty", errOut)
			}

			if !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}

			if tc.wantAuth == "" {
				return
			}

			mu.Lock()
			got := slices.Clone(auths)
			mu.Unlock()

			// Compared, never printed, since the header carries the key.
			if len(got) != 1 || got[0] != tc.wantAuth {
				t.Errorf("the listing took %d requests, want one carrying the expected key", len(got))
			}
		})
	}

	t.Run("should ask berget one question with the profile's model", func(t *testing.T) {
		t.Parallel()

		var mu sync.Mutex

		var models []string

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")

			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, `{"object":"list","data":[`+
					`{"id":"Qwen/Qwen3.5-2B","model_type":"system-one","aliases":["systemone"]},`+
					`{"id":"mistral","model_type":"text"}]}`)

				return
			}

			var body struct {
				Model string `json:"model"`
			}

			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding the question: %v", err)
			}

			mu.Lock()
			models = append(models, body.Model)
			mu.Unlock()

			_, _ = io.WriteString(w, `{"model":"Qwen/Qwen3.5-2B","answers":{"key":{"type":"noul","noul":0.9}},"usage":{}}`)
		}))
		defer srv.Close()

		dir := configWith(t, `{"default":"bg","profiles":{"bg":{"provider":"berget",`+
			`"api_key":"SECRET-BG-0123456789","base_url":"`+srv.URL+`","model":"Qwen/Qwen3.5-2B"}}}`)

		out, errOut, code := runInConfig(t, dir, []string{"profile", "test", "bg"}, "", nil)

		assertNoSecret(t, out, errOut)

		if code != ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
		}

		if want := "profile: bg\nprovider: berget\nkey: file, ends in 6789\nmodels: 1\n"; out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}

		mu.Lock()
		defer mu.Unlock()

		if !slices.Equal(models, []string{"Qwen/Qwen3.5-2B"}) {
			t.Errorf("the question carried models %q, want the profile's", models)
		}
	})
}

type profileCase struct {
	name            string
	file            string
	old             string
	mode            os.FileMode
	unixOnly        bool
	keychain        string
	items           map[string]string
	store           []creds.StoreOption
	args            []string
	stdin           string
	stdinUnread     bool
	tty             bool
	secret          string
	env             map[string]string
	wantCode        int
	wantOut         string
	wantErr         []string
	notErr          []string
	wantFile        string
	wantNoFile      bool
	wantNoOld       bool
	wantItems       map[string]string
	noKeychainCalls bool
}

func runProfileCase(t *testing.T, tc profileCase) {
	t.Helper()

	if tc.unixOnly && runtime.GOOS == "windows" {
		t.Skip("windows carries no unix permission bits, so the mode check does not apply")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	fill := strings.NewReplacer(
		"{hash}", strings.TrimPrefix(creds.KeychainAccount("x", path), "x@"),
		"{path}", path,
	).Replace

	writeConfig(t, dir, "profiles.json", fill(tc.file), tc.mode)
	writeConfig(t, dir, "credentials.json", tc.old, 0)

	chain := workingKeychain(filled(tc.items, fill))
	opts := []RootOption{
		WithKeychain(chain),
		WithStdinTTY(tc.tty),
		WithSecretReader(func() (string, error) { return tc.secret, nil }),
	}

	switch tc.keychain {
	case keychainNone:
		opts = append(opts, WithKeychain(noKeychain()))
	case keychainTimeout:
		opts = append(opts, WithKeychain(timingOutKeychain{}))
	case keychainStalled:
		opts = append(opts, WithKeychain(stalledKeychain{chain}))
	case keychainUnreadable:
		opts = append(opts, WithKeychain(unreadableKeychain{chain}))
	case keychainUndeletable:
		opts = append(opts, WithKeychain(undeletableKeychain{chain}))
	case keychainUnrestorable:
		opts = append(opts, WithKeychain(&unrestorableKeychain{fakeKeychain: chain}))
	}

	if tc.store != nil {
		opts = append(opts, WithCredentialStore(creds.NewStore(tc.store...)))
	}

	if tc.stdinUnread {
		opts = append(opts, WithStdin(unreadStdin{t}))
	}

	out, errOut, code := runInConfig(t, dir, tc.args, tc.stdin, tc.env, opts...)

	// First, because every other assertion below would pass on output that had the key spliced
	// into it.
	assertNoSecret(t, out, errOut)

	if code != tc.wantCode {
		t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
	}

	if want := fill(tc.wantOut); out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}

	for _, want := range tc.wantErr {
		if !strings.Contains(errOut, fill(want)) {
			t.Errorf("stderr = %q, want it to contain %q", errOut, fill(want))
		}
	}

	for _, unwanted := range tc.notErr {
		if strings.Contains(errOut, unwanted) {
			t.Errorf("stderr = %q, want no %q", errOut, unwanted)
		}
	}

	if tc.wantFile != "" {
		assertProfiles(t, path, fill(tc.wantFile))
	}

	if tc.wantNoFile {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("profiles.json is there, stat error = %v", err)
		}
	}

	if tc.wantNoOld {
		if _, err := os.Stat(filepath.Join(dir, "credentials.json")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("credentials.json is still there, stat error = %v", err)
		}
	}

	// Names, never keys, in the message, since an item holds a key.
	if want := filled(tc.wantItems, fill); want != nil && !maps.Equal(chain.contents(), want) {
		t.Errorf("keychain items %v, want %v",
			slices.Sorted(maps.Keys(chain.contents())), slices.Sorted(maps.Keys(want)))
	}

	if tc.noKeychainCalls && chain.calls() != 0 {
		t.Errorf("keychain calls = %d, want none", chain.calls())
	}
}

func runInConfig(
	t *testing.T, dir string, args []string, stdin string, env map[string]string, opts ...RootOption,
) (string, string, int) {
	t.Helper()

	lookup := map[string]string{"ONESIE_CONFIG_DIR": dir}
	maps.Copy(lookup, env)

	var out, errOut bytes.Buffer

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, append([]RootOption{
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(lookup)),
		// Never the real keychain. A case that wants one working passes its own.
		WithKeychain(noKeychain()),
	}, opts...)...)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}

func configWith(t *testing.T, profiles string) string {
	t.Helper()

	dir := t.TempDir()
	writeConfig(t, dir, "profiles.json", profiles, 0)

	return dir
}

func writeConfig(t *testing.T, dir, name, content string, mode os.FileMode) {
	t.Helper()

	if content == "" {
		return
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating the config dir: %v", err)
	}

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}

	if mode == 0 {
		return
	}

	// WriteFile respects the umask, so the mode a case asks for is set explicitly.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("setting the mode of %s: %v", name, err)
	}
}

func assertProfiles(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading profiles.json: %v", err)
	}

	var got, wanted creds.Profiles

	if decodeErr := json.Unmarshal(data, &got); decodeErr != nil {
		t.Fatalf("profiles.json is not JSON: %v", decodeErr)
	}

	if decodeErr := json.Unmarshal([]byte(want), &wanted); decodeErr != nil {
		t.Fatalf("the wanted profiles are not JSON: %v", decodeErr)
	}

	// Names and the default in the message, never the profiles, since a profile can hold a key.
	if !reflect.DeepEqual(got, wanted) {
		t.Errorf("profiles.json holds %v with default %q, want %v with default %q, or a member differs",
			slices.Sorted(maps.Keys(got.Profiles)), got.Default, slices.Sorted(maps.Keys(wanted.Profiles)), wanted.Default)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stating profiles.json: %v", err)
	}

	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Errorf("profiles.json mode = %o, want no access for others", info.Mode().Perm())
	}
}

func filled(items map[string]string, fill func(string) string) map[string]string {
	if items == nil {
		return nil
	}

	out := make(map[string]string, len(items))
	for name, key := range items {
		out[fill(name)] = key
	}

	return out
}

func (k *fakeKeychain) contents() map[string]string {
	k.mu.Lock()
	defer k.mu.Unlock()

	return maps.Clone(k.items)
}

type unreadableKeychain struct {
	*fakeKeychain
}

func (unreadableKeychain) Get(string) (string, error) {
	return "", &creds.KeychainError{Op: "read the key from", Err: errors.New("the keychain is locked")}
}

type unreadStdin struct {
	t *testing.T
}

func (s unreadStdin) Read([]byte) (int, error) {
	s.t.Error("stdin was read, want every check to run before the key is read")

	return 0, io.EOF
}

type stalledKeychain struct {
	*fakeKeychain
}

func (stalledKeychain) Get(string) (string, error) {
	return "", &creds.KeychainError{Op: "read the key from", Err: creds.ErrKeychainTimeout}
}

type undeletableKeychain struct {
	*fakeKeychain
}

func (undeletableKeychain) Delete(string) error {
	return &creds.KeychainError{Op: "remove the key from", Err: errors.New("the keychain is locked")}
}

type unrestorableKeychain struct {
	*fakeKeychain

	sets int
}

func (k *unrestorableKeychain) Set(account, key string) error {
	k.sets++
	if k.sets > 1 {
		return &creds.KeychainError{Op: "store the key in", Err: errors.New("the keychain is locked")}
	}

	return k.fakeKeychain.Set(account, key)
}

type closedPipe struct{}

func (closedPipe) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

func unclearBaseURLs() []unclearBaseURL {
	return []unclearBaseURL{
		{name: "whose password holds an unescaped slash", url: "https://user:SECRET-ab/cd@proxy.example/v1"},
		{name: "whose password holds an invalid escape", url: "https://u:SECRET-p%zz@proxy.example"},
		{name: "whose password starts with digits and a slash", url: "https://user:1/SECRET-a@proxy.example"},
		{name: "whose password starts with a slash", url: "http://user:/SECRET-c@proxy.example"},
		{name: "with an @ in its path", url: "https://proxy.example/a@b"},
		{name: "with userinfo and no scheme", url: "u:SECRET-pw@proxy.example"},
		{name: "whose first // is not after a scheme", url: "u:SECRET-a@proxy.example//v1"},
		{name: "whose scheme has one slash", url: "https:/u:SECRET-b@h//v1"},
		{name: "with a user and a password", url: "https://user:SECRET-TOKEN@proxy.example/v1"},
		{name: "with a token as the user", url: "http://SECRET-TOKEN@proxy.example"},
		{name: "with an escaped colon in the user", url: "https://u%3ASECRET-p@proxy.example"},
	}
}

type unclearBaseURL struct {
	name string
	url  string
}
