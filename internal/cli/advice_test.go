package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
)

func TestExplain(t *testing.T) {
	t.Parallel()

	const (
		tooMany  = `{"detail":"Too many score levels. Must have at most 10 levels."}`
		tooLong  = `{"detail":"max_tokens_exceeded"}`
		invalid  = `{"detail":"Invalid request."}`
		longNote = ". Pass --long-context to raise the context limit, which is slower and less accurate"
		boxNote  = " A box question needs a model that serves it, such as -m jev-preview"
	)

	typesafe := requestFacts{provider: onesie.TypeSafe(), model: onesie.DefaultModel}

	with := func(change func(*requestFacts)) requestFacts {
		facts := typesafe
		change(&facts)

		return facts
	}

	tests := []struct {
		name   string
		status int
		body   string
		facts  requestFacts
		want   string
	}{
		{
			name: "should name the model sent and point at --list-models", status: http.StatusBadRequest,
			body:  `{"detail":"Unknown model: onesie-1.12"}`,
			facts: with(func(f *requestFacts) { f.model = "onesie-1.12" }),
			want:  "onesie: model 'onesie-1.12' not found. Try --list-models",
		},
		{
			name: "should say the local check passed when onesie checked the request", status: http.StatusBadRequest,
			body: tooMany, facts: with(func(f *requestFacts) { f.validated = true }),
			want: "onesie: 400 Too many score levels. Must have at most 10 levels. onesie's own check passed, " +
				"so its built in limits may be stale. Run onesie -V",
		},
		{
			name: "should keep the server text when onesie checked nothing", status: http.StatusBadRequest,
			body: tooMany, facts: typesafe,
			want: "onesie: 400 Too many score levels. Must have at most 10 levels.",
		},
		{
			name: "should point at --long-context where it would help", status: http.StatusBadRequest,
			body: tooLong, facts: typesafe, want: "onesie: 400 max_tokens_exceeded" + longNote,
		},
		{
			name: "should not point at --long-context when it is already on", status: http.StatusBadRequest,
			body: tooLong, facts: with(func(f *requestFacts) { f.longContext = true }),
			want: "onesie: 400 max_tokens_exceeded",
		},
		{
			name: "should name jev-preview when a box question is refused as invalid", status: http.StatusBadRequest,
			body: invalid, facts: with(func(f *requestFacts) { f.boxed = true }),
			want: "onesie: 400 Invalid request." + boxNote,
		},
		{
			name: "should not name jev-preview off TypeSafe", status: http.StatusBadRequest,
			body: invalid, facts: with(func(f *requestFacts) { f.boxed, f.provider = true, onesie.OpenRouter() }),
			want: "onesie: 400 Invalid request.",
		},
		{
			name: "should point at the account when it is out of credits", status: http.StatusPaymentRequired,
			body: `{"detail":"Insufficient credits"}`, facts: typesafe,
			want: "onesie: 402 Insufficient credits. Add credits to the account this key belongs to",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := explain(listingRefusal(t, tc.status, tc.body), tc.facts)
			if got == nil || got.Error() != tc.want {
				t.Errorf("explain = %v, want %q", got, tc.want)
			}
		})
	}

	t.Run("should leave no error as none", func(t *testing.T) {
		t.Parallel()

		if got := explain(nil, typesafe); got != nil {
			t.Errorf("explain(nil) = %v, want nil", got)
		}
	})
}

func listingRefusal(t *testing.T, status int, body string) error {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client, err := onesie.New(onesie.WithAPIKey("SECRET-TEST"), onesie.WithBaseURL(srv.URL))
	if err != nil {
		t.Fatalf("onesie.New: %v", err)
	}

	_, err = client.ListModels(t.Context())
	if err == nil {
		t.Fatalf("ListModels answered %d without an error", status)
	}

	return err
}

