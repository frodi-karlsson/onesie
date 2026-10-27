package cli

import (
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

			_, errOut, code := runInConfig(t, t.TempDir(), []string{"q", "--state", "x", "--retries", "0", "-r"}, "",
				map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-TEST", onesie.TypeSafe().EnvBaseURL(): srv.URL})

			if code != ExitUnavailable || errOut != prefix+tc.want+"\n" {
				t.Errorf("exit code = %d, stderr = %q, want %d and %q", code, errOut, ExitUnavailable, prefix+tc.want)
			}

			if strings.Contains(errOut, "Go ") || strings.Contains(errOut, "json:") {
				t.Errorf("stderr = %q, want no decoder internals", errOut)
			}
		})
	}
}
