package cli

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestNewRootCmd(t *testing.T) {
	t.Parallel()

	const (
		profiles = `{"default":"home","profiles":{` +
			`"home":{"provider":"typesafe","api_key":"SECRET-HOME","model":"home-model"},` +
			`"work":{"provider":"typesafe","api_key":"SECRET-WORK","model":"work-model"}}}`
		body = `{"model":"body-model","questions":{"q":{"type":"noul","instructions":"is it urgent"}}}`
	)

	asking := []string{"is it urgent", "--state", "x", "--print-request"}

	tests := []struct {
		name      string
		args      []string
		env       map[string]string
		wantModel string
		wantErr   string
	}{
		{
			name: "should take TYPESAFE_DEFAULT_MODEL over the default profile's model, with a notice",
			args: asking, env: map[string]string{"TYPESAFE_DEFAULT_MODEL": "env-model"},
			wantModel: "env-model", wantErr: "onesie: TYPESAFE_DEFAULT_MODEL overrides the default profile's model\n",
		},
		{
			name: "should keep a chosen profile's model over TYPESAFE_DEFAULT_MODEL, with a notice",
			args: append([]string{"--profile", "work"}, asking...), env: map[string]string{"TYPESAFE_DEFAULT_MODEL": "env-model"},
			wantModel: "work-model", wantErr: "onesie: the profile's model overrides TYPESAFE_DEFAULT_MODEL\n",
		},
		{
			name: "should keep a chosen profile's model when ONESIE_PROFILE chose it",
			args: asking, env: map[string]string{"ONESIE_PROFILE": "work", "TYPESAFE_DEFAULT_MODEL": "env-model"},
			wantModel: "work-model", wantErr: "onesie: the profile's model overrides TYPESAFE_DEFAULT_MODEL\n",
		},
		{
			name:      "should take a chosen profile's model over a body's model, with a notice",
			args:      []string{"-f", "{dir}/body.json", "--state", "x", "--print-request", "--profile", "work"},
			wantModel: "work-model", wantErr: "onesie: the profile's model overrides the request body's model\n",
		},
		{
			name:      "should keep a body's model over the default profile's model, with a notice",
			args:      []string{"-f", "{dir}/body.json", "--state", "x", "--print-request"},
			wantModel: "body-model", wantErr: "onesie: the request body's model overrides the default profile's model\n",
		},
		{
			name:      "should print a notice for each value -m overrides",
			args:      []string{"-f", "{dir}/body.json", "--state", "x", "--print-request", "-m", "flag-model"},
			wantModel: "flag-model", wantErr: "onesie: --model overrides the request body's model\n" +
				"onesie: --model overrides the default profile's model\n",
		},
		{
			name: "should print no notice for a flag equal to the value it outranks",
			args: append([]string{"-m", "home-model"}, asking...), wantModel: "home-model",
		},
		{
			name: "should accept ONESIE_CACHE and ONESIE_CACHE_TTL with spaces around them",
			args: asking, env: map[string]string{"ONESIE_CACHE": " 1", "ONESIE_CACHE_TTL": " 90m "},
			wantModel: "home-model",
		},
		{
			name: "should skip a malformed ONESIE_CACHE and ONESIE_LONG_CONTEXT outside their modes",
			args: []string{"--ask", "q=is it urgent", "--print-questions"},
			env:  map[string]string{"ONESIE_CACHE": "maybe", "ONESIE_CACHE_TTL": "soon", "ONESIE_MOCK": "/nonexistent"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := configWith(t, profiles)
			writeConfig(t, dir, "body.json", body, 0)

			args := make([]string, 0, len(tc.args))
			for _, arg := range tc.args {
				args = append(args, strings.ReplaceAll(arg, "{dir}/", dir+string(filepath.Separator)))
			}

			out, errOut, code := runInConfig(t, dir, args, "", tc.env)
			assertNoSecret(t, out, errOut)

			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			if tc.wantModel != "" && !strings.Contains(out, `"model":"`+tc.wantModel+`"`) {
				t.Errorf("stdout = %s, want the model %q", out, tc.wantModel)
			}

			if errOut != tc.wantErr {
				t.Errorf("stderr = %q, want %q", errOut, tc.wantErr)
			}
		})
	}

	t.Run("should refuse --provider with ONESIE_PROFILE after a flag clash", func(t *testing.T) {
		t.Parallel()

		env := map[string]string{"ONESIE_PROFILE": "work"}

		_, errOut, code := runInConfig(t, configWith(t, profiles), append([]string{"--provider", "openrouter"}, asking...),
			"", env)
		if want := "onesie: --provider and ONESIE_PROFILE both choose what the run uses. Drop --provider or unset " +
			"ONESIE_PROFILE\n"; code != ExitUsage || errOut != want {
			t.Errorf("exit code = %d, stderr = %q, want %d and %q", code, errOut, ExitUsage, want)
		}

		_, errOut, code = runInConfig(t, configWith(t, profiles),
			[]string{"--provider", "openrouter", "is it urgent", "-i", "jsonl", "-o", "table"}, "", env)
		if want := "onesie: -o table reads one record. Drop -i jsonl or use -o json\n"; code != ExitUsage || errOut != want {
			t.Errorf("exit code = %d, stderr = %q, want %d and the clash %q", code, errOut, ExitUsage, want)
		}
	})

	t.Run("should choose the provider and profile in one order, with a notice", func(t *testing.T) {
		t.Parallel()

		choices := []struct {
			name    string
			args    []string
			env     map[string]string
			wantErr string
		}{
			{
				name:    "should name the default profile it runs without under --provider",
				args:    append([]string{"--provider", "openrouter"}, asking...),
				wantErr: "onesie: --provider openrouter runs without the default profile 'home'\n",
			},
			{
				name: "should name the default profile it runs without under ONESIE_PROVIDER",
				args: asking, env: map[string]string{"ONESIE_PROVIDER": "openrouter"},
				wantErr: "onesie: ONESIE_PROVIDER=openrouter runs without the default profile 'home'\n",
			},
			{
				name: "should note --profile over ONESIE_PROVIDER",
				args: append([]string{"--profile", "work"}, asking...), env: map[string]string{"ONESIE_PROVIDER": "openrouter"},
				wantErr: "onesie: --profile work overrides ONESIE_PROVIDER\n",
			},
			{
				name: "should note --profile over ONESIE_PROFILE",
				args: append([]string{"--profile", "work"}, asking...), env: map[string]string{"ONESIE_PROFILE": "home"},
				wantErr: "onesie: --profile work overrides ONESIE_PROFILE\n",
			},
			{
				name:    "should note --profile over ONESIE_PROVIDER under --print-questions",
				args:    []string{"--ask", "q=is it urgent", "--print-questions", "--profile", "work"},
				env:     map[string]string{"ONESIE_PROVIDER": "openrouter"},
				wantErr: "onesie: --profile work overrides ONESIE_PROVIDER\n",
			},
			{
				name:    "should name the default profile under --provider and --print-questions",
				args:    []string{"--ask", "q=is it urgent", "--print-questions", "--provider", "berget"},
				wantErr: "onesie: --provider berget runs without the default profile 'home'\n",
			},
		}

		for _, tc := range choices {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				out, errOut, code := runInConfig(t, configWith(t, profiles), tc.args, "", tc.env)
				assertNoSecret(t, out, errOut)

				if code != ExitOK || errOut != tc.wantErr {
					t.Errorf("exit code = %d, stderr = %q, want %d and %q", code, errOut, ExitOK, tc.wantErr)
				}
			})
		}
	})

	t.Run("should drop the notice when profiles.json is exposed under --provider", func(t *testing.T) {
		t.Parallel()

		if runtime.GOOS == "windows" {
			t.Skip("windows has no group or other mode bits to expose the file through")
		}

		dir := t.TempDir()
		writeConfig(t, dir, "profiles.json", profiles, 0o644)

		_, errOut, code := runInConfig(t, dir, append([]string{"--provider", "openrouter"}, asking...), "", nil)
		if code != ExitOK || errOut != "" {
			t.Errorf("exit code = %d, stderr = %q, want %d and none", code, errOut, ExitOK)
		}
	})

	t.Run("should not convert credentials.json under --provider", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		writeConfig(t, dir, "credentials.json", `{"providers":{"typesafe":{"api_key":"SECRET-OLD"}}}`, 0)

		out, errOut, code := runInConfig(t, dir, append([]string{"--provider", "openrouter"}, asking...), "", nil)
		assertNoSecret(t, out, errOut)

		if code != ExitOK || errOut != "" {
			t.Errorf("exit code = %d, stderr = %q, want %d and none", code, errOut, ExitOK)
		}

		if _, err := os.Stat(filepath.Join(dir, "profiles.json")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("profiles.json stat error = %v, want it never written", err)
		}
	})

	t.Run("should skip ONESIE_MOCK silently under --list-models", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[]}`))
		}))
		defer srv.Close()

		_, errOut, code := runInConfig(t, t.TempDir(), []string{"--list-models"}, "", map[string]string{
			"ONESIE_MOCK": writeMock(t, `{"answer":0.9}`), envBaseURL: srv.URL, "TYPESAFE_API_KEY": "SECRET-ENV",
		})
		if code != ExitOK || errOut != "" {
			t.Errorf("exit code = %d, stderr = %q, want %d and none", code, errOut, ExitOK)
		}
	})

	t.Run("should refuse a malformed CLOUDFLARE_ACCOUNT_ID on a cloudflare run under --mock", func(t *testing.T) {
		t.Parallel()

		_, errOut, code := runMocked(t, t.Context(),
			[]string{"is it urgent", "--state", "x", "--provider", "cloudflare", "--mock", writeMock(t, `{"answer":0.9}`)},
			"", map[string]string{"CLOUDFLARE_ACCOUNT_ID": "acct"}, nil)
		if code != ExitUsage || errOut != "onesie: CLOUDFLARE_ACCOUNT_ID must be 32 hexadecimal characters\n" {
			t.Errorf("exit code = %d, stderr = %q, want %d and the account refusal", code, errOut, ExitUsage)
		}
	})
}