func TestAdvise(t *testing.T) {
	t.Parallel()

	t.Run("should name the unknown model and point at --list-models", func(t *testing.T) {
		t.Parallel()

		const body = `{"model":"onesie-1.12",` +
			`"questions":{"urgent":{"type":"noul","instructions":"is this urgent"}}}`

		tests := []adviceCase{
			{
				name:     "should name the model -m sent and point at --list-models",
				args:     []string{"is this urgent", "-m", "onesie-1.12"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: `{"detail":"Unknown model: onesie-1.12"}`,
				wantCode: ExitUsage,
				wantSent: `"model":"onesie-1.12"`,
				wantErr:  "onesie: model 'onesie-1.12' not found. Try --list-models\n",
				wantOut: []string{`"error":{"kind":"http","status":400,` +
					`"message":"model 'onesie-1.12' not found. Try --list-models"}`},
			},
			{
				name:     "should name a clef model workers ai does not serve",
				args:     []string{"is this urgent", "-m", "clef-nope"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: `{"errors":[{"message":"AiError: No such model: No such model @cf/cloudflare/clef-nope or task (x)","code":5007}],"success":false}`,
				wantCode: ExitUsage,
				wantSent: `"model":"clef-nope"`,
				wantErr:  "onesie: model 'clef-nope' not found. Try --list-models\n",
			},
			{
				name:     "should name the model the environment supplied",
				args:     []string{"is this urgent"},
				stdin:    "a ticket",
				env:      map[string]string{onesie.EnvDefaultModel: "onesie-9.9"},
				status:   http.StatusBadRequest,
				response: `{"detail":"Unknown model: onesie-9.9"}`,
				wantCode: ExitUsage,
				wantSent: `"model":"onesie-9.9"`,
				wantErr:  "onesie: model 'onesie-9.9' not found. Try --list-models\n",
			},
			{
				name:     "should name the model a question file body carried",
				args:     []string{"-f", "body.json"},
				stdin:    "a ticket",
				files:    map[string]string{"body.json": body},
				status:   http.StatusBadRequest,
				response: `{"detail":"Unknown model: onesie-1.12"}`,
				wantCode: ExitUsage,
				wantSent: `"model":"onesie-1.12"`,
				wantErr:  "onesie: model 'onesie-1.12' not found. Try --list-models\n",
			},
			{
				name:     "should name the model a request body carried",
				args:     []string{"-i", "request"},
				stdin:    `{"state":"a ticket",` + body[1:] + "\n",
				status:   http.StatusBadRequest,
				response: `{"detail":"Unknown model: onesie-1.12"}`,
				wantCode: ExitRecords,
				wantSent: `"model":"onesie-1.12"`,
				wantErr:  "",
				wantOut: []string{`{"error":{"kind":"http","status":400,` +
					`"message":"model 'onesie-1.12' not found. Try --list-models"}}`},
			},
			{
				name:     "should carry the remedy into a streaming error record",
				args:     []string{"is this urgent", "-i", "lines", "-m", "onesie-1.12"},
				stdin:    "a ticket\n",
				status:   http.StatusBadRequest,
				response: `{"detail":"Unknown model: onesie-1.12"}`,
				wantCode: ExitRecords,
				wantSent: `"model":"onesie-1.12"`,
				wantErr:  "",
				wantOut: []string{`"error":{"kind":"http","status":400,` +
					`"message":"model 'onesie-1.12' not found. Try --list-models"}`},
			},
			{
				name:   "should name the model on the 404 Berget sends for an unknown model",
				args:   []string{"is this urgent", "-m", "jev-latest"},
				stdin:  "a ticket",
				status: http.StatusNotFound,
				response: `{"error":{"message":"Model not found: jev-latest","type":"invalid_request_error",` +
					`"code":"model_not_found"}}`,
				wantCode: ExitUsage,
				wantSent: `"model":"jev-latest"`,
				wantErr:  "onesie: model 'jev-latest' not found. Try --list-models\n",
			},
			{
				name:   "should name the model on the 422 Levanto sends for an unknown model",
				args:   []string{"is this urgent", "-m", "jev-latest"},
				stdin:  "a ticket",
				status: http.StatusUnprocessableEntity,
				response: `{"detail":[{"loc":["body","model"],"msg":"unknown model 'jev-latest'; GET /v1/models ` +
					`lists the accepted names (levanto-sage-v1.4, sage-latest)","type":"value_error"}]}`,
				wantCode: ExitUsage,
				wantSent: `"model":"jev-latest"`,
				wantErr:  "onesie: model 'jev-latest' not found. Try --list-models\n",
			},
			{
				name:     "should leave a 404 about anything else unchanged",
				args:     []string{"is this urgent", "-m", "jev-latest"},
				stdin:    "a ticket",
				status:   http.StatusNotFound,
				response: `{"error":{"message":"no such route","type":"invalid_request_error","code":null}}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: 404 no such route\n",
			},
			{
				name:     "should leave a 400 about anything else unchanged",
				args:     []string{"is this urgent", "-m", "onesie-1.12"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: `{"detail":"state must be an object"}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 state must be an object\n",
			},
			{
				name:     "should keep the server text for a listing, which sends no model",
				args:     []string{"--list-models"},
				env:      map[string]string{onesie.EnvDefaultModel: "onesie-9.9"},
				status:   http.StatusBadRequest,
				response: `{"detail":"Unknown model: onesie-9.9"}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 Unknown model: onesie-9.9\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				checkAdvice(t, tc)
			})
		}
	})

	t.Run("should say the local check passed when the server rejects a count", func(t *testing.T) {
		t.Parallel()

		// The wording the live API answered with on 2026-09-22, for a score carrying eleven levels.
		const tooManyLevels = `{"detail":"Too many score levels. Must have at most 10 levels."}`

		const rejected = "onesie: 400 Too many score levels. Must have at most 10 levels."

		const note = " onesie's own check passed, so its built in limits may be stale. Run onesie -V"

		const file = "team:\n  ask: which team\n  pick:\n    billing: payments\n    technical: bugs\n"

		const frozen = `{"state":"a ticket","model":"onesie-1.12",` +
			`"questions":{"severity":{"type":"score","instructions":"how bad",` +
			`"criteria":["calm","annoyed","angry"]}}}`

		tests := []adviceCase{
			{
				name:     "should say the local check passed when the server rejects a level count",
				args:     []string{"--ask", "severity=how bad is it", "--rate", "calm,annoyed,angry"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: tooManyLevels,
				wantCode: ExitUsage,
				wantErr:  rejected + note + "\n",
			},
			{
				name:     "should say the same for an option count from a question file",
				args:     []string{"-f", "questions.yaml"},
				stdin:    "a ticket",
				files:    map[string]string{"questions.yaml": file},
				status:   http.StatusBadRequest,
				response: `{"detail":"Too many choice options. Must have at most 255 options."}`,
				wantCode: ExitUsage,
				wantErr: "onesie: 400 Too many choice options. Must have at most 255 options." +
					note + "\n",
			},
			{
				name:     "should say the same for a 422, which the API reference documents",
				args:     []string{"--ask", "severity=how bad is it", "--rate", "calm,annoyed,angry"},
				stdin:    "a ticket",
				status:   http.StatusUnprocessableEntity,
				response: tooManyLevels,
				wantCode: ExitUsage,
				wantErr: "onesie: 422 Too many score levels. Must have at most 10 levels." +
					note + "\n",
			},
			{
				name:     "should leave the same rejection alone under -i request, which never checked",
				args:     []string{"-i", "request"},
				stdin:    frozen + "\n",
				status:   http.StatusBadRequest,
				response: tooManyLevels,
				wantCode: ExitRecords,
				wantErr:  "",
				wantOut: []string{`{"error":{"kind":"http","status":400,` +
					`"message":"` + strings.TrimPrefix(rejected, "onesie: ") + `"}}`},
			},
			{
				name:     "should say the same for an image the server refuses",
				args:     []string{"--image", "photo=square.png", "is there a red square in `photo`"},
				files:    map[string]string{"square.png": string(squarePNG(t))},
				status:   http.StatusBadRequest,
				response: `{"detail":"image side must be at most 1568 pixels"}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 image side must be at most 1568 pixels." + note + "\n",
			},
			{
				name: "should carry the note into a streaming error record",
				args: []string{
					"--ask", "severity=how bad is it", "--rate", "calm,annoyed,angry", "-i", "lines",
				},
				stdin:    "a ticket\n",
				status:   http.StatusBadRequest,
				response: tooManyLevels,
				wantCode: ExitRecords,
				wantOut: []string{`"error":{"kind":"http","status":400,` +
					`"message":"` + strings.TrimPrefix(rejected, "onesie: ") + note + `"}`},
			},
			{
				name:     "should leave a 400 reading Invalid request untouched",
				args:     []string{"--ask", "severity=how bad is it", "--rate", "calm,annoyed,angry"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: `{"detail":"Invalid request."}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 Invalid request.\n",
			},
			{
				name:     "should leave a 400 about a level that is not a count untouched",
				args:     []string{"--ask", "severity=how bad is it", "--rate", "calm,annoyed,angry"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: `{"detail":"Score levels must be strings."}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 Score levels must be strings.\n",
			},
			{
				name:     "should leave a 400 bounding something other than a count untouched",
				args:     []string{"--ask", "severity=how bad is it", "--rate", "calm,annoyed,angry"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: `{"detail":"Request too large. Must have at most 32000 tokens."}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 Request too large. Must have at most 32000 tokens.\n",
			},
			{
				name:     "should leave a 400 naming another field untouched",
				args:     []string{"--ask", "severity=how bad is it", "--rate", "calm,annoyed,angry"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: `{"detail":[{"loc":["body","state"],"msg":"field required"}]}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 state: field required\n",
			},
			{
				name: "should keep the model remedy when the rejection is an unknown model",
				args: []string{
					"--ask", "severity=how bad is it", "--rate", "calm,annoyed,angry",
					"-m", "onesie-1.12",
				},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: `{"detail":"Unknown model: onesie-1.12"}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: model 'onesie-1.12' not found. Try --list-models\n",
			},
			{
				// A listing sends no questions, so no local bound was checked and the note would name
				// a check that never ran. This pins the one remaining call site.
				name:     "should leave a listing untouched, since it sends no questions",
				args:     []string{"--list-models"},
				status:   http.StatusBadRequest,
				response: tooManyLevels,
				wantCode: ExitUsage,
				wantErr:  rejected + "\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				checkAdvice(t, tc)
			})
		}
	})

	t.Run("should summarise an error body that is not JSON", func(t *testing.T) {
		t.Parallel()

		const (
			page  = "<!doctype html>\n<html><head><title>405 Not Allowed</title></head><body>nginx</body></html>\n"
			check = ". Check that the base URL points at the System One API"
		)

		tests := []adviceCase{
			{
				name:        "should give the status and leave an html page out",
				args:        []string{"is this urgent"},
				stdin:       "a ticket",
				status:      http.StatusMethodNotAllowed,
				response:    page,
				contentType: "text/html",
				wantCode:    ExitUsage,
				wantErr:     "onesie: 405 Method Not Allowed, with a response body that is not JSON" + check + "\n",
				wantOut: []string{`"message":"405 Method Not Allowed, with a response body that is not JSON` +
					check + `"`},
			},
			{
				name:        "should keep the first line of a plain text body",
				args:        []string{"is this urgent"},
				stdin:       "a ticket",
				status:      http.StatusNotFound,
				response:    "no such route\nsecond line\n",
				contentType: "text/plain",
				wantCode:    ExitUsage,
				wantErr:     "onesie: 404 Not Found, with a response body that is not JSON: no such route" + check + "\n",
			},
			{
				name:        "should summarise a replayed body's error line too",
				args:        []string{"-i", "request"},
				stdin:       `{"state":"s","model":"m","questions":{"q":{"type":"noul","instructions":"q"}}}` + "\n",
				status:      http.StatusMethodNotAllowed,
				response:    page,
				contentType: "text/html",
				wantCode:    ExitRecords,
				wantOut: []string{`"message":"405 Method Not Allowed, with a response body that is not JSON` +
					check + `"`},
			},
			{
				name:        "should summarise an outage page with no note about the base URL",
				args:        []string{"is this urgent", "--retries", "0"},
				stdin:       "a ticket",
				status:      http.StatusServiceUnavailable,
				response:    page,
				contentType: "text/html",
				wantCode:    ExitUnavailable,
				wantErr:     "onesie: 503 Service Unavailable, with a response body that is not JSON\n",
			},
			{
				name:        "should summarise a refused key's text body beside the key hint and no note about the base URL",
				args:        []string{"is this urgent"},
				stdin:       "a ticket",
				status:      http.StatusUnauthorized,
				response:    "bad key\n",
				contentType: "text/plain",
				wantCode:    ExitAuth,
				wantErr: "onesie: 401 Unauthorized, with a response body that is not JSON: bad key. " +
					"The key came from TYPESAFE_API_KEY\n",
			},
			{
				name:        "should drop a text body that only repeats the status text",
				args:        []string{"is this urgent"},
				stdin:       "a ticket",
				status:      http.StatusUnauthorized,
				response:    "Unauthorized\n",
				contentType: "text/plain",
				wantCode:    ExitAuth,
				wantErr: "onesie: 401 Unauthorized, with a response body that is not JSON. " +
					"The key came from TYPESAFE_API_KEY\n",
			},
			{
				name:        "should drop a text body that only repeats the status line",
				args:        []string{"is this urgent", "--retries", "0"},
				stdin:       "a ticket",
				status:      http.StatusServiceUnavailable,
				response:    "503 service unavailable\n",
				contentType: "text/plain",
				wantCode:    ExitUnavailable,
				wantErr:     "onesie: 503 Service Unavailable, with a response body that is not JSON\n",
			},
			{
				name:     "should not repeat a refused key message that already says to check the key",
				args:     []string{"is this urgent"},
				stdin:    "a ticket",
				status:   http.StatusUnauthorized,
				response: `{"error":{"message":"Cannot authenticate with the server. Please check your API key and try again."}}`,
				wantCode: ExitAuth,
				wantErr: "onesie: 401 Cannot authenticate with the server. Please check your API key and try again. " +
					"The key came from TYPESAFE_API_KEY\n",
			},
			{
				name:     "should show a JSON string body's first line and not call it not JSON",
				args:     []string{"is this urgent", "--retries", "0"},
				stdin:    "a ticket",
				status:   http.StatusBadGateway,
				response: `"Bad Gateway\nupstream"`,
				wantCode: ExitUnavailable,
				wantErr:  "onesie: 502 Bad Gateway\n",
			},
			{
				name:        "should summarise the body for --list-models",
				args:        []string{"--list-models"},
				status:      http.StatusMethodNotAllowed,
				response:    page,
				contentType: "text/html",
				wantCode:    ExitUsage,
				wantErr:     "onesie: 405 Method Not Allowed, with a response body that is not JSON" + check + "\n",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				checkAdvice(t, tc)
			})
		}
	})

	t.Run("should point at the flag or model a refusal needs", func(t *testing.T) {
		t.Parallel()

		const (
			invalid  = `{"detail":"Invalid request."}`
			tooLong  = `{"detail":"max_tokens_exceeded"}`
			boxNote  = " A box question needs a model that serves it, such as -m jev-preview"
			longNote = ". Pass --long-context to raise the context limit, which is slower and less accurate"
		)

		square := map[string]string{"square.png": string(squarePNG(t))}
		boxed := []string{"--image", "photo=square.png", "--ask", "mug=the mug in `photo`", "--box"}
		boxBody := `{"state":{"photo":"r"},"model":"jev-1.13.0","questions":{"mug":{"type":"box","instructions":"x"}},` +
			`"assets":{"r":{"type":"image","format":"png","data_base64":"AQ=="}}}`

		tests := []adviceCase{
			{
				name:     "should name jev-preview when a box question is refused as invalid",
				args:     boxed,
				files:    square,
				status:   http.StatusBadRequest,
				response: invalid,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 Invalid request." + boxNote + "\n",
			},
			{
				name:     "should not name jev-preview on openrouter",
				args:     boxed,
				files:    square,
				env:      map[string]string{"ONESIE_PROVIDER": "openrouter"},
				status:   http.StatusBadRequest,
				response: invalid,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 Invalid request.\n",
			},
			{
				name:     "should name jev-preview for a replayed box body too",
				args:     []string{"-i", "request"},
				stdin:    boxBody + "\n",
				status:   http.StatusBadRequest,
				response: invalid,
				wantCode: ExitRecords,
				wantOut:  []string{`"message":"400 Invalid request.` + boxNote + `"`},
			},
			{
				name:     "should name --long-context when the state is over the context limit",
				args:     []string{"is this urgent"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: tooLong,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 max_tokens_exceeded" + longNote + "\n",
			},
			{
				name:     "should leave max_tokens_exceeded alone on cloudflare, which takes no long context",
				args:     []string{"is this urgent"},
				stdin:    "a ticket",
				env:      map[string]string{"ONESIE_PROVIDER": "cloudflare"},
				status:   http.StatusBadRequest,
				response: tooLong,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 max_tokens_exceeded\n",
			},
			{
				name:     "should leave the limit alone when --long-context is already on",
				args:     []string{"is this urgent", "--long-context"},
				stdin:    "a ticket",
				status:   http.StatusBadRequest,
				response: tooLong,
				wantCode: ExitUsage,
				wantErr:  "onesie: 400 max_tokens_exceeded\n",
			},
			{
				name:     "should name --long-context for a replayed body too",
				args:     []string{"-i", "request"},
				stdin:    `{"state":"s","model":"m","questions":{"q":{"type":"noul","instructions":"q"}}}` + "\n",
				status:   http.StatusBadRequest,
				response: tooLong,
				wantCode: ExitRecords,
				wantOut:  []string{`"message":"400 max_tokens_exceeded` + longNote + `"`},
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				checkAdvice(t, tc)
			})
		}
	})
}

func checkAdvice(t *testing.T, tc adviceCase) {
	t.Helper()

	sent, out, errOut, code := runAdvised(t, tc)

	if code != tc.wantCode {
		t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			code, tc.wantCode, out, errOut)
	}

	if errOut != tc.wantErr {
		t.Errorf("stderr = %q, want %q", errOut, tc.wantErr)
	}

	for _, want := range tc.wantOut {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}

	if tc.wantSent == "" {
		return
	}

	if len(sent) != 1 {
		t.Fatalf("made %d requests, want 1", len(sent))
	}

	if !strings.Contains(sent[0], tc.wantSent) {
		t.Errorf("request body = %s, want it to contain %s", sent[0], tc.wantSent)
	}
}

func runAdvised(t *testing.T, tc adviceCase) ([]string, string, string, int) {
	t.Helper()

	var (
		mu   sync.Mutex
		sent []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}

		mu.Lock()
		sent = append(sent, string(body))
		mu.Unlock()

		contentType := tc.contentType
		if contentType == "" {
			contentType = "application/json"
		}

		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(tc.status)

		if _, err := io.WriteString(w, tc.response); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))
	defer srv.Close()

	lookup := func(name string) (string, bool) {
		value, ok := tc.env[name]

		return value, ok
	}

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithKeychain(noKeychain()),
		WithClientFactory(func(_ context.Context, opts ...onesie.Option) (*onesie.Client, error) {
			return onesie.New(append([]onesie.Option{
				onesie.WithAPIKey("k"),
				onesie.WithBaseURL(srv.URL),
				onesie.WithEnv(lookup),
			}, opts...)...)
		}),
		WithStdin(strings.NewReader(tc.stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookup),
		WithReadFile(func(name string) ([]byte, error) {
			content, ok := tc.files[name]
			if !ok {
				return nil, fmt.Errorf("no file named %s", name)
			}

			return []byte(content), nil
		}),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(tc.args)

	code := Execute(t.Context(), root)

	mu.Lock()
	defer mu.Unlock()

	return sent, out.String(), errOut.String(), code
}

