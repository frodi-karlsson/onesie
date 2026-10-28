package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
)

func TestClientBuilder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		profiles string
		args     []string
		env      map[string]string
		want     string
	}{
		{
			name: "should refuse a --base-url whose password holds a question mark",
			args: []string{"is this urgent", "--state", "hello", "-o", "json", "--base-url", "https://u:1?SECRET-x@proxy.example"},
			env:  map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			want: "onesie: --base-url" + unsafeBaseURL + keyElsewhere,
		},
		{
			name: "should refuse a --base-url with a query before any request",
			args: []string{"--list-models", "--base-url", "{url}/?SECRET-x"},
			env:  map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			want: "onesie: --base-url" + unsafeBaseURL,
		},
		{
			name: "should refuse a base url variable with a fragment before any request",
			args: []string{"is this urgent", "--state", "hello", "-o", "json"},
			env:  map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV", onesie.TypeSafe().EnvBaseURL(): "{url}/#SECRET-x"},
			want: "onesie: TYPESAFE_BASE_URL" + unsafeBaseURL,
		},
		{
			name: "should refuse a profile's base url with a query before profile test sends a request",
			profiles: `{"default":"ts","profiles":{"ts":{"provider":"typesafe","api_key":"SECRET-TS",` +
				`"base_url":"{url}/?SECRET-x"}}}`,
			args: []string{"profile", "test"},
			want: "onesie: the profile's base_url" + unsafeBaseURL,
		},
		{
			name: "should refuse a keychain profile's base url with an @ in its path before reading the keychain",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","store":"keychain",` +
				`"account":"work@abc","base_url":"{url}/a@b"}}}`,
			args: []string{"is this urgent", "--state", "hello", "-o", "json"},
			want: "onesie: the profile's base_url" + unsafeBaseURL + keyElsewhere,
		},
		{
			name: "should refuse a cloudflare keychain profile with no account before reading the keychain",
			profiles: `{"default":"cf","profiles":{"cf":{"provider":"cloudflare","store":"keychain",` +
				`"account":"work@abc"}}}`,
			args: []string{"is this urgent", "--state", "hello", "-o", "json"},
			want: "onesie: no cloudflare account id",
		},
		{
			name: "should refuse a cloudflare keychain profile with a malformed account variable before reading the keychain",
			profiles: `{"default":"cf","profiles":{"cf":{"provider":"cloudflare","store":"keychain",` +
				`"account":"work@abc"}}}`,
			args: []string{"is this urgent", "--state", "hello", "-o", "json"},
			env:  map[string]string{onesie.Cloudflare().EnvAccountID(): "not-hex"},
			want: "CLOUDFLARE_ACCOUNT_ID must be 32 hexadecimal characters",
		},
	}

	for _, unclear := range unclearBaseURLs() {
		// The host is swapped for the local server's, so a request that goes out is counted.
		tests = append(tests, struct {
			name     string
			profiles string
			args     []string
			env      map[string]string
			want     string
		}{
			name: "should refuse a --base-url " + unclear.name + " before any request",
			args: []string{"--list-models", "--base-url", strings.ReplaceAll(unclear.url, "proxy.example", "{host}")},
			env:  map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			want: "onesie: --base-url" + unsafeBaseURL + keyElsewhere,
		})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var requests atomic.Int32

			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests.Add(1)
			}))
			defer srv.Close()

			withURL := strings.NewReplacer("{url}", srv.URL, "{host}", strings.TrimPrefix(srv.URL, "http://")).Replace

			env := map[string]string{}
			for name, value := range tc.env {
				env[name] = withURL(value)
			}

			args := make([]string, 0, len(tc.args))
			for _, arg := range tc.args {
				args = append(args, withURL(arg))
			}

			dir := t.TempDir()
			if tc.profiles != "" {
				dir = configWith(t, withURL(tc.profiles))
			}

			chain := workingKeychain(map[string]string{"work@abc": "SECRET-KC"})

			out, errOut, code := runInConfig(t, dir, args, "", env, withKeychain(chain))

			assertNoSecret(t, out, errOut)

			if chain.calls() != 0 {
				t.Errorf("keychain calls = %d, want none", chain.calls())
			}

			if code != ExitUsage {
				t.Errorf("exit code = %d, want %d", code, ExitUsage)
			}

			if !strings.Contains(out+errOut, tc.want) {
				t.Errorf("stdout = %q, stderr = %q, want either to carry %q", out, errOut, tc.want)
			}

			if got := requests.Load(); got != 0 {
				t.Errorf("the server saw %d requests, want none", got)
			}
		})
	}

	t.Run("should refuse a keychain profile's base url with no host before reading the keychain", func(t *testing.T) {
		t.Parallel()

		dir := configWith(t, `{"default":"work","profiles":{"work":{"provider":"typesafe","store":"keychain",`+
			`"account":"work@abc","base_url":"https://"}}}`)
		chain := workingKeychain(map[string]string{"work@abc": "SECRET-KC"})

		_, errOut, code := runInConfig(t, dir, []string{"is this urgent", "--state", "hello"}, "", nil, withKeychain(chain))
		if code != ExitUsage || chain.calls() != 0 {
			t.Errorf("exit code = %d, keychain calls = %d, stderr = %q, want %d and no keychain call",
				code, chain.calls(), errOut, ExitUsage)
		}
	})

	t.Run("should refuse a base url onesie-go refuses in its words", func(t *testing.T) {
		t.Parallel()

		refusals := []struct {
			name string
			args []string
			want string
		}{
			{
				name: "should say a --base-url with a scheme and no host names no host",
				args: []string{"--base-url", "https://"},
				want: "onesie: --base-url https:// names no host\n",
			},
			{
				name: "should say a plain http --base-url with no host names no host",
				args: []string{"--base-url", "http://"},
				want: "onesie: --base-url http:// names no host\n",
			},
			{
				name: "should say a --base-url with no scheme is not http or https",
				args: []string{"--base-url", "proxy.example"},
				want: "onesie: --base-url is not an http or https URL\n",
			},
			{
				name: "should word profile create the same way",
				args: []string{"profile", "create", "w", "--provider", "typesafe", "--base-url", "https://"},
				want: "onesie: --base-url https:// names no host\n",
			},
		}

		for _, tc := range refusals {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				args := tc.args
				if args[0] != "profile" {
					args = append([]string{"is this urgent", "--state", "x"}, args...)
				}

				_, errOut, code := runInConfig(t, t.TempDir(), args, "SECRET-NEW-0123456789\n",
					map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-TEST"})
				if code != ExitUsage || errOut != tc.want {
					t.Errorf("exit code = %d, stderr = %q, want %d and %q", code, errOut, ExitUsage, tc.want)
				}
			})
		}
	})

	question := []string{"is this urgent", "-r", "--state", "the server is down"}
	asking := func(extra ...string) []string {
		return append(append([]string{}, question...), extra...)
	}

	const (
		fileWork = `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
			`"base_url":"{url}"}}}`
		pinnedWork = `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
			`"base_url":"{url}","model":"jev-1.13.0"}}}`
	)

	runs := []struct {
		name      string
		profiles  string
		args      []string
		stdin     string
		env       map[string]string
		items     map[string]string
		wantCode  int
		anyCode   bool
		wantAuth  string
		wantModel string
		wantErr   []string
	}{
		{
			name: "should send the profile's key and not the provider's variable", profiles: fileWork,
			args: asking(), env: map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV", onesie.TypeSafe().EnvBaseURL(): "{other}"},
			wantAuth: "Bearer SECRET-WORK", wantModel: onesie.TypeSafe().DefaultModel(),
		},
		{
			name: "should send the key the profile's keychain item holds",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","store":"keychain",` +
				`"account":"work@abc","base_url":"{url}"}}}`,
			args: asking(), items: map[string]string{"work@abc": "SECRET-KC"},
			wantAuth: "Bearer SECRET-KC", wantModel: onesie.TypeSafe().DefaultModel(),
		},
		{
			name: "should send the profile's model", profiles: pinnedWork, args: asking(),
			wantAuth: "Bearer SECRET-WORK", wantModel: "jev-1.13.0",
		},
		{
			name: "should let -m beat the profile's model", profiles: pinnedWork, args: asking("-m", "other"),
			wantAuth: "Bearer SECRET-WORK", wantModel: "other",
		},
		{
			name: "should let the profile's model beat TYPESAFE_DEFAULT_MODEL", profiles: pinnedWork, args: asking(),
			env:      map[string]string{onesie.TypeSafe().EnvDefaultModel(): "env-model"},
			wantAuth: "Bearer SECRET-WORK", wantModel: "jev-1.13.0",
		},
		{
			name: "should take TYPESAFE_DEFAULT_MODEL when the profile names no model", profiles: fileWork,
			args: asking(), env: map[string]string{onesie.TypeSafe().EnvDefaultModel(): "env-model"},
			wantAuth: "Bearer SECRET-WORK", wantModel: "env-model",
		},
		{
			name: "should route an -i request body that names no model by the profile's model",
			profiles: `{"default":"cf","profiles":{"cf":{"provider":"cloudflare","api_key":"SECRET-CF",` +
				`"base_url":"{url}","model":"typesafe/jev-1.13"}}}`,
			args:     []string{"-i", "request"},
			stdin:    `{"state":"x","questions":{"a":{"type":"noul"}}}` + "\n",
			anyCode:  true,
			wantAuth: "Bearer SECRET-CF", wantModel: "typesafe/jev-1.13",
		},
		{
			name:     "should send the profile's model from calibrate",
			profiles: pinnedWork,
			args: []string{
				"calibrate", "--ask", "urgent=is this urgent", "-i", "jsonl", "--map", ".body", "--label", "urgent=.u",
			},
			stdin:    `{"body":"the server is down","u":true}` + "\n",
			anyCode:  true,
			wantAuth: "Bearer SECRET-WORK", wantModel: "jev-1.13.0",
		},
		{
			name: "should keep the profile's base_url ahead of TYPESAFE_BASE_URL", profiles: fileWork, args: asking(),
			env:      map[string]string{onesie.TypeSafe().EnvBaseURL(): "{other}"},
			wantAuth: "Bearer SECRET-WORK", wantModel: onesie.TypeSafe().DefaultModel(),
		},
		{
			name: "should keep --base-url ahead of the profile's base_url",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
				`"base_url":"{other}"}}}`,
			args:     asking("--base-url", "{url}"),
			wantAuth: "Bearer SECRET-WORK", wantModel: onesie.TypeSafe().DefaultModel(),
		},
		{
			name: "should name the profile's base_url when it ends in /v1",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
				`"base_url":"{url}/v1"}}}`,
			args:     asking(),
			wantErr:  []string{"onesie: warning: the profile's base_url ends in /v1 and onesie adds the API path itself"},
			wantAuth: "Bearer SECRET-WORK", wantModel: onesie.TypeSafe().DefaultModel(),
		},
		{
			name:     "should name profile update for a cloudflare profile with no account",
			profiles: `{"default":"cf","profiles":{"cf":{"provider":"cloudflare","api_key":"SECRET-CF"}}}`,
			args:     asking(),
			wantCode: ExitUsage,
			wantErr: []string{
				"no cloudflare account id",
				"or run onesie profile update 'cf' --account-id ACCOUNT_ID",
			},
		},
		{
			name: "should exit 3 when the profile's keychain item is gone",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","store":"keychain",` +
				`"account":"work@abc","base_url":"{url}"}}}`,
			args:     asking(),
			wantCode: ExitAuth,
			wantErr:  []string{"the keychain holds no key for the profile"},
		},
	}

	for _, tc := range runs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wanted, sent := modelServer(t)
			unwanted, strays := modelServer(t)
			fill := strings.NewReplacer("{url}", wanted.URL, "{other}", unwanted.URL).Replace

			env := map[string]string{}
			for name, value := range tc.env {
				env[name] = fill(value)
			}

			args := make([]string, 0, len(tc.args))
			for _, arg := range tc.args {
				args = append(args, fill(arg))
			}

			out, errOut, code := runInConfig(t, configWith(t, fill(tc.profiles)), args, tc.stdin, env,
				withKeychain(workingKeychain(tc.items)))

			assertNoSecret(t, out, errOut)

			if !tc.anyCode && code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
			}

			for _, want := range tc.wantErr {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, want)
				}
			}

			got := sent()

			// Compared, never printed, since the header carries the key.
			switch {
			case tc.wantAuth == "" && len(got) != 0:
				t.Errorf("the run sent %d requests, want none", len(got))
			case tc.wantAuth != "" && (len(got) != 1 || got[0].auth != tc.wantAuth || got[0].model != tc.wantModel):
				t.Errorf("the run sent %d requests, want one with the expected key and model %q", len(got), tc.wantModel)
			}

			if len(strays()) != 0 {
				t.Error("the outranked base url took a request")
			}
		})
	}

	plain := []struct {
		name     string
		profiles string
		args     []string
		env      map[string]string
		wantCode int
		wantErr  []string
		notErr   string
	}{
		{
			name:     "should refuse a plain http --base-url to another host",
			args:     []string{"--list-models", "--base-url", "http://proxy.example"},
			env:      map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			wantCode: ExitUsage,
			wantErr: []string{"onesie: --base-url http://proxy.example is plain http to a host other than this " +
				"machine, so the API key would cross the network unencrypted. Use https, or pass --allow-http"},
		},
		{
			name:     "should refuse a plain http TYPESAFE_BASE_URL to another host",
			args:     asking(),
			env:      map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV", onesie.TypeSafe().EnvBaseURL(): "http://proxy.example"},
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: TYPESAFE_BASE_URL http://proxy.example is plain http", "--allow-http"},
		},
		{
			name: "should refuse a profile's plain http base_url to another host",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
				`"base_url":"http://proxy.example"}}}`,
			args:     asking(),
			wantCode: ExitUsage,
			wantErr:  []string{"onesie: the profile's base_url http://proxy.example is plain http", "--allow-http"},
		},
		{
			name: "should refuse a profile's plain http base_url before profile test sends a request",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
				`"base_url":"http://proxy.example"}}}`,
			args:     []string{"profile", "test", "work"},
			wantCode: ExitUsage,
			wantErr:  []string{"is plain http", "--allow-http"},
		},
		{
			name:     "should refuse a plain http --base-url from calibrate",
			args:     []string{"calibrate", "--ask", "u=is this urgent", "-i", "jsonl", "--map", ".body", "--label", "u=.u", "--base-url", "http://proxy.example"},
			env:      map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			wantCode: ExitUsage,
			wantErr:  []string{"is plain http", "--allow-http"},
		},
		{
			name:     "should not warn for a cloudflare --base-url ending in /v1, since its API path is /run",
			args:     []string{"--list-models", "--provider", "cloudflare", "--base-url", "{url}/v1"},
			env:      map[string]string{onesie.Cloudflare().EnvAPIKey(): "SECRET-CF"},
			wantCode: ExitUnavailable,
			notErr:   "ends in /",
		},
		{
			name:     "should name the segment a cloudflare --base-url repeats and the URL only questions go to",
			args:     []string{"--list-models", "--provider", "cloudflare", "--base-url", "{url}/run"},
			env:      map[string]string{onesie.Cloudflare().EnvAPIKey(): "SECRET-CF"},
			wantCode: ExitUnavailable,
			wantErr: []string{
				"onesie: warning: --base-url ends in /run and onesie adds the API path itself, so questions go to ",
				"/run/run",
			},
			notErr: "requests go to",
		},
		{
			name:     "should send to a plain http server on this machine without --allow-http",
			args:     []string{"--list-models", "--base-url", "{url}"},
			env:      map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			wantCode: ExitOK,
			notErr:   "plain http",
		},
		{
			name:     "should send to this machine under --allow-http without a warning",
			args:     []string{"--list-models", "--base-url", "{url}", "--allow-http"},
			env:      map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"},
			wantCode: ExitOK,
			notErr:   "plain http",
		},
		{
			name: "should let profile test take --allow-http",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
				`"base_url":"{url}"}}}`,
			args:     []string{"profile", "test", "work", "--allow-http"},
			wantCode: ExitOK,
			notErr:   "plain http",
		},
	}

	for _, tc := range plain {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// A loopback server, so no case sends a key off this machine, whatever proxy the
			// environment names.
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"models":[]}`))
			}))
			defer srv.Close()

			fill := strings.NewReplacer("{url}", srv.URL).Replace

			dir := t.TempDir()
			if tc.profiles != "" {
				dir = configWith(t, fill(tc.profiles))
			}

			args := make([]string, 0, len(tc.args))
			for _, arg := range tc.args {
				args = append(args, fill(arg))
			}

			out, errOut, code := runInConfig(t, dir, args, `{"body":"x","u":true}`+"\n", tc.env)

			assertNoSecret(t, out, errOut)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
			}

			for _, want := range tc.wantErr {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, want)
				}
			}

			if tc.notErr != "" && strings.Contains(errOut, tc.notErr) {
				t.Errorf("stderr = %q, want no %q", errOut, tc.notErr)
			}
		})
	}

	t.Run("should send each openrouter profile its own key", func(t *testing.T) {
		t.Parallel()

		srv, sent := modelServer(t)
		dir := configWith(t, `{"profiles":{`+
			`"a":{"provider":"openrouter","api_key":"SECRET-A","base_url":"`+srv.URL+`"},`+
			`"b":{"provider":"openrouter","api_key":"SECRET-B","base_url":"`+srv.URL+`"}}}`)

		for _, profile := range []string{"a", "b"} {
			out, errOut, code := runInConfig(t, dir, asking("--profile", profile), "", nil)

			assertNoSecret(t, out, errOut)

			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}
		}

		if got := sent(); len(got) != 2 || got[0].auth != "Bearer SECRET-A" || got[1].auth != "Bearer SECRET-B" {
			t.Error("the two openrouter profiles did not each send their own key")
		}
	})

	t.Run("should warn that --allow-http sends the key over plain http", func(t *testing.T) {
		t.Parallel()

		var host atomic.Value

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host.Store(r.Host)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"models":[]}`))
		}))
		defer srv.Close()

		out, errOut, code := runInConfig(t, t.TempDir(),
			[]string{"--list-models", "--allow-http", "--base-url", "http://proxy.example"}, "",
			map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV"}, withDialTo(srv.Listener.Addr().String()))

		assertNoSecret(t, out, errOut)

		const want = "onesie: warning: http://proxy.example is plain http, so the API key crosses the network unencrypted\n"

		if code != ExitOK || !strings.Contains(errOut, want) {
			t.Errorf("exit code = %d, stderr = %q, want %d and %q", code, errOut, ExitOK, want)
		}

		if got, _ := host.Load().(string); got != "proxy.example" {
			t.Errorf("the local server saw host %q, want proxy.example", got)
		}
	})
}

