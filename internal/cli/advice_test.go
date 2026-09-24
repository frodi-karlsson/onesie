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

	"github.com/frodi-karlsson/onesie/internal/jev"
)

func TestNewRootCmdUnknownModel(t *testing.T) {
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
				`"message":"onesie: model 'onesie-1.12' not found. Try --list-models"}`},
		},
		{
			name:     "should name the model the environment supplied",
			args:     []string{"is this urgent"},
			stdin:    "a ticket",
			env:      map[string]string{jev.EnvDefaultModel: "onesie-9.9"},
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
				`"message":"onesie: model 'onesie-1.12' not found. Try --list-models"}}`},
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
				`"message":"onesie: model 'onesie-1.12' not found. Try --list-models"}`},
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
			env:      map[string]string{jev.EnvDefaultModel: "onesie-9.9"},
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
}

func TestNewRootCmdRejectedCount(t *testing.T) {
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
				`"message":"` + rejected + `"}}`},
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
				`"message":"` + rejected + note + `"}`},
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
}

type adviceCase struct {
	name     string
	args     []string
	stdin    string
	env      map[string]string
	files    map[string]string
	status   int
	response string

	wantCode int
	wantSent string
	wantErr  string
	wantOut  []string
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

		w.Header().Set("Content-Type", "application/json")
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
		WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
			return jev.New(append([]jev.Option{
				jev.WithAPIKey("k"),
				jev.WithBaseURL(srv.URL),
				jev.WithEnv(lookup),
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
