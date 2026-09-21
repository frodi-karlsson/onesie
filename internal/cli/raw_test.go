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

func TestStreamRaw(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"jev-1.13.0","answers":{"a":{"type":"noul","noul":0.3}}}`

	tests := []struct {
		name     string
		args     []string
		stdin    string
		status   int
		response string
		wantSent []string
		wantOut  []string
		wantCode int
	}{
		{
			name:     "should forward a body byte identical and print the response",
			args:     []string{"-i", "request"},
			stdin:    `{"state":"x","model":"jev-1.13.0","questions":{"a":{"type":"noul"}}}` + "\n",
			response: answered,
			wantSent: []string{`{"state":"x","model":"jev-1.13.0","questions":{"a":{"type":"noul"}}}`},
			wantOut:  []string{answered},
			wantCode: ExitOK,
		},
		{
			name:     "should write one response line per input line",
			args:     []string{"-i", "request"},
			stdin:    "{\"state\":\"one\"}\n{\"state\":\"two\"}\n",
			response: answered,
			wantSent: []string{`{"state":"one"}`, `{"state":"two"}`},
			wantOut:  []string{answered, answered},
			wantCode: ExitOK,
		},
		{
			name:     "should compact a body that was written with whitespace",
			args:     []string{"-i", "request"},
			stdin:    "{\"state\": \"x\",  \"model\": \"jev-1.13.0\"}\n",
			response: answered,
			wantSent: []string{`{"state":"x","model":"jev-1.13.0"}`},
			wantOut:  []string{answered},
			wantCode: ExitOK,
		},
		{
			name:     "should skip a blank line under --skip-blank",
			args:     []string{"-i", "request", "--skip-blank"},
			stdin:    "{\"state\":\"one\"}\n\n{\"state\":\"two\"}\n",
			response: answered,
			wantSent: []string{`{"state":"one"}`, `{"state":"two"}`},
			wantOut:  []string{answered, answered},
			wantCode: ExitOK,
		},
		{
			name:     "should write nothing for an empty stream",
			args:     []string{"-i", "request"},
			stdin:    "",
			response: answered,
			wantCode: ExitOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sent, out, errOut, code := runRequestMode(t, tc.args, tc.stdin, tc.status, tc.response)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out, errOut)
			}

			if diff := compareLines(sent, tc.wantSent); diff != "" {
				t.Errorf("sent bodies: %s", diff)
			}

			if diff := compareLines(outputLines(out), tc.wantOut); diff != "" {
				t.Errorf("stdout: %s", diff)
			}

			if errOut != "" {
				t.Errorf("stderr should be empty, got:\n%s", errOut)
			}
		})
	}
}

func TestStreamRawFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		stdin    string
		status   int
		response string
		wantSent int
		wantOut  []string
		wantCode int
	}{
		{
			name:     "should write an error record for a line that is not JSON",
			args:     []string{"-i", "request"},
			stdin:    "{\"state\":\"one\"}\nnot json\n",
			response: `{"answers":{}}`,
			wantSent: 1,
			wantOut:  []string{`{"answers":{}}`, `{"error":{"kind":"input"`, "line 2"},
			wantCode: ExitRecords,
		},
		{
			name:     "should write an error record when the server rejects a body",
			args:     []string{"-i", "request"},
			stdin:    "{\"state\":\"one\"}\n",
			status:   http.StatusUnprocessableEntity,
			response: `{"error":{"message":"questions is required"}}`,
			wantSent: 1,
			wantOut:  []string{`{"error":{"kind":"http"`, `"status":422`},
			wantCode: ExitRecords,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sent, out, errOut, code := runRequestMode(t, tc.args, tc.stdin, tc.status, tc.response)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out, errOut)
			}

			if len(sent) != tc.wantSent {
				t.Errorf("sent %d bodies, want %d: %q", len(sent), tc.wantSent, sent)
			}

			written := outputLines(out)

			// One line per input line, which is what a consumer reading line by line needs. A
			// failure writes jev's error record rather than a blank line.
			if len(written) != len(outputLines(tc.stdin)) {
				t.Errorf("wrote %d lines, want %d\ngot:\n%s",
					len(written), len(outputLines(tc.stdin)), out)
			}

			for i, line := range written {
				if line == "" {
					t.Errorf("line %d is blank\ngot:\n%s", i+1, out)
				}
			}

			for _, want := range tc.wantOut {
				if !strings.Contains(out, want) {
					t.Errorf("stdout missing %q\ngot:\n%s", want, out)
				}
			}

			if errOut != "" {
				t.Errorf("stderr should be empty, got:\n%s", errOut)
			}
		})
	}
}

func TestRequestIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  []string
		stdin string
	}{
		{
			name:  "should forward printed bodies unchanged",
			args:  []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request"},
			stdin: "{\"a\":1}\n{\"b\":2}\n",
		},
		{
			name:  "should keep a large integer through the round trip",
			args:  []string{"--ask", "urgent=is this urgent", "-i", "jsonl", "--print-request"},
			stdin: "{\"ticket_id\":12345678901234567890}\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			printed, errOut, code := runOfflineStdin(t, tc.args, tc.stdin)
			if code != ExitOK {
				t.Fatalf("printing exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			// No client factory and no key, so a case that exits ok reached no network at all,
			// which is the identity claim section 10 makes.
			replayed, errOut, code := runOfflineStdin(
				t, []string{"-i", "request", "--print-request"}, printed)
			if code != ExitOK {
				t.Fatalf("replay exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
			}

			if replayed != printed {
				t.Errorf("replayed:\n%s\nprinted:\n%s", replayed, printed)
			}

			if errOut != "" {
				t.Errorf("stderr should be empty, got:\n%s", errOut)
			}

			// The bodies a real run sends are the printed ones, byte for byte.
			sent, out, errOut, code := runRequestMode(
				t, []string{"-i", "request"}, printed, 0, `{"answers":{}}`)
			if code != ExitOK {
				t.Fatalf("sending exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, ExitOK, out, errOut)
			}

			if diff := compareLines(sent, outputLines(printed)); diff != "" {
				t.Errorf("sent bodies: %s", diff)
			}
		})
	}
}

func TestNewRootCmdRequestFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "should reject a positional question with -i request",
			args:    []string{"-i", "request", "is this urgent"},
			wantErr: "jev: -i request carries its own questions. Drop the question argument",
		},
		{
			name:    "should reject --ask with -i request",
			args:    []string{"-i", "request", "--ask", "urgent=is this urgent"},
			wantErr: "jev: -i request carries its own questions. Drop --ask",
		},
		{
			name: "should reject -f with -i request before it is read",
			args: []string{"-i", "request", "-f", "nowhere.yaml"},
			wantErr: "jev: -f does not apply to -i request, " +
				"which carries its own questions",
		},
		{
			name:    "should reject --replace with -i request",
			args:    []string{"-i", "request", "--replace"},
			wantErr: "jev: --replace applies to -f, which -i request does not accept",
		},
		{
			name: "should reject --state with -i request",
			args: []string{"-i", "request", "--state", "x"},
			wantErr: "jev: --state does not apply to -i request, " +
				"whose bodies carry their own state",
		},
		{
			name: "should reject --state-file with -i request",
			args: []string{"-i", "request", "--state-file", "x"},
			wantErr: "jev: --state-file does not apply to -i request, " +
				"whose bodies carry their own state",
		},
		{
			name:    "should reject -o with -i request",
			args:    []string{"-i", "request", "-o", "json"},
			wantErr: "jev: -o does not apply to -i request, which forwards raw responses",
		},
		{
			name:    "should reject -q with -i request",
			args:    []string{"-i", "request", "-q"},
			wantErr: "jev: -q needs a policy to report, which -i request has none of",
		},
		{
			name: "should reject --usage with -i request",
			args: []string{"-i", "request", "--usage"},
			wantErr: "jev: --usage does not apply to -i request, " +
				"whose response bodies already carry usage",
		},
		{
			name:    "should reject --merge with -i request",
			args:    []string{"-i", "request", "--merge"},
			wantErr: "jev: --merge does not apply to -i request, which forwards raw responses",
		},
		{
			name: "should reject -m with -i request",
			args: []string{"-i", "request", "-m", "jev-1.13.0"},
			wantErr: "jev: -m does not apply to -i request, " +
				"whose bodies carry their own model",
		},
		{
			name: "should reject --print-questions with -i request",
			args: []string{"-i", "request", "--print-questions"},
			wantErr: "jev: --print-questions needs questions of its own, " +
				"which -i request does not build",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runOfflineStdin(t, tc.args, `{"state":"x"}`+"\n")

			if code != ExitUsage {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, ExitUsage, out, errOut)
			}

			if strings.TrimSpace(errOut) != tc.wantErr {
				t.Errorf("stderr = %q, want %q", strings.TrimSpace(errOut), tc.wantErr)
			}

			if out != "" {
				t.Errorf("stdout should be empty, got:\n%s", out)
			}
		})
	}
}

func TestNewRootCmdWarnsOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "should print a flag warning once for a mode that builds a plan",
			args: []string{"is this urgent", "-j", "4"},
			want: "warning: -j 4 ignored. -i text reads one record",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, out, errOut, code := runRequestMode(
				t, tc.args, "hello\n", 0,
				`{"model":"m","answers":{"answer":{"type":"noul","noul":0.9}}}`)

			if code != ExitOK {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, ExitOK, out, errOut)
			}

			// Once, not twice. CheckFlags has two callers now, and a run that reached both would
			// report the same warning to the same stream twice.
			if got := strings.Count(errOut, tc.want); got != 1 {
				t.Errorf("warning printed %d times, want 1\nstderr:\n%s", got, errOut)
			}
		})
	}
}

// runRequestMode runs the given arguments against a stub that answers every body with the same
// response, and returns the request bodies it received in arrival order.
func runRequestMode(
	t *testing.T,
	args []string,
	stdin string,
	status int,
	response string,
) ([]string, string, string, int) {
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

		if status != 0 {
			w.WriteHeader(status)
		}

		if _, err := io.WriteString(w, response); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))
	defer srv.Close()

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithClientFactory(func(context.Context) (*jev.Client, error) {
			return jev.New(
				jev.WithAPIKey("k"),
				jev.WithBaseURL(srv.URL),
				jev.WithEnv(func(string) (string, bool) { return "", false }),
			)
		}),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(func(string) (string, bool) { return "", false }),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	mu.Lock()
	defer mu.Unlock()

	return sent, out.String(), errOut.String(), code
}

func compareLines(got, want []string) string {
	if len(got) != len(want) {
		return fmt.Sprintf("got %d lines %q, want %d %q", len(got), got, len(want), want)
	}

	for i := range got {
		if got[i] != want[i] {
			return fmt.Sprintf("line %d = %s, want %s", i+1, got[i], want[i])
		}
	}

	return ""
}
