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
	"time"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

func TestStreamRaw(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"onesie-1.13.0","answers":{"a":{"type":"noul","noul":0.3}}}`

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
			stdin:    `{"state":"x","model":"onesie-1.13.0","questions":{"a":{"type":"noul"}}}` + "\n",
			response: answered,
			wantSent: []string{`{"state":"x","model":"onesie-1.13.0","questions":{"a":{"type":"noul"}}}`},
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
			stdin:    "{\"state\": \"x\",  \"model\": \"onesie-1.13.0\"}\n",
			response: answered,
			wantSent: []string{`{"state":"x","model":"onesie-1.13.0"}`},
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
			name:     "should write one line for a newline terminated response",
			args:     []string{"-i", "request"},
			stdin:    "{\"state\":\"a\"}\n{\"state\":\"b\"}\n",
			response: `{"answers":{}}` + "\n",
			wantSent: []string{`{"state":"a"}`, `{"state":"b"}`},
			wantOut:  []string{`{"answers":{}}`, `{"answers":{}}`},
			wantCode: ExitOK,
		},
		{
			name:     "should write one line for a pretty printed response",
			args:     []string{"-i", "request"},
			stdin:    "{\"state\":\"a\"}\n{\"state\":\"b\"}\n",
			response: "{\n  \"answers\": {\n    \"a\": 1\n  }\n}\n",
			wantSent: []string{`{"state":"a"}`, `{"state":"b"}`},
			wantOut:  []string{`{"answers":{"a":1}}`, `{"answers":{"a":1}}`},
			wantCode: ExitOK,
		},
		{
			name:     "should write one line for a response that is not JSON at all",
			args:     []string{"-i", "request"},
			stdin:    "{\"state\":\"a\"}\n",
			response: "not json\nat all\n",
			wantSent: []string{`{"state":"a"}`},
			wantOut:  []string{"not json at all"},
			wantCode: ExitOK,
		},
		{
			name:     "should send a body the way --print-request printed it",
			args:     []string{"-i", "request"},
			stdin:    `{"state":"a < b & c > d"}` + "\n",
			response: answered,
			wantSent: []string{`{"state":"a < b & c > d"}`},
			wantOut:  []string{answered},
			wantCode: ExitOK,
		},
		{
			name:     "should print a spaced body unchanged under --print-request",
			args:     []string{"-i", "request", "--print-request"},
			stdin:    "{\"state\": \"a < b\"}\n",
			response: answered,
			wantOut:  []string{`{"state": "a < b"}`},
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

	t.Run("should write an error record for a failed line", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			args      []string
			stdin     string
			status    int
			response  string
			wantSent  int
			wantLines int
			wantOut   []string
			wantErr   string
			wantCode  int
		}{
			{
				name:      "should write an error record for a line that is not JSON",
				args:      []string{"-i", "request"},
				stdin:     "{\"state\":\"one\"}\nnot json\n",
				response:  `{"answers":{}}`,
				wantSent:  1,
				wantLines: 2,
				wantOut:   []string{`{"answers":{}}`, `{"error":{"kind":"input"`, "line 2"},
				wantCode:  ExitRecords,
			},
			{
				name:      "should write an error record for a line that is not a JSON object",
				args:      []string{"-i", "request"},
				stdin:     "{\"state\":\"one\"}\n12\n[1,2,3]\n\"just a string\"\n",
				response:  `{"answers":{}}`,
				wantSent:  1,
				wantLines: 4,
				wantOut: []string{
					"line 2: a request body must be a JSON object, got number",
					"line 3: a request body must be a JSON object, got array",
					"line 4: a request body must be a JSON object, got string",
				},
				wantCode: ExitRecords,
			},
			{
				name:      "should send an object the API will reject rather than judging it",
				args:      []string{"-i", "request"},
				stdin:     "{\"hello\":1}\n",
				status:    http.StatusUnprocessableEntity,
				response:  `{"error":{"message":"questions is required"}}`,
				wantSent:  1,
				wantLines: 1,
				wantOut:   []string{`{"error":{"kind":"http"`, `"status":422`},
				wantCode:  ExitRecords,
			},
			{
				name:      "should write an error record when the server rejects a body",
				args:      []string{"-i", "request"},
				stdin:     "{\"state\":\"one\"}\n",
				status:    http.StatusUnprocessableEntity,
				response:  `{"error":{"message":"questions is required"}}`,
				wantSent:  1,
				wantLines: 1,
				wantOut:   []string{`{"error":{"kind":"http"`, `"status":422`},
				wantCode:  ExitRecords,
			},
			{
				name:      "should end the run at a 401 rather than failing every line",
				args:      []string{"-i", "request"},
				stdin:     "{\"state\":\"one\"}\n{\"state\":\"two\"}\n{\"state\":\"three\"}\n",
				status:    http.StatusUnauthorized,
				response:  `{"error":{"message":"invalid api key"}}`,
				wantSent:  1,
				wantLines: 1,
				wantOut:   []string{`{"error":{"kind":"http"`, `"status":401`},
				wantErr:   "onesie:",
				wantCode:  ExitAuth,
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
				// failure writes onesie's error record rather than a blank line, and a run that aborts
				// writes the prefix it completed rather than a record for every line left.
				if len(written) != tc.wantLines {
					t.Errorf("wrote %d lines, want %d\ngot:\n%s", len(written), tc.wantLines, out)
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

				if tc.wantErr == "" && errOut != "" {
					t.Errorf("stderr should be empty, got:\n%s", errOut)
				}

				if tc.wantErr != "" && !strings.Contains(errOut, tc.wantErr) {
					t.Errorf("stderr missing %q, got:\n%s", tc.wantErr, errOut)
				}
			})
		}
	})

	t.Run("should keep multiple requests in flight under -j", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			args    []string
			flight  int
			records int
		}{
			{
				name:    "should keep -j records in flight at once",
				args:    []string{"-i", "request", "-j", "4"},
				flight:  4,
				records: 8,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				// Every request blocks until as many as -j allows are in flight together, so a run
				// that serialises them never opens the gate and fails on the deadline rather than
				// passing by accident.
				var (
					mu      sync.Mutex
					arrived int
				)

				gate := make(chan struct{})

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					mu.Lock()
					arrived++

					if arrived == tc.flight {
						close(gate)
					}

					mu.Unlock()

					select {
					case <-gate:
					case <-time.After(3 * time.Second):
						t.Errorf("only %d requests were in flight, want %d", arrived, tc.flight)
					}

					w.Header().Set("Content-Type", "application/json")

					if _, err := io.WriteString(w, `{"answers":{}}`); err != nil {
						t.Errorf("writing the stub response: %v", err)
					}
				}))
				defer srv.Close()

				var stdin strings.Builder

				for i := range tc.records {
					fmt.Fprintf(&stdin, "{\"state\":%d}\n", i)
				}

				out, errOut, code := runAgainst(t, tc.args, stdin.String(), srv.URL)

				if code != ExitOK {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitOK, out, errOut)
				}

				if got := len(outputLines(out)); got != tc.records {
					t.Errorf("wrote %d lines, want %d\ngot:\n%s", got, tc.records, out)
				}
			})
		}
	})

	t.Run("should write records in completion order under --unordered", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name     string
			args     []string
			wantLast string
		}{
			{
				name:     "should write a record as it completes rather than in input order",
				args:     []string{"-i", "request", "--unordered", "-j", "3"},
				wantLast: `{"state":0}`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				// The first record is held until the two behind it have been answered, so input order
				// and completion order disagree and only a run that writes on completion can put the
				// first record last.
				var (
					mu   sync.Mutex
					done int
				)

				others := make(chan struct{})

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("reading the request body: %v", err)
					}

					if bytes.Contains(body, []byte(`{"state":0}`)) {
						select {
						case <-others:
						case <-time.After(3 * time.Second):
							t.Errorf("the later records never completed")
						}

						// The gate says the two responses were written, not that the client has read
						// them. A settle is the only thing that separates the two.
						time.Sleep(200 * time.Millisecond)
					}

					w.Header().Set("Content-Type", "application/json")

					if _, err := w.Write(body); err != nil {
						t.Errorf("writing the stub response: %v", err)
					}

					mu.Lock()
					done++

					if done == 2 {
						close(others)
					}

					mu.Unlock()
				}))
				defer srv.Close()

				out, errOut, code := runAgainst(
					t, tc.args, "{\"state\":0}\n{\"state\":1}\n{\"state\":2}\n", srv.URL)

				if code != ExitOK {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitOK, out, errOut)
				}

				written := outputLines(out)
				if len(written) != 3 {
					t.Fatalf("wrote %d lines, want 3\ngot:\n%s", len(written), out)
				}

				if written[len(written)-1] != tc.wantLast {
					t.Errorf("last line = %s, want %s\ngot:\n%s",
						written[len(written)-1], tc.wantLast, out)
				}
			})
		}
	})

	t.Run("should stop at the first failed record under --stop-on-error", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			args      []string
			stdin     string
			wantSent  int
			wantLines int
			wantCode  int
		}{
			{
				name:      "should end the run at the first failed record",
				args:      []string{"-i", "request", "--stop-on-error"},
				stdin:     "not json\n{\"state\":\"a\"}\n{\"state\":\"b\"}\n",
				wantSent:  0,
				wantLines: 1,
				wantCode:  ExitUsage,
			},
			{
				name:      "should read every record without it",
				args:      []string{"-i", "request"},
				stdin:     "not json\n{\"state\":\"a\"}\n{\"state\":\"b\"}\n",
				wantSent:  2,
				wantLines: 3,
				wantCode:  ExitRecords,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				sent, out, _, code := runRequestMode(t, tc.args, tc.stdin, 0, `{"answers":{}}`)

				if code != tc.wantCode {
					t.Fatalf("exit code = %d, want %d\nstdout:\n%s", code, tc.wantCode, out)
				}

				if len(sent) != tc.wantSent {
					t.Errorf("sent %d bodies, want %d: %q", len(sent), tc.wantSent, sent)
				}

				if got := len(outputLines(out)); got != tc.wantLines {
					t.Errorf("wrote %d lines, want %d\ngot:\n%s", got, tc.wantLines, out)
				}
			})
		}
	})
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
			wantErr: "onesie: -i request carries its own questions. Drop the question argument",
		},
		{
			name:    "should reject --ask with -i request",
			args:    []string{"-i", "request", "--ask", "urgent=is this urgent"},
			wantErr: "onesie: -i request carries its own questions. Drop --ask",
		},
		{
			name: "should reject -f with -i request before it is read",
			args: []string{"-i", "request", "-f", "nowhere.yaml"},
			wantErr: "onesie: -f does not apply to -i request, " +
				"which carries its own questions",
		},
		{
			name:    "should reject --replace with -i request",
			args:    []string{"-i", "request", "--replace"},
			wantErr: "onesie: --replace applies to -f, which -i request does not accept",
		},
		{
			name:    "should reject --pick with -i request",
			args:    []string{"-i", "request", "--pick", "a,b"},
			wantErr: "onesie: -i request carries its own questions. Drop --pick",
		},
		{
			name:    "should reject --rate with -i request",
			args:    []string{"-i", "request", "--rate", "low,high"},
			wantErr: "onesie: -i request carries its own questions. Drop --rate",
		},
		{
			name:    "should reject --desc with -i request",
			args:    []string{"-i", "request", "--desc", "a=first"},
			wantErr: "onesie: -i request carries its own questions. Drop --desc",
		},
		{
			name:    "should reject --sep with -i request",
			args:    []string{"-i", "request", "--sep", ";"},
			wantErr: "onesie: -i request carries its own questions. Drop --sep",
		},
		{
			name: "should reject --threshold with -i request",
			args: []string{"-i", "request", "--threshold", "0.5"},
			wantErr: "onesie: --threshold does not apply to -i request, " +
				"which carries no policy",
		},
		{
			name: "should reject --min-confidence with -i request",
			args: []string{"-i", "request", "--min-confidence", "0.5"},
			wantErr: "onesie: --min-confidence does not apply to -i request, " +
				"which carries no policy",
		},
		{
			name: "should reject --fallback with -i request",
			args: []string{"-i", "request", "--fallback", "maybe"},
			wantErr: "onesie: --fallback does not apply to -i request, " +
				"which carries no policy",
		},
		{
			name: "should reject --state with -i request",
			args: []string{"-i", "request", "--state", "x"},
			wantErr: "onesie: --state does not apply to -i request, " +
				"whose bodies carry their own state",
		},
		{
			name: "should reject --state-file with -i request",
			args: []string{"-i", "request", "--state-file", "x"},
			wantErr: "onesie: --state-file does not apply to -i request, " +
				"whose bodies carry their own state",
		},
		{
			name:    "should reject -o with -i request",
			args:    []string{"-i", "request", "-o", "json"},
			wantErr: "onesie: -o does not apply to -i request, which forwards raw responses",
		},
		{
			name:    "should reject -r with -i request",
			args:    []string{"-i", "request", "-r"},
			wantErr: "onesie: -r does not apply to -i request, which forwards raw responses",
		},
		{
			name:    "should reject -q with -i request",
			args:    []string{"-i", "request", "-q"},
			wantErr: "onesie: -q needs a policy to report, which -i request has none of",
		},
		{
			name: "should reject --assert with -i request",
			args: []string{"-i", "request", "--assert", "answer.value > 0.5"},
			wantErr: "onesie: --assert does not apply to -i request, " +
				"which forwards raw responses",
		},
		{
			name: "should reject an unparseable --assert with -i request",
			args: []string{"-i", "request", "--assert", "nonsense syntax here !!"},
			wantErr: "onesie: --assert does not apply to -i request, " +
				"which forwards raw responses",
		},
		{
			name: "should reject --usage with -i request",
			args: []string{"-i", "request", "--usage"},
			wantErr: "onesie: --usage does not apply to -i request, " +
				"whose response bodies already carry usage",
		},
		{
			name:    "should reject --merge with -i request",
			args:    []string{"-i", "request", "--merge"},
			wantErr: "onesie: --merge does not apply to -i request, which forwards raw responses",
		},
		{
			name: "should name --merge-key when that is the flag given",
			args: []string{"-i", "request", "--merge-key", "verdict"},
			wantErr: "onesie: --merge-key does not apply to -i request, " +
				"which forwards raw responses",
		},
		{
			name: "should reject -m with -i request",
			args: []string{"-i", "request", "-m", "onesie-1.13.0"},
			wantErr: "onesie: -m does not apply to -i request, " +
				"whose bodies carry their own model",
		},
		{
			name: "should reject --stop-on-assert with -i request",
			args: []string{"-i", "request", "--stop-on-assert"},
			wantErr: "onesie: --stop-on-assert does not apply to -i request, " +
				"whose bodies carry no assertion",
		},
		{
			name: "should reject --print-questions with -i request",
			args: []string{"-i", "request", "--print-questions"},
			wantErr: "onesie: --print-questions needs questions of its own, " +
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

	out, errOut, code := runAgainst(t, args, stdin, srv.URL)

	mu.Lock()
	defer mu.Unlock()

	return sent, out, errOut, code
}

func runAgainst(
	t *testing.T,
	args []string,
	stdin, baseURL string,
	extra ...RootOption,
) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		append([]RootOption{
			WithKeychain(noKeychain()),
			WithClientFactory(func(_ context.Context, opts ...jev.Option) (*jev.Client, error) {
				return jev.New(append([]jev.Option{
					jev.WithAPIKey("k"),
					jev.WithBaseURL(baseURL),
					jev.WithEnv(func(string) (string, bool) { return "", false }),
				}, opts...)...)
			}),
			WithStdin(strings.NewReader(stdin)),
			WithStdinTTY(false),
			WithStdoutTTY(false),
			WithLookupEnv(func(string) (string, bool) { return "", false }),
		}, extra...)...,
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
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
