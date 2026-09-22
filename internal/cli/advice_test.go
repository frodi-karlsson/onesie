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

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func TestNewRootCmdUnknownModel(t *testing.T) {
	t.Parallel()

	const body = `{"model":"jev-1.12",` +
		`"questions":{"urgent":{"type":"noul","instructions":"is this urgent"}}}`

	tests := []adviceCase{
		{
			name:     "should name the model -m sent and point at --list-models",
			args:     []string{"is this urgent", "-m", "jev-1.12"},
			stdin:    "a ticket",
			status:   http.StatusBadRequest,
			response: `{"detail":"Unknown model: jev-1.12"}`,
			wantCode: ExitUsage,
			wantSent: `"model":"jev-1.12"`,
			wantErr:  "jev: model 'jev-1.12' not found. Try --list-models\n",
			wantOut: []string{`"error":{"kind":"http","status":400,` +
				`"message":"jev: model 'jev-1.12' not found. Try --list-models"}`},
		},
		{
			name:     "should name the model the environment supplied",
			args:     []string{"is this urgent"},
			stdin:    "a ticket",
			env:      map[string]string{jev.EnvDefaultModel: "jev-9.9"},
			status:   http.StatusBadRequest,
			response: `{"detail":"Unknown model: jev-9.9"}`,
			wantCode: ExitUsage,
			wantSent: `"model":"jev-9.9"`,
			wantErr:  "jev: model 'jev-9.9' not found. Try --list-models\n",
		},
		{
			name:     "should name the model a question file body carried",
			args:     []string{"-f", "body.json"},
			stdin:    "a ticket",
			files:    map[string]string{"body.json": body},
			status:   http.StatusBadRequest,
			response: `{"detail":"Unknown model: jev-1.12"}`,
			wantCode: ExitUsage,
			wantSent: `"model":"jev-1.12"`,
			wantErr:  "jev: model 'jev-1.12' not found. Try --list-models\n",
		},
		{
			name:     "should name the model a request body carried",
			args:     []string{"-i", "request"},
			stdin:    `{"state":"a ticket",` + body[1:] + "\n",
			status:   http.StatusBadRequest,
			response: `{"detail":"Unknown model: jev-1.12"}`,
			wantCode: ExitRecords,
			wantSent: `"model":"jev-1.12"`,
			wantErr:  "",
			wantOut: []string{`{"error":{"kind":"http","status":400,` +
				`"message":"jev: model 'jev-1.12' not found. Try --list-models"}}`},
		},
		{
			name:     "should carry the remedy into a streaming error record",
			args:     []string{"is this urgent", "-i", "lines", "-m", "jev-1.12"},
			stdin:    "a ticket\n",
			status:   http.StatusBadRequest,
			response: `{"detail":"Unknown model: jev-1.12"}`,
			wantCode: ExitRecords,
			wantSent: `"model":"jev-1.12"`,
			wantErr:  "",
			wantOut: []string{`"error":{"kind":"http","status":400,` +
				`"message":"jev: model 'jev-1.12' not found. Try --list-models"}`},
		},
		{
			name:     "should leave a 400 about anything else unchanged",
			args:     []string{"is this urgent", "-m", "jev-1.12"},
			stdin:    "a ticket",
			status:   http.StatusBadRequest,
			response: `{"detail":"state must be an object"}`,
			wantCode: ExitUsage,
			wantErr:  "jev: 400 state must be an object\n",
		},
		{
			name:     "should keep the server text for a listing, which sends no model",
			args:     []string{"--list-models"},
			env:      map[string]string{jev.EnvDefaultModel: "jev-9.9"},
			status:   http.StatusBadRequest,
			response: `{"detail":"Unknown model: jev-9.9"}`,
			wantCode: ExitUsage,
			wantErr:  "jev: 400 Unknown model: jev-9.9\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkAdvice(t, tc)
		})
	}
}

func TestNewRootCmdRejectedCriteria(t *testing.T) {
	t.Parallel()

	const tooMany = `{"detail":[{"loc":["body","questions","team","criteria"],` +
		`"msg":"too many items"}]}`

	const note = ". jev's own check passed, so its built in limits may be stale. Run jev -V"

	tests := []adviceCase{
		{
			name:     "should say the local check passed when the server rejects a count",
			args:     []string{"--ask", "team=which team", "--pick", "billing,technical"},
			stdin:    "a ticket",
			status:   http.StatusUnprocessableEntity,
			response: tooMany,
			wantCode: ExitUsage,
			wantErr:  "jev: 422 questions.team.criteria: too many items" + note + "\n",
		},
		{
			name:   "should say the same when the path names an index under criteria",
			args:   []string{"--ask", "team=which team", "--pick", "billing,technical"},
			stdin:  "a ticket",
			status: http.StatusUnprocessableEntity,
			response: `{"detail":[{"loc":["body","questions","team","criteria",0],` +
				`"msg":"too long"}]}`,
			wantCode: ExitUsage,
			wantErr:  "jev: 422 questions.team.criteria.0: too long" + note + "\n",
		},
		{
			name:     "should carry the note into a streaming error record",
			args:     []string{"--ask", "team=which team", "--pick", "billing,technical", "-i", "lines"},
			stdin:    "a ticket\n",
			status:   http.StatusUnprocessableEntity,
			response: tooMany,
			wantCode: ExitRecords,
			wantOut: []string{`"error":{"kind":"http","status":422,` +
				`"message":"jev: 422 questions.team.criteria: too many items` + note + `"}`},
		},
		{
			name:     "should leave a 422 about another field unchanged",
			args:     []string{"is this urgent"},
			stdin:    "a ticket",
			status:   http.StatusUnprocessableEntity,
			response: `{"detail":[{"loc":["body","state"],"msg":"field required"}]}`,
			wantCode: ExitUsage,
			wantErr:  "jev: 422 state: field required\n",
		},
		{
			// A listing sends no criteria, so this response is contrived. It is here to pin the
			// listing call site, which would otherwise be the one path whose errors go unadvised.
			name:     "should advise a listing as well",
			args:     []string{"--list-models"},
			status:   http.StatusUnprocessableEntity,
			response: tooMany,
			wantCode: ExitUsage,
			wantErr:  "jev: 422 questions.team.criteria: too many items" + note + "\n",
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
