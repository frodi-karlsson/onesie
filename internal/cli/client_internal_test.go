package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
)

func TestClientBuilder(t *testing.T) {
	t.Parallel()

	refused := "onesie: " + refusedBaseURL

	tests := []struct {
		name     string
		profiles string
		args     []string
		env      map[string]string
	}{
		{
			name: "should refuse a --base-url whose password holds a question mark",
			args: []string{"is this urgent", "--state", "hello", "-o", "json", "--base-url", "https://u:1?SECRET-x@proxy.example"},
			env:  map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
		},
		{
			name: "should refuse a --base-url with a query before any request",
			args: []string{"--list-models", "--base-url", "{url}/?SECRET-x"},
			env:  map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
		},
		{
			name: "should refuse a base url variable with a fragment before any request",
			args: []string{"is this urgent", "--state", "hello", "-o", "json"},
			env:  map[string]string{onesie.EnvAPIKey: "SECRET-ENV", onesie.EnvBaseURL: "{url}/#SECRET-x"},
		},
		{
			name: "should refuse a profile's base url with a query before profile test sends a request",
			profiles: `{"default":"ts","profiles":{"ts":{"provider":"typesafe","api_key":"SECRET-TS",` +
				`"base_url":"{url}/?SECRET-x"}}}`,
			args: []string{"profile", "test"},
		},
	}

	for _, unclear := range unclearBaseURLs() {
		// The host is swapped for the local server's, so a request that goes out is counted.
		tests = append(tests, struct {
			name     string
			profiles string
			args     []string
			env      map[string]string
		}{
			name: "should refuse a --base-url " + unclear.name + " before any request",
			args: []string{"--list-models", "--base-url", strings.ReplaceAll(unclear.url, "proxy.example", "{host}")},
			env:  map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
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

			out, errOut, code := runInConfig(t, dir, args, "", env)

			assertNoSecret(t, out, errOut)

			if code != ExitUsage {
				t.Errorf("exit code = %d, want %d", code, ExitUsage)
			}

			if !strings.Contains(out+errOut, refused) {
				t.Errorf("stdout and stderr carry no %q", refused)
			}

			if got := requests.Load(); got != 0 {
				t.Errorf("the server saw %d requests, want none", got)
			}
		})
	}

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
				want: "onesie: base URL https:// names no host. Write it as https://HOST\n",
			},
			{
				name: "should say a --base-url with no scheme is not http or https",
				args: []string{"--base-url", "proxy.example"},
				want: "onesie: base URL proxy.example is not an http or https URL\n",
			},
			{
				name: "should word profile create the same way",
				args: []string{"profile", "create", "w", "--provider", "typesafe", "--base-url", "https://"},
				want: "onesie: base URL https:// names no host. Write it as https://HOST\n",
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
					map[string]string{onesie.EnvAPIKey: "SECRET-TEST"})
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
			args: asking(), env: map[string]string{onesie.EnvAPIKey: "SECRET-ENV", onesie.EnvBaseURL: "{other}"},
			wantAuth: "Bearer SECRET-WORK", wantModel: onesie.DefaultModel,
		},
		{
			name: "should send the key the profile's keychain item holds",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","store":"keychain",` +
				`"account":"work@abc","base_url":"{url}"}}}`,
			args: asking(), items: map[string]string{"work@abc": "SECRET-KC"},
			wantAuth: "Bearer SECRET-KC", wantModel: onesie.DefaultModel,
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
			env:      map[string]string{onesie.EnvDefaultModel: "env-model"},
			wantAuth: "Bearer SECRET-WORK", wantModel: "jev-1.13.0",
		},
		{
			name: "should take TYPESAFE_DEFAULT_MODEL when the profile names no model", profiles: fileWork,
			args: asking(), env: map[string]string{onesie.EnvDefaultModel: "env-model"},
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
			env:      map[string]string{onesie.EnvBaseURL: "{other}"},
			wantAuth: "Bearer SECRET-WORK", wantModel: onesie.DefaultModel,
		},
		{
			name: "should keep --base-url ahead of the profile's base_url",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
				`"base_url":"{other}"}}}`,
			args:     asking("--base-url", "{url}"),
			wantAuth: "Bearer SECRET-WORK", wantModel: onesie.DefaultModel,
		},
		{
			name: "should name the profile's base_url when it ends in /v1",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
				`"base_url":"{url}/v1"}}}`,
			args:     asking(),
			wantErr:  []string{"warning: the profile's base_url ends in /v1 and onesie adds the API path itself"},
			wantAuth: "Bearer SECRET-WORK", wantModel: onesie.DefaultModel,
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
				WithKeychain(workingKeychain(tc.items)))

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
			env:      map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
			wantCode: ExitUsage,
			wantErr: []string{"onesie: --base-url http://proxy.example is plain http to a host other than this " +
				"machine, so the API key would cross the network unencrypted. Use https, or pass --allow-http"},
		},
		{
			name:     "should refuse a plain http TYPESAFE_BASE_URL to another host",
			args:     asking(),
			env:      map[string]string{onesie.EnvAPIKey: "SECRET-ENV", onesie.EnvBaseURL: "http://proxy.example"},
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
			env:      map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
			wantCode: ExitUsage,
			wantErr:  []string{"is plain http", "--allow-http"},
		},
		{
			name:     "should send to a plain http server on this machine without --allow-http",
			args:     []string{"--list-models", "--base-url", "{url}"},
			env:      map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
			wantCode: ExitOK,
			notErr:   "plain http",
		},
		{
			name:     "should send to this machine under --allow-http without a warning",
			args:     []string{"--list-models", "--base-url", "{url}", "--allow-http"},
			env:      map[string]string{onesie.EnvAPIKey: "SECRET-ENV"},
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

func TestPlainRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "should pass https to any host", baseURL: "https://proxy.example"},
		{name: "should pass plain http to localhost", baseURL: "http://localhost:8080"},
		{name: "should pass plain http to 127.0.0.1", baseURL: "http://127.0.0.1:8080"},
		{name: "should pass plain http to any address in 127.0.0.0/8", baseURL: "http://127.1.2.3"},
		{name: "should pass plain http to ::1", baseURL: "http://[::1]:8080"},
		{name: "should pass plain http to localhost in another case", baseURL: "http://LocalHost:8080"},
		{name: "should pass plain http to an IPv4-mapped loopback address", baseURL: "http://[::ffff:127.0.0.1]:8080"},
		{name: "should flag plain http to localhost with a trailing dot", baseURL: "http://localhost.", want: true},
		{name: "should flag plain http to an IPv4-mapped private address", baseURL: "http://[::ffff:10.0.0.1]", want: true},
		{name: "should flag plain http to a named host", baseURL: "http://proxy.example", want: true},
		{name: "should flag plain http to a private address", baseURL: "http://10.0.0.1", want: true},
		{name: "should flag plain http to a name under localhost", baseURL: "http://api.localhost", want: true},
		{name: "should flag plain http to an address that only starts like loopback", baseURL: "http://128.0.0.1", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := plainRemote(tc.baseURL); got != tc.want {
				t.Errorf("plainRemote(%q) = %v, want %v", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestPlainHTTPCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		baseURL     string
		allowHTTP   bool
		wantWarning string
		wantRefusal string
	}{
		{
			name: "should refuse plain http to another host, naming the source and --allow-http", baseURL: "http://proxy.example",
			wantRefusal: "onesie: TYPESAFE_BASE_URL http://proxy.example is plain http to a host other than this " +
				"machine, so the API key would cross the network unencrypted. Use https, or pass --allow-http to send it anyway",
		},
		{
			name: "should warn rather than refuse under --allow-http", baseURL: "http://proxy.example", allowHTTP: true,
			wantWarning: "warning: http://proxy.example is plain http, so the API key crosses the network unencrypted",
		},
		{name: "should pass https to another host", baseURL: "https://proxy.example"},
		{name: "should pass plain http to this machine", baseURL: "http://127.0.0.1:8080"},
		{name: "should pass plain http to this machine under --allow-http", baseURL: "http://[::1]:8080", allowHTTP: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			warning, refused := plainHTTPCheck(tc.baseURL, onesie.EnvBaseURL, tc.allowHTTP)

			if warning != tc.wantWarning {
				t.Errorf("warning = %q, want %q", warning, tc.wantWarning)
			}

			switch {
			case tc.wantRefusal == "" && refused != nil:
				t.Errorf("refused with %v, want no refusal", refused)
			case tc.wantRefusal != "" && (refused == nil || refused.Error() != tc.wantRefusal):
				t.Errorf("refusal = %v, want %q", refused, tc.wantRefusal)
			}
		})
	}
}

func TestEndsInVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "should not warn for the typesafe default", baseURL: onesie.TypeSafe().BaseURL},
		{name: "should not warn for the openrouter default", baseURL: onesie.OpenRouter().BaseURL},
		{name: "should not warn for the berget default", baseURL: onesie.Berget().BaseURL},
		{name: "should not warn for the levanto default", baseURL: onesie.Levanto().BaseURL},
		{name: "should not warn for a /v1 that is not the last segment", baseURL: "https://proxy.example/v1/proxy"},
		{name: "should not warn for a segment that only ends in v1", baseURL: "https://proxy.example/apiv1"},
		{name: "should warn for a path ending in /v1", baseURL: "https://proxy.example/v1", want: true},
		{name: "should warn for a path ending in /v1/", baseURL: "https://proxy.example/v1/", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := endsInVersion(tc.baseURL); got != tc.want {
				t.Errorf("endsInVersion(%q) = %v, want %v", tc.baseURL, got, tc.want)
			}
		})
	}
}

func TestValidBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{name: "should take an https URL", url: "https://proxy.example/v1"},
		{name: "should say a base URL with a scheme and no host names no host", url: "https://", want: "onesie: base URL https:// names no host. Write it as https://HOST"},
		{name: "should echo a base URL with a path and no host as given", url: "https:///v1", want: "onesie: base URL https:///v1 names no host. Write it as https://HOST"},
		{name: "should say a base URL that does not parse does not parse", url: "https://x.invalid/%zz", want: `onesie: base URL https://x.invalid/%zz does not parse: invalid URL escape "%zz"`},
		{name: "should say a base URL with no scheme is not http or https", url: "proxy.example", want: "onesie: base URL proxy.example is not an http or https URL"},
		{name: "should refuse a base URL with a query and no host as unclear", url: "https://?x", want: "onesie: " + refusedBaseURL},
		{name: "should refuse a base URL with userinfo and no scheme as unclear", url: "u:SECRET-pw@proxy.example", want: "onesie: " + refusedBaseURL},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validBaseURL(tc.url)

			got := ""
			if err != nil {
				got = err.Error()
			}

			if got != tc.want {
				t.Errorf("validBaseURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}
