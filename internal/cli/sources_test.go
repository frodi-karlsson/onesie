package cli

import (
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/cache"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func TestNewRootCmd(t *testing.T) {
	t.Parallel()

	t.Run("should match the gate merge compatibility cases", checkMergedGateCases)

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

	t.Run("should print no base URL or key notice for a run that sends nothing", func(t *testing.T) {
		t.Parallel()

		env := map[string]string{"ONESIE_BASE_URL": "https://env.example", "TYPESAFE_API_KEY": "SECRET-ENV"}

		for _, args := range [][]string{
			{"is it urgent", "--state", "x", "--mock", writeMock(t, `{"answer":0.9}`)},
			{"calibrate", "--ask", "u=is it urgent", "-i", "jsonl", "--map", ".body", "--label", "u=.u", "--print-request"},
		} {
			out, errOut, code := runInConfig(t, configWith(t, profiles), args, "{\"body\":\"x\",\"u\":true}\n", env)
			assertNoSecret(t, out, errOut)

			if code != ExitOK || errOut != "" {
				t.Errorf("%v: exit code = %d, stderr = %q, want %d and none", args, code, errOut, ExitOK)
			}
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
			"ONESIE_MOCK": writeMock(t, `{"answer":0.9}`), "ONESIE_BASE_URL": srv.URL, "TYPESAFE_API_KEY": "SECRET-ENV",
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

func TestMergeRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		args    []string
		want    switches
		wantErr string
	}{
		{name: "should read no variable as every switch off"},
		{name: "should read empty variables as unset", env: map[string]string{envCache: "", envLongContext: "", envCacheTTL: ""}},
		{
			name: "should read what the flags take",
			env:  map[string]string{envCache: "true", envLongContext: "1", envCacheTTL: "90m"},
			want: switches{cache: true, cacheFrom: setting.SourceEnv, longContext: true, aliasTTL: 90 * time.Minute},
		},
		{
			name: "should accept ONESIE_CACHE and ONESIE_CACHE_TTL with spaces around them",
			env:  map[string]string{envCache: " 1", envCacheTTL: " 90m "},
			want: switches{cache: true, cacheFrom: setting.SourceEnv, aliasTTL: 90 * time.Minute},
		},
		{name: "should read a blank variable as unset", env: map[string]string{envCache: "  ", envCacheTTL: "\t"}},
		{name: "should read ONESIE_CACHE_TTL=0 as no alias lifetime", env: map[string]string{envCacheTTL: "0"}, want: switches{aliasTTL: cache.NoAliasTTL}},
		{name: "should be on under --cache", args: []string{"--cache"}, want: switches{cache: true, cacheFrom: setting.SourceFlag}},
		{name: "should let --cache=false win over ONESIE_CACHE=1", env: map[string]string{envCache: "1"}, args: []string{"--cache=false"}, want: switches{cacheFrom: setting.SourceFlag}},
		{name: "should let --cache win over ONESIE_CACHE=0", env: map[string]string{envCache: "0"}, args: []string{"--cache"}, want: switches{cache: true, cacheFrom: setting.SourceFlag}},
		{
			name: "should let --long-context=false win over ONESIE_LONG_CONTEXT=1",
			env:  map[string]string{envLongContext: "1"}, args: []string{"--long-context=false"},
		},
		{
			name: "should skip a malformed ONESIE_CACHE under --print-request",
			env:  map[string]string{envCache: "maybe", envCacheTTL: "soon"}, args: []string{"--print-request"},
		},
		{
			name:    "should refuse an ONESIE_LONG_CONTEXT the flag would not take",
			env:     map[string]string{envLongContext: "garbage"},
			wantErr: "ONESIE_LONG_CONTEXT=garbage is not a value --long-context takes. Use 1 or 0",
		},
		{
			name:    "should refuse an ONESIE_CACHE the flag would not take, even beside the flag",
			env:     map[string]string{envCache: "yes"},
			args:    []string{"--cache"},
			wantErr: "ONESIE_CACHE=yes is not a value --cache takes. Use 1 or 0",
		},
		{
			name:    "should refuse a negative ONESIE_CACHE_TTL",
			env:     map[string]string{envCacheTTL: "-1h"},
			wantErr: "ONESIE_CACHE_TTL=-1h is not a duration of zero or more, such as 90m or 72h",
		},
		{
			name:    "should refuse an ONESIE_CACHE_TTL that is no duration",
			env:     map[string]string{envCacheTTL: "soon"},
			wantErr: "ONESIE_CACHE_TTL=soon is not a duration of zero or more, such as 90m or 72h",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flags := &runFlags{}
			cmd := &cobra.Command{Use: "onesie"}
			flags.given = setting.Register(cmd.Flags(), cmd.PersistentFlags(), settingrow.Table(), argv.New(),
				settingrow.CommandRoot, runTargets(flags))
			cmd.Flags().AddFlagSet(cmd.PersistentFlags())

			if err := cmd.Flags().Parse(tc.args); err != nil {
				t.Fatal(err)
			}

			settings := defaultSettings(BuildInfo{})
			settings.lookupEnv = lookupFrom(tc.env)
			settings.holder = newHolder(settings, flags, func() io.Writer { return io.Discard })

			early, err := setting.ParseRun(settingrow.Table(), flags.given, settingrow.CommandRoot)
			if err != nil {
				t.Fatal(err)
			}
			config, err := newRunConfig(early, flags.given, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			merged, err := mergeRun(settings, config, flags.given, settingrow.CommandRoot, nil)
			got := merged.config

			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr || Classify(err) != ExitUsage {
					t.Fatalf("mergeRun = %v, want the usage error %q", err, tc.wantErr)
				}

				return
			}

			if err != nil || got.env != tc.want {
				t.Errorf("mergeRun switches = %+v, %v, want %+v", got.env, err, tc.want)
			}
		})
	}
}

func TestConnectionEnv(t *testing.T) {
	t.Parallel()

	env := lookupFrom(map[string]string{
		"TYPESAFE_API_KEY": "SECRET-ENV", "ONESIE_BASE_URL": "https://env.example", "TYPESAFE_DEFAULT_MODEL": "m",
		"CLOUDFLARE_ACCOUNT_ID": "acct", "ONESIE_PROFILE": "work", "ONESIE_PROVIDER": "levanto", "ONESIE_CACHE": "1",
	})

	tests := []struct {
		name     string
		variable string
		want     string
		found    bool
	}{
		{name: "should pass a key variable through", variable: "TYPESAFE_API_KEY", want: "SECRET-ENV", found: true},
		{name: "should pass a base URL variable through", variable: "ONESIE_BASE_URL", want: "https://env.example", found: true},
		{name: "should pass the model variable through", variable: "TYPESAFE_DEFAULT_MODEL", want: "m", found: true},
		{name: "should pass the account variable through", variable: "CLOUDFLARE_ACCOUNT_ID", want: "acct", found: true},
		{name: "should hide ONESIE_PROFILE", variable: "ONESIE_PROFILE"},
		{name: "should hide a variable no connection row reads", variable: "ONESIE_CACHE"},
		{name: "should hide ONESIE_PROVIDER even when the test chooses it", variable: "ONESIE_PROVIDER"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, found := connectionEnv(env)(tc.variable)
			if got != tc.want || found != tc.found {
				t.Errorf("connectionEnv(%s) = %q, %t, want %q, %t", tc.variable, got, found, tc.want, tc.found)
			}
		})
	}
}
