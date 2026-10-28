package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
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
			wantSent: []string{`{"model":"jev-latest","state":"one"}`, `{"model":"jev-latest","state":"two"}`},
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
			wantSent: []string{`{"model":"jev-latest","state":"one"}`, `{"model":"jev-latest","state":"two"}`},
			wantOut:  []string{answered, answered},
			wantCode: ExitOK,
		},
		{
			name:     "should write one line for a newline terminated response",
			args:     []string{"-i", "request"},
			stdin:    "{\"state\":\"a\"}\n{\"state\":\"b\"}\n",
			response: `{"answers":{}}` + "\n",
			wantSent: []string{`{"model":"jev-latest","state":"a"}`, `{"model":"jev-latest","state":"b"}`},
			wantOut:  []string{`{"answers":{}}`, `{"answers":{}}`},
			wantCode: ExitOK,
		},
		{
			name:     "should write one line for a pretty printed response",
			args:     []string{"-i", "request"},
			stdin:    "{\"state\":\"a\"}\n{\"state\":\"b\"}\n",
			response: "{\n  \"answers\": {\n    \"a\": {\"type\": \"noul\", \"noul\": 1}\n  }\n}\n",
			wantSent: []string{`{"model":"jev-latest","state":"a"}`, `{"model":"jev-latest","state":"b"}`},
			wantOut:  []string{`{"answers":{"a":{"type":"noul","noul":1}}}`, `{"answers":{"a":{"type":"noul","noul":1}}}`},
			wantCode: ExitOK,
		},
		{
			name:     "should send a body the way --print-request printed it",
			args:     []string{"-i", "request"},
			stdin:    `{"model":"jev-latest","state":"a < b & c > d"}` + "\n",
			response: answered,
			wantSent: []string{`{"model":"jev-latest","state":"a < b & c > d"}`},
			wantOut:  []string{answered},
			wantCode: ExitOK,
		},
		{
			name:     "should print a spaced body with the model filled in and its values as written under --print-request",
			args:     []string{"-i", "request", "--print-request"},
			stdin:    "{\"state\": \"a < b\", \"n\": 1.50}\n",
			response: answered,
			wantOut:  []string{`{"model":"jev-latest","state":"a < b","n":1.50}`},
			wantCode: ExitOK,
		},
		{
			name:     "should print long_context as true under --long-context with --print-request",
			args:     []string{"-i", "request", "--print-request", "--long-context"},
			stdin:    "{\"model\":\"jev-own\",\"long_context\":false,\"state\":\"a\"}\n",
			response: answered,
			wantOut:  []string{`{"model":"jev-own","long_context":true,"state":"a"}`},
			wantCode: ExitOK,
		},
		{
			name:     "should refuse a long_context that is not a boolean under --long-context with --print-request",
			args:     []string{"-i", "request", "--print-request", "--long-context"},
			stdin:    "{\"model\":\"jev-own\",\"long_context\":\"yes\",\"state\":\"a\"}\n",
			response: answered,
			wantOut: []string{
				`{"error":{"kind":"input","status":null,"message":"long_context must be true or false"}}`,
			},
			wantCode: ExitRecords,
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
				name:      "should refuse a body with an unknown question type before sending it",
				args:      []string{"-i", "request"},
				stdin:     `{"state":"x","questions":{"a":{"type":"tarot","instructions":"q"}}}` + "\n",
				response:  `{"answers":{}}`,
				wantSent:  0,
				wantLines: 1,
				wantOut: []string{
					`{"error":{"kind":"input"`,
					"question 'a' has the type 'tarot', which is not noul, choice, score or box",
				},
				wantCode: ExitRecords,
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

	t.Run("should refuse a 200 that is no System One result or does not fit its request", func(t *testing.T) {
		t.Parallel()

		const asked = `{"state":"x","model":"m","questions":{"u":{"type":"noul","instructions":"q"}}}`

		tests := []struct {
			name     string
			stdin    string
			response string
			want     string
		}{
			{name: "should refuse a body that is not JSON", stdin: asked, response: "garbage", want: "the response is not JSON"},
			{
				name: "should refuse a result with no answers object", stdin: `{"state":"x"}`, response: `{"x":1}`,
				want: "the response is not a System One result, since it has no answers",
			},
			{
				name: "should refuse an answer that carries only its type", stdin: asked,
				response: `{"answers":{"u":{"type":"noul"}}}`, want: "answer 'u' has no noul, or it is null",
			},
			{
				name: "should refuse an answer the request did not get", stdin: asked,
				response: `{"answers":{"v":{"type":"noul","noul":0.5}}}`, want: "question 'u' has no answer",
			},
			{
				name: "should refuse an answer outside its range", stdin: asked,
				response: `{"answers":{"u":{"type":"noul","noul":1.5}}}`,
				want:     "question 'u' answered 1.5, which lies outside [0,1]",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				_, out, errOut, code := runRequestMode(t, []string{"-i", "request", "--stats"}, tc.stdin+"\n", 0, tc.response)
				if code != ExitRecords {
					t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, ExitRecords, out, errOut)
				}

				lines := outputLines(out)
				if len(lines) != 1 || !strings.HasPrefix(lines[0], `{"error":{"kind":"response","status":200,`) ||
					!strings.Contains(lines[0], "response could not be used: "+tc.want) {
					t.Errorf("stdout = %q, want one response error line naming %q", out, tc.want)
				}

				if !strings.Contains(errOut, "1 failed") {
					t.Errorf("stderr = %q, want the record counted as failed", errOut)
				}
			})
		}
	})

	t.Run("should count the tokens of a 200 it could not use", func(t *testing.T) {
		t.Parallel()

		_, out, errOut, code := runRequestMode(t, []string{"-i", "request", "--stats"},
			`{"state":"x","model":"m","questions":{"u":{"type":"noul","instructions":"q"}}}`+"\n", 0,
			`{"answers":{"u":{"type":"noul","noul":2}},"usage":{"input_tokens":10,"output_tokens":2}}`)
		if code != ExitRecords || !strings.Contains(errOut, "10 in / 2 out tokens") {
			t.Errorf("exit code = %d, stderr = %q, want %d and the tokens counted\nstdout:\n%s", code, errOut, ExitRecords, out)
		}
	})

	t.Run("should count the tokens of a 200 whose answers cannot be read", func(t *testing.T) {
		t.Parallel()

		_, out, errOut, code := runRequestMode(t, []string{"-i", "request", "--stats"},
			`{"state":"x","model":"m","questions":{"u":{"type":"noul","instructions":"q"}}}`+"\n", 0,
			`{"model":"m","answers":[],"usage":{"input_tokens":3,"output_tokens":1}}`)
		if code != ExitRecords || !strings.Contains(errOut, "3 in / 1 out tokens") {
			t.Errorf("exit code = %d, stderr = %q, want %d and the tokens counted\nstdout:\n%s", code, errOut, ExitRecords, out)
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

					<-gate

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
				wantLast: `{"model":"m","answers":{},"state":0}`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				// The first record is held until the two behind it have been written, so input order
				// and completion order disagree and only a run that writes on completion can put the
				// first record last.
				stdout := &signalledLines{written: make(chan struct{}, 3)}

				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("reading the request body: %v", err)
					}

					if bytes.Contains(body, []byte(`"state":0}`)) {
						for range 2 {
							<-stdout.written
						}
					}

					w.Header().Set("Content-Type", "application/json")

					if _, err := w.Write(body); err != nil {
						t.Errorf("writing the stub response: %v", err)
					}
				}))
				defer srv.Close()

				errOut, code := runAgainstTo(
					t, stdout, tc.args, "{\"model\":\"m\",\"answers\":{},\"state\":0}\n{\"model\":\"m\",\"answers\":{},\"state\":1}\n"+
						"{\"model\":\"m\",\"answers\":{},\"state\":2}\n", srv.URL)
				out := stdout.String()

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
				wantCode:  ExitRecords,
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

	t.Run("should forward printed bodies unchanged", func(t *testing.T) {
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
				// which is what makes --print-request an identity here.
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
					t, []string{"-i", "request"}, printed, 0, `{"answers":{"urgent":{"type":"noul","noul":0.5}}}`)
				if code != ExitOK {
					t.Fatalf("sending exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
						code, ExitOK, out, errOut)
				}

				if diff := compareLines(sent, outputLines(printed)); diff != "" {
					t.Errorf("sent bodies: %s", diff)
				}
			})
		}
	})

	t.Run("should fill in the resolved model for a body that names none", func(t *testing.T) {
		t.Parallel()

		const bare = `{"state":"x","questions":{"q":{"type":"noul","instructions":"q"}}}`

		profiles := `{"default":"work","profiles":{"work":{"provider":"typesafe","api_key":"SECRET-W",` +
			`"base_url":"{url}","model":"jev-profile"}}}`

		tests := []struct {
			name     string
			profiles string
			env      map[string]string
			args     []string
			stdin    string
			want     string
		}{
			{
				name:     "should send the profile's model",
				profiles: profiles,
				args:     []string{"-i", "request"},
				stdin:    bare,
				want:     `{"model":"jev-profile","state":"x","questions":{"q":{"type":"noul","instructions":"q"}}}`,
			},
			{
				name:     "should print the profile's model under --print-request",
				profiles: profiles,
				args:     []string{"-i", "request", "--print-request"},
				stdin:    bare,
				want:     `{"model":"jev-profile","state":"x","questions":{"q":{"type":"noul","instructions":"q"}}}`,
			},
			{
				name:  "should send the default model variable with no profile",
				env:   map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV", onesie.TypeSafe().EnvBaseURL(): "{url}", onesie.TypeSafe().EnvDefaultModel(): "jev-env"},
				args:  []string{"-i", "request"},
				stdin: bare,
				want:  `{"model":"jev-env","state":"x","questions":{"q":{"type":"noul","instructions":"q"}}}`,
			},
			{
				name:     "should keep the model a body names",
				profiles: profiles,
				args:     []string{"-i", "request", "--print-request"},
				stdin:    `{"model":"jev-own","state":"x"}`,
				want:     `{"model":"jev-own","state":"x"}`,
			},
			{
				name:     "should fill an empty object",
				profiles: profiles,
				args:     []string{"-i", "request", "--print-request"},
				stdin:    `{}`,
				want:     `{"model":"jev-profile"}`,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

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

					if _, err := io.WriteString(w, `{"model":"jev-1.14.0","answers":{"q":{"type":"noul","noul":0.5}}}`); err != nil {
						t.Errorf("writing the stub response: %v", err)
					}
				}))
				t.Cleanup(srv.Close)

				fill := strings.NewReplacer("{url}", srv.URL).Replace

				env := map[string]string{}
				for name, value := range tc.env {
					env[name] = fill(value)
				}

				dir := t.TempDir()
				if tc.profiles != "" {
					dir = configWith(t, fill(tc.profiles))
				}

				out, errOut, code := runInConfig(t, dir, tc.args, tc.stdin+"\n", env)
				if code != ExitOK {
					t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, ExitOK, errOut)
				}

				mu.Lock()
				defer mu.Unlock()

				got := strings.TrimSuffix(out, "\n")
				if len(sent) > 0 {
					got = sent[0]
				}

				if got != tc.want {
					t.Errorf("body = %s, want %s", got, tc.want)
				}
			})
		}
	})

	t.Run("should check long context against the run's resolved model", func(t *testing.T) {
		t.Parallel()

		body := `{"state":"s","questions":{"answer":{"type":"noul","instructions":"q"}}}` + "\n"

		tests := []struct {
			name     string
			profiles string
			env      map[string]string
			wantCode int
			wantErr  string
			wantSent int
		}{
			{
				name: "should refuse a cloudflare profile's model before any request",
				profiles: `{"default":"cf","profiles":{"cf":{"provider":"cloudflare","api_key":"SECRET-CF",` +
					`"base_url":"{url}","model":"clef-1"}}}`,
				wantCode: ExitUsage,
				wantErr:  "onesie: the cloudflare provider takes no long context",
			},
			{
				name:     "should send under the default model variable on typesafe, which takes long context",
				env:      map[string]string{onesie.TypeSafe().EnvAPIKey(): "SECRET-ENV", onesie.TypeSafe().EnvBaseURL(): "{url}", onesie.TypeSafe().EnvDefaultModel(): "jev-env"},
				wantCode: ExitOK,
				wantSent: 1,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				srv, auths := recordingServer(t, 0)
				fill := strings.NewReplacer("{url}", srv.URL).Replace

				env := map[string]string{}
				for name, value := range tc.env {
					env[name] = fill(value)
				}

				dir := t.TempDir()
				if tc.profiles != "" {
					dir = configWith(t, fill(tc.profiles))
				}

				out, errOut, code := runInConfig(t, dir, []string{"-i", "request", "--long-context"}, body, env)

				assertNoSecret(t, out, errOut)

				if code != tc.wantCode {
					t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut)
				}

				if !strings.Contains(errOut, tc.wantErr) {
					t.Errorf("stderr = %q, want it to contain %q", errOut, tc.wantErr)
				}

				if got := len(auths()); got != tc.wantSent {
					t.Errorf("the server took %d requests, want %d", got, tc.wantSent)
				}
			})
		}
	})

	t.Run("should keep the stdout, stderr, exit code and --out file of each pinned run", func(t *testing.T) {
		t.Parallel()

		checkPinnedRuns(t, pinnedRawRuns())
	})
}

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

	var out bytes.Buffer

	errOut, code := runAgainstTo(t, &out, args, stdin, baseURL, extra...)

	return out.String(), errOut, code
}