func modelServer(t *testing.T) (*httptest.Server, func() []sentRequest) {
	t.Helper()

	var mu sync.Mutex

	var sent []sentRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model     string                     `json:"model"`
			Questions map[string]json.RawMessage `json:"questions"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding the request body: %v", err)
		}

		mu.Lock()
		sent = append(sent, sentRequest{auth: r.Header.Get("Authorization"), model: body.Model})
		mu.Unlock()

		answers := map[string]any{}
		for id := range body.Questions {
			answers[id] = map[string]any{"type": "noul", "noul": 0.5}
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(map[string]any{"model": body.Model, "answers": answers}); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))

	t.Cleanup(srv.Close)

	return srv, func() []sentRequest {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(sent)
	}
}

type sentRequest struct {
	auth  string
	model string
}

func withDialTo(addr string) RootOption {
	// Every connection goes to addr, whatever host the request names, so a remote base URL stays on
	// this machine. Proxy is left unset, so no proxy the environment names is used.
	return func(s *rootSettings) {
		s.transport = &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, addr)
			},
		}
	}
}

func TestPooled(t *testing.T) {
	t.Parallel()

	t.Run("should clone the base transport with a pool of -j connections per host", func(t *testing.T) {
		t.Parallel()

		base := &http.Transport{MaxIdleConns: 2}

		got, ok := pooled(base, 8).(*http.Transport)
		if !ok || got == base {
			t.Fatalf("pooled = %T, want a clone of the base transport", pooled(base, 8))
		}

		if got.MaxIdleConnsPerHost != 8 || got.MaxConnsPerHost != 8 || got.MaxIdleConns != 8 {
			t.Errorf("pool = %d idle per host, %d per host, %d idle, want 8 each",
				got.MaxIdleConnsPerHost, got.MaxConnsPerHost, got.MaxIdleConns)
		}

		if base.MaxIdleConnsPerHost != 0 {
			t.Errorf("the base transport changed to %d idle per host, want it left alone", base.MaxIdleConnsPerHost)
		}
	})

	t.Run("should size the pool for one job when -j is below one", func(t *testing.T) {
		t.Parallel()

		got, ok := pooled(&http.Transport{MaxIdleConns: 2}, 0).(*http.Transport)
		if !ok || got.MaxIdleConnsPerHost != 1 || got.MaxConnsPerHost != 1 || got.MaxIdleConns != 2 {
			t.Errorf("pooled = %+v, want one connection per host and the base's two idle", got)
		}
	})

	t.Run("should leave the client its own transport when the base is not an http.Transport", func(t *testing.T) {
		t.Parallel()

		if got := pooled(&countingRoundTripper{base: http.DefaultTransport}, 4); got != nil {
			t.Errorf("pooled = %T, want nil", got)
		}
	})
}

func TestProfileKey(t *testing.T) {
	t.Parallel()

	file, err := profileRun("ts", creds.Profile{Provider: "typesafe", APIKey: "SECRET-FILE"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	keychained, err := profileRun("work",
		creds.Profile{Provider: "typesafe", Store: creds.StoreKeychain, Account: "work@abc"}, "--profile")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		run          resolution
		items        map[string]string
		want         string
		wantKeychain bool
	}{
		{name: "should give a file profile's key without the keychain", run: file, want: "SECRET-FILE"},
		{
			name: "should read a keychain profile's key from the keychain", run: keychained,
			items: map[string]string{"work@abc": "SECRET-KC"}, want: "SECRET-KC",
		},
		{
			name: "should refuse an empty keychain item as a keychain failure", run: keychained,
			items: map[string]string{"work@abc": ""}, wantKeychain: true,
		},
		{name: "should pass a missing keychain item on as a keychain failure", run: keychained, wantKeychain: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			chain := workingKeychain(tc.items)
			settings := rootSettings{keychain: creds.NewKeychain(creds.WithBackend(chain))}

			got, err := profileKey(settings, tc.run)()

			var keychainErr *creds.KeychainError
			if errors.As(err, &keychainErr) != tc.wantKeychain || got != tc.want {
				t.Errorf("key = %q, error = %v, want %q and a keychain failure %v", got, err, tc.want, tc.wantKeychain)
			}

			if tc.run.keyFrom == sourceFile && chain.calls() != 0 {
				t.Errorf("keychain calls = %d, want none for a file profile", chain.calls())
			}
		})
	}
}

func TestBaseURLFrom(t *testing.T) {
	t.Parallel()

	profileOf := func(profile creds.Profile) resolution {
		run, err := profileRun("p", profile, "--profile")
		if err != nil {
			t.Fatal(err)
		}

		return run
	}

	bare, err := providerRun("typesafe", "nothing", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	cloudflare, err := providerRun("cloudflare", "--provider", lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		source onesie.BaseURLSource
		run    resolution
		flag   string
		want   string
	}{
		{
			name: "should name --base-url when the flag was given", source: onesie.BaseURLFromOption,
			run:  profileOf(creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", BaseURL: "https://p.example"}),
			flag: " https://flag.example ", want: "--base-url",
		},
		{
			name: "should name the profile's base_url without the flag", source: onesie.BaseURLFromOption,
			run:  profileOf(creds.Profile{Provider: "typesafe", APIKey: "SECRET-P", BaseURL: "https://p.example"}),
			want: "the profile's base_url",
		},
		{
			name: "should name the base URL variable", source: onesie.BaseURLFromEnv, run: bare,
			want: onesie.TypeSafe().EnvBaseURL(),
		},
		{
			name: "should name the profile's account_id when the profile has one", source: onesie.BaseURLFromAccount,
			run:  profileOf(creds.Profile{Provider: "cloudflare", APIKey: "SECRET-P", AccountID: testAccountID}),
			want: "the profile's account_id",
		},
		{
			name: "should name the account variable otherwise", source: onesie.BaseURLFromAccount, run: cloudflare,
			want: "CLOUDFLARE_ACCOUNT_ID",
		},
		{name: "should name the provider's default", source: onesie.BaseURLFromDefault, run: bare, want: "the provider's default"},
		{name: "should name a base URL of unknown source plainly", source: onesie.BaseURLSourceUnknown, run: bare, want: "the base URL"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := baseURLFrom(tc.source, tc.flag, tc.run); got != tc.want {
				t.Errorf("baseURLFrom = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBaseURLRefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "should name the source, the shown URL and the rule", raw: "https://",
			want: "--base-url https:// names no host",
		},
		{
			name: "should leave out a URL it cannot show", raw: "proxy.example",
			want: "--base-url is not an http or https URL",
		},
		{
			name: "should point the key elsewhere for a URL holding an @", raw: "https://u:SECRET-pw@proxy.example",
			want: "--base-url must not hold @, ? or #. Put the API key in a profile or the provider's key variable instead",
		},
		{
			name: "should point the key elsewhere for an @ after a query", raw: "https://proxy.example/?k=SECRET@x",
			want: "--base-url must not hold @, ? or #. Put the API key in a profile or the provider's key variable instead",
		},
		{
			name: "should add no key hint for a URL holding only a query", raw: "https://proxy.example/?x",
			want: "--base-url must not hold @, ? or #",
		},
		{
			name: "should name --allow-http for plain http to another host", raw: "http://proxy.example/v1",
			want: "--base-url http://proxy.example is plain http to a host other than this machine, so the API key " +
				"would cross the network unencrypted. Use https, or pass --allow-http to send it anyway",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := onesie.New(onesie.WithAPIKey("SECRET-K"), onesie.WithBaseURL(tc.raw))

			var refused *onesie.BaseURLError
			if !errors.As(err, &refused) {
				t.Fatalf("New = %v, want a *onesie.BaseURLError", err)
			}

			got := baseURLRefusal(refused, "--base-url")
			if got.Error() != tc.want {
				t.Errorf("Error = %q, want %q", got.Error(), tc.want)
			}

			if !errors.Is(got, onesie.ErrValidation) {
				t.Error("the refusal lost the library's error, so it would not exit 2")
			}
		})
	}
}
