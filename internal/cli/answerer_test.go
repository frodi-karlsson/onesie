package cli

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
)

func TestLiveAnswerer(t *testing.T) {
	t.Parallel()

	const prefix = "onesie: 200 response could not be used: "

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "should call an array no System One result", body: `[1]`, want: "the response is not a System One result, since it is an array"},
		{
			name: "should name answers that are not an object", body: `{"answers":[]}`,
			want: "the response is not a System One result, since its answers are not an object",
		},
		{
			name: "should name a model that is not a string", body: `{"model":5,"answers":{}}`,
			want: "the response is not a System One result, since its model is not a string",
		},
		{
			name: "should name a noul that is not a number", body: `{"answers":{"answer":{"type":"noul","noul":"yes"}}}`,
			want: "answer 'answer' has a noul that is not a number",
		},
		{
			name: "should name an answer that is not an object", body: `{"answers":{"answer":[1]}}`,
			want: "answer 'answer' is an array, not an object",
		},
		{
			name: "should name a type that is not a string", body: `{"answers":{"answer":{"type":5}}}`,
			want: "answer 'answer' has a type that is not a string",
		},
		{
			name: "should name a probability that is not a number",
			body: `{"answers":{"answer":{"type":"choice","choice":"a","confidence":1,"probabilities":{"a":"x"}}}}`,
			want: "answer 'answer' has a probability for 'a' that is not a number",
		},
		{
			name: "should name a box edge that is not a number",
			body: `{"answers":{"answer":{"type":"box","box":{"top":"a"}}}}`,
			want: "answer 'answer' has a box top edge that is not a number",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Errorf("writing the stub response: %v", err)
				}
			}))
			t.Cleanup(srv.Close)

			_, errOut, code := runInConfig(t, t.TempDir(), []string{"q", "--state", "x", "--retries", "0", "-o", "raw"}, "",
				map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-TEST", onesie.TypeSafe().EnvBaseURL(): srv.URL})

			if code != ExitUnavailable || errOut != prefix+tc.want+"\n" {
				t.Errorf("exit code = %d, stderr = %q, want %d and %q", code, errOut, ExitUnavailable, prefix+tc.want)
			}

			if strings.Contains(errOut, "Go ") || strings.Contains(errOut, "json:") {
				t.Errorf("stderr = %q, want no decoder internals", errOut)
			}
		})
	}

	t.Run("should advise from the provider of the client it holds", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadRequest)

			if _, err := io.WriteString(w, `{"detail":"max_tokens_exceeded"}`); err != nil {
				t.Errorf("writing the stub response: %v", err)
			}
		}))
		t.Cleanup(srv.Close)

		for _, tc := range []struct {
			provider onesie.Provider
			want     bool
		}{
			{provider: onesie.TypeSafe(), want: true},
			{provider: onesie.Cloudflare()},
		} {
			client, err := onesie.New(
				onesie.WithProvider(tc.provider), onesie.WithAPIKey("SECRET-K"), onesie.WithBaseURL(srv.URL))
			if err != nil {
				t.Fatal(err)
			}

			_, err = newLiveAnswerer(client).answer(t.Context(), recordKey{}, preparedFor(t, tc.provider, onesie.Request{
				State: "a ticket", Model: client.DefaultModel(),
				Questions: onesie.Questions{{ID: "u", Question: onesie.Noul{Instructions: "is it urgent"}}},
			}))
			if got := strings.Contains(text(err), "--long-context"); got != tc.want {
				t.Errorf("%s: error = %v, want a --long-context note %v", tc.provider.Name(), err, tc.want)
			}
		}
	})
	t.Run("should send the body it is handed", func(t *testing.T) {
		t.Parallel()

		bodies := make(chan []byte, 1)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			bodies <- body

			_, _ = io.WriteString(w, `{"model":"m","answers":{"u":{"type":"noul","noul":0.5}}}`)
		}))
		t.Cleanup(srv.Close)

		client, err := onesie.New(onesie.WithAPIKey("SECRET-K"), onesie.WithBaseURL(srv.URL))
		if err != nil {
			t.Fatal(err)
		}

		ready := preparedFor(t, onesie.TypeSafe(), onesie.Request{
			State: "a ticket", Model: "jev-1", Questions: onesie.Questions{{ID: "u", Question: onesie.Noul{Instructions: "is it urgent"}}},
		})

		if _, err := newLiveAnswerer(client).answer(t.Context(), recordKey{}, ready); err != nil {
			t.Fatal(err)
		}

		if sent := <-bodies; !bytes.Equal(sent, ready.body.Bytes()) {
			t.Errorf("sent %s, want the prepared body %s", sent, ready.body.Bytes())
		}
	})
}