type adviceCase struct {
	name        string
	args        []string
	stdin       string
	env         map[string]string
	files       map[string]string
	status      int
	response    string
	contentType string

	wantCode int
	wantSent string
	wantErr  string
	wantOut  []string
}

func TestWithKeyHint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		profiles string
		args     []string
		env      map[string]string
		status   int
		wantCode int
		wantErr  string
		notErr   string
	}{
		{
			name: "should name profile update --key when the server refuses a profile's key",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
				`"base_url":"{url}"}}}`,
			args:     []string{"is this urgent", "--state", "x", "-r"},
			status:   http.StatusUnauthorized,
			wantCode: ExitAuth,
			wantErr:  "onesie: 401 the key was refused. Replace the key with onesie profile update 'work' --key",
		},
		{
			name:     "should name the key variable when the server refuses a key from the environment",
			args:     []string{"is this urgent", "--state", "x", "-r", "--provider", "openrouter", "--base-url", "{url}"},
			env:      map[string]string{"OPENROUTER_API_KEY": "SECRET-OR"},
			status:   http.StatusForbidden,
			wantCode: ExitAuth,
			wantErr:  "onesie: 403 the key was refused. The key came from OPENROUTER_API_KEY",
		},
		{
			name: "should name the key variable when calibrate is refused",
			args: []string{
				"calibrate", "--ask", "u=is this urgent", "-i", "jsonl", "--map", ".body", "--label", "u=.u",
				"--base-url", "{url}",
			},
			env:      map[string]string{onesie.EnvAPIKey: "SECRET-TS"},
			status:   http.StatusUnauthorized,
			wantCode: ExitAuth,
			wantErr:  "The key came from TYPESAFE_API_KEY",
		},
		{
			name: "should leave a 402 without the key hint",
			profiles: `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-WORK",` +
				`"base_url":"{url}"}}}`,
			args:     []string{"is this urgent", "--state", "x", "-r"},
			status:   http.StatusPaymentRequired,
			wantCode: ExitAuth,
			notErr:   "profile update",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := recordingServer(t, tc.status)
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

			if !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
			}

			if tc.notErr != "" && strings.Contains(errOut, tc.notErr) {
				t.Errorf("stderr = %q, want no %q", errOut, tc.notErr)
			}
		})
	}
}