func runAgainstTo(
	t *testing.T,
	out io.Writer,
	args []string,
	stdin, baseURL string,
	extra ...RootOption,
) (string, int) {
	t.Helper()

	var errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		append([]RootOption{
			withKeychain(noKeychain()),
			WithClientFactory(func(_ context.Context, opts ...onesie.Option) (*onesie.Client, error) {
				return onesie.New(append([]onesie.Option{
					onesie.WithAPIKey("k"),
					onesie.WithBaseURL(baseURL),
					onesie.WithEnv(func(string) (string, bool) { return "", false }),
					onesie.WithClock(instantClock{now: time.Now}),
				}, opts...)...)
			}),
			WithStdin(strings.NewReader(stdin)),
			WithStdinTTY(false),
			WithStdoutTTY(false),
			// An empty config dir, so the developer's own profiles never choose the model a body takes.
			WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
		}, extra...)...,
	)

	root.Command.SetOut(out)
	root.Command.SetErr(&errOut)
	root.Command.SetArgs(args)

	code := root.Execute(t.Context())

	return errOut.String(), code
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

func TestRequests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flags runFlags
		want  bool
	}{
		{name: "should send requests on a run that asks", want: true},
		{name: "should send none under --print-questions", flags: runFlags{printQuestions: true}},
		{name: "should send none under --print-request", flags: runFlags{printRequest: true}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := requests(runConfig{runFlags: tc.flags}); got != tc.want {
				t.Errorf("requests = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestWriteBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		bodies []string
		want   string
	}{
		{
			name:   "should write each body on its own line",
			bodies: []string{`{"a":1}`, `{"b":2}`}, want: "{\"a\":1}\n{\"b\":2}\n",
		},
		{name: "should write an empty body as an empty line", bodies: []string{""}, want: "\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			write := writeBodies(&out)
			for _, body := range tc.bodies {
				if err := write([]byte(body)); err != nil {
					t.Fatalf("write: %v", err)
				}
			}

			if out.String() != tc.want {
				t.Errorf("wrote %q, want %q", out.String(), tc.want)
			}
		})
	}

	t.Run("should return the writer's error", func(t *testing.T) {
		t.Parallel()

		if err := writeBodies(brokenWriter{})([]byte("{}")); !errors.Is(err, errBroken) {
			t.Errorf("write = %v, want %v", err, errBroken)
		}
	})
}

func TestRawRun_Send(t *testing.T) {
	t.Parallel()

	const asked = `{"state":"x","model":"m","questions":{"u":{"type":"noul","instructions":"q"}}}`

	tests := []struct {
		name       string
		response   string
		wantLine   string
		wantErr    bool
		wantTokens int
	}{
		{
			name:       "should write the response to the prepared body on one line",
			response:   "{\"model\":\"m\",\n\"answers\":{\"u\":{\"type\":\"noul\",\"noul\":0.4}},\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}\n",
			wantLine:   `{"model":"m","answers":{"u":{"type":"noul","noul":0.4}},"usage":{"input_tokens":3,"output_tokens":1}}`,
			wantTokens: 3,
		},
		{
			name:     "should keep the base URL hint for a 200 that is not JSON",
			response: "garbage",
			wantLine: "200 response could not be used: the response is not JSON, check the base URL",
			wantErr:  true,
		},
		{
			name:       "should count the tokens of a 200 with no answers",
			response:   `{"model":"m","usage":{"input_tokens":4,"output_tokens":2}}`,
			wantLine:   "the response is not a System One result, since it has no answers",
			wantErr:    true,
			wantTokens: 4,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var sent []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("reading the request body: %v", err)
				}

				sent = body

				w.Header().Set("Content-Type", "application/json")

				if _, err := io.WriteString(w, tc.response); err != nil {
					t.Errorf("writing the stub response: %v", err)
				}
			}))
			defer srv.Close()

			client, err := onesie.New(onesie.WithAPIKey("k"), onesie.WithBaseURL(srv.URL), onesie.WithInsecureHTTP(),
				onesie.WithEnv(func(string) (string, bool) { return "", false }))
			if err != nil {
				t.Fatalf("onesie.New: %v", err)
			}

			body, err := onesie.TypeSafe().PrepareRaw([]byte(asked), client.DefaultModel())
			if err != nil {
				t.Fatalf("PrepareRaw: %v", err)
			}

			stats := &collector{}
			run := &rawRun{stats: stats, provider: onesie.TypeSafe(), client: client}

			line, err := run.send(t.Context(), body)
			if (err != nil) != tc.wantErr || !strings.Contains(string(line), tc.wantLine) {
				t.Errorf("send = %s, %v, want a line holding %q", line, err, tc.wantLine)
			}

			if string(sent) != string(body.Bytes()) {
				t.Errorf("sent %s, want the prepared body %s", sent, body.Bytes())
			}

			if got := stats.snapshot(0); got.InputTokens != tc.wantTokens || got.Questions != 1 {
				t.Errorf("stats = %d input tokens over %d questions, want %d over 1",
					got.InputTokens, got.Questions, tc.wantTokens)
			}
		})
	}
}
