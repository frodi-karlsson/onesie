package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frodi-karlsson/jev-cli/internal/jev"
)

func TestPlural(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		count int
		noun  string
		want  string
	}{
		{name: "should leave a single request bare", count: 1, noun: "request", want: "1 request"},
		{name: "should add an s to two requests", count: 2, noun: "request", want: "2 requests"},
		{
			name: "should leave a single question bare", count: 1, noun: "question",
			want: "1 question",
		},
		{
			name: "should add an s to two questions", count: 2, noun: "question",
			want: "2 questions",
		},
		{name: "should leave a single attempt bare", count: 1, noun: "attempt", want: "1 attempt"},
		{name: "should add an s to two attempts", count: 2, noun: "attempt", want: "2 attempts"},
		{name: "should leave a single retry bare", count: 1, noun: "retry", want: "1 retry"},
		{name: "should spell two retries irregularly", count: 2, noun: "retry", want: "2 retries"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := plural(tc.count, tc.noun); got != tc.want {
				t.Errorf("plural(%d, %q) = %q, want %q", tc.count, tc.noun, got, tc.want)
			}
		})
	}
}

func TestCollectorConcurrent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		workers int
		each    int
	}{
		{name: "should separate retries from terminals under concurrency", workers: 16, each: 50},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var c collector

			var wg sync.WaitGroup

			for range tc.workers {
				wg.Add(1)

				go func() {
					defer wg.Done()

					for range tc.each {
						// A record that was rate limited once and then answered.
						c.observe(jev.Attempt{Index: 0, Status: http.StatusTooManyRequests})
						c.observe(jev.Attempt{Index: 1, Status: http.StatusOK})
						c.record("jev-1.13.0", jev.Usage{InputTokens: 2, OutputTokens: 1}, 1)

						// A record the server refused outright, which is a failed attempt that
						// caused no retry.
						c.observe(jev.Attempt{Index: 0, Status: http.StatusBadRequest})
						c.terminalAttempt(&jev.APIError{Status: http.StatusBadRequest})
						c.recordFailure(true, 1)
					}
				}()
			}

			wg.Wait()

			got := c.snapshot(time.Second, time.Second)
			each := tc.workers * tc.each

			if got.Attempts != 3*each {
				t.Errorf("Attempts = %d, want %d", got.Attempts, 3*each)
			}

			if got.Requests != 2*each {
				t.Errorf("Requests = %d, want %d", got.Requests, 2*each)
			}

			if got.Failed != each {
				t.Errorf("Failed = %d, want %d", got.Failed, each)
			}

			if got.Retries[http.StatusTooManyRequests] != each {
				t.Errorf("Retries[429] = %d, want %d",
					got.Retries[http.StatusTooManyRequests], each)
			}

			// The status that ended its record every time it was seen was never retried, and
			// naming it in the breakdown would report a retry that cannot have happened.
			if _, named := got.Retries[http.StatusBadRequest]; named {
				t.Errorf("Retries names 400, which was never retried: %v", got.Retries)
			}

			if got.InputTokens != 2*each {
				t.Errorf("InputTokens = %d, want %d", got.InputTokens, 2*each)
			}
		})
	}
}

func TestTerminalStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCounted bool
	}{
		{
			name: "should take the status from an API error",
			err:  &jev.APIError{Status: http.StatusBadRequest}, wantStatus: 400, wantCounted: true,
		},
		{
			name: "should reach the API error a retry after error embeds",
			err: &jev.RetryAfterError{
				APIError: jev.APIError{Status: http.StatusTooManyRequests},
			},
			wantStatus: 429, wantCounted: true,
		},
		{
			name: "should count a transport failure as statusless",
			err:  &jev.ConnectionError{Err: errors.New("reset")}, wantCounted: true,
		},
		{
			name:        "should count a timeout as statusless",
			err:         &jev.TimeoutError{ConnectionError: jev.ConnectionError{Err: errors.New("x")}},
			wantCounted: true,
		},
		{
			name: "should count an interrupt as statusless",
			err:  fmt.Errorf("jev: #1: %w", context.Canceled), wantCounted: true,
		},
		{
			// Its attempt came back 2xx and was counted as a success, so subtracting it here
			// would hide a real transport retry.
			name: "should not count a 2xx body that could not be used",
			err:  &jev.ResponseError{Status: http.StatusOK, Err: errors.New("bad json")},
		},
		{
			name: "should not count a request rejected before it was sent",
			err:  &jev.ValidationError{Message: "no API key"},
		},
		{
			name: "should not count an answer the response was missing",
			err:  &jev.AnswerError{Name: "urgent", Missing: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, counted := terminalStatus(tc.err)
			if status != tc.wantStatus || counted != tc.wantCounted {
				t.Errorf("terminalStatus() = (%d, %t), want (%d, %t)",
					status, counted, tc.wantStatus, tc.wantCounted)
			}
		})
	}
}

func TestNewRootCmdStats(t *testing.T) {
	t.Parallel()

	const answered = `{"model":"jev-1.13.0","answers":{"urgent":{"type":"noul","noul":0.9}},` +
		`"usage":{"input_tokens":2841,"output_tokens":71}}`

	tests := []struct {
		name       string
		args       []string
		stdin      string
		handler    func() http.HandlerFunc
		wantCode   int
		wantErr    []string
		absentOut  []string
		wantOutHas []string
	}{
		{
			name:     "should write the summary to stderr and leave stdout to the answer",
			args:     []string{"--ask", "urgent=is this urgent", "-o", "json", "--stats"},
			stdin:    "the server is down",
			handler:  func() http.HandlerFunc { return answerHandler(answered) },
			wantCode: ExitOK,
			wantErr: []string{
				"1 request, 1 question, 2841 in / 71 out, model jev-1.13.0, " +
					"1 attempt, 10s/attempt,",
			},
			wantOutHas: []string{`"urgent"`},
			// The whole reason the line goes to stderr. On stdout it would corrupt every
			// pipeline --stats exists to measure.
			absentOut: []string{"1 request", "attempt"},
		},
		{
			name:  "should count a retry rather than an attempt and name its status",
			args:  []string{"--ask", "urgent=is this urgent", "-o", "json", "--stats"},
			stdin: "the server is down",
			handler: func() http.HandlerFunc {
				return onceThen(http.StatusTooManyRequests, answered)
			},
			wantCode:   ExitOK,
			wantErr:    []string{"2 attempts (1 retry: 429×1)"},
			wantOutHas: []string{`"urgent"`},
		},
		{
			name:  "should name a retry with no status transport",
			args:  []string{"--ask", "urgent=is this urgent", "-o", "json", "--stats"},
			stdin: "the server is down",
			handler: func() http.HandlerFunc {
				return hangUpThen(answered)
			},
			wantCode:   ExitOK,
			wantErr:    []string{"2 attempts (1 retry: transport×1)"},
			wantOutHas: []string{`"urgent"`},
		},
		{
			name: "should separate records from requests when a line fails to parse",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "-o", "json", "--stats",
			},
			stdin:      "{\"id\":1}\nnot json\n",
			handler:    func() http.HandlerFunc { return answerHandler(answered) },
			wantCode:   ExitRecords,
			wantErr:    []string{"2 records, 1 failed, 1 request, 1 question, 2841 in / 71 out"},
			wantOutHas: []string{`"urgent"`},
		},
		{
			// The 400 ended its record, so it caused no retry. Naming it in the breakdown is the
			// report a caller cannot tell from a real one.
			name: "should name only the status that was retried",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "-o", "json",
				"-j", "1", "--stats",
			},
			stdin: "{\"id\":1}\n{\"id\":2}\n",
			handler: func() http.HandlerFunc {
				return scripted(http.StatusBadRequest, http.StatusTooManyRequests, answered)
			},
			wantCode:   ExitRecords,
			wantErr:    []string{"3 attempts (1 retry: 429×1)"},
			wantOutHas: []string{`"status":400`},
		},
		{
			name: "should name only the status that was retried under concurrency",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "-o", "json",
				"-j", "2", "--unordered", "--stats",
			},
			stdin: "{\"id\":1}\n{\"id\":2}\n",
			handler: func() http.HandlerFunc {
				return scripted(http.StatusBadRequest, http.StatusTooManyRequests, answered)
			},
			wantCode:   ExitRecords,
			wantErr:    []string{"3 attempts (1 retry: 429×1)"},
			wantOutHas: []string{`"status":400`},
		},
		{
			// The mutation this catches is counting the record as neither a record nor a failure,
			// which suppresses both clauses and reports a clean run beside exit 6.
			name: "should count a merge collision as a failed record",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "-o", "json",
				"--merge", "--stats",
			},
			stdin:      "{\"id\":1}\n{\"id\":2,\"answers\":{}}\n{\"id\":3}\n",
			handler:    func() http.HandlerFunc { return answerHandler(answered) },
			wantCode:   ExitRecords,
			wantErr:    []string{"3 records, 1 failed, 2 requests, 2 questions"},
			wantOutHas: []string{"would overwrite"},
		},
		{
			name: "should total every record when they run concurrently",
			args: []string{
				"--ask", "urgent=is this urgent", "-i", "jsonl", "-o", "json",
				"-j", "4", "--stats",
			},
			stdin: strings.Repeat("{\"id\":1}\n", 8),
			handler: func() http.HandlerFunc {
				return answerHandler(answered)
			},
			wantCode: ExitOK,
			wantErr: []string{
				"8 requests, 8 questions, 22728 in / 568 out, model jev-1.13.0, 8 attempts",
			},
			wantOutHas: []string{`"urgent"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(tc.handler())
			defer srv.Close()

			out, errOut, code := runAgainst(t, tc.args, tc.stdin, srv.URL)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out, errOut)
			}

			for _, want := range tc.wantErr {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr missing %q\ngot:\n%s", want, errOut)
				}
			}

			for _, want := range tc.wantOutHas {
				if !strings.Contains(out, want) {
					t.Errorf("stdout missing %q\ngot:\n%s", want, out)
				}
			}

			for _, unwanted := range tc.absentOut {
				if strings.Contains(out, unwanted) {
					t.Errorf("stdout should not contain %q\ngot:\n%s", unwanted, out)
				}
			}
		})
	}
}

func answerHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if _, err := io.WriteString(w, body); err != nil {
			panic(err)
		}
	}
}

func scripted(first, second int, body string) http.HandlerFunc {
	var seen atomic.Int32

	// Keyed by call order rather than by record, because -j 2 --unordered decides that order and
	// the point of the case is that the summary does not.
	return func(w http.ResponseWriter, r *http.Request) {
		switch seen.Add(1) {
		case 1:
			status(first)(w, r)
		case 2:
			status(second)(w, r)
		default:
			answerHandler(body)(w, r)
		}
	}
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)

		if _, err := io.WriteString(w, `{"error":{"message":"stub"}}`); err != nil {
			panic(err)
		}
	}
}

func onceThen(status int, body string) http.HandlerFunc {
	var first atomic.Bool

	return func(w http.ResponseWriter, r *http.Request) {
		if first.CompareAndSwap(false, true) {
			w.WriteHeader(status)

			return
		}

		answerHandler(body)(w, r)
	}
}

func hangUpThen(body string) http.HandlerFunc {
	var first atomic.Bool

	return func(w http.ResponseWriter, r *http.Request) {
		if first.CompareAndSwap(false, true) {
			// A connection dropped mid request is a transport failure with no status at all,
			// which is the case the breakdown labels rather than printing a bare zero.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				panic(err)
			}

			conn.Close()

			return
		}

		answerHandler(body)(w, r)
	}
}

func TestNewRootCmdStatsRequestMode(t *testing.T) {
	t.Parallel()

	const body = `{"state":{"id":1},"questions":{"a":{"type":"noul","instructions":"q1"},` +
		`"b":{"type":"noul","instructions":"q2"}}}`

	tests := []struct {
		name     string
		status   int
		response string
		wantCode int
		wantErr  string
	}{
		{
			name:   "should take tokens from the response and questions from the request",
			status: http.StatusOK,
			response: `{"model":"jev-1.14.0","answers":{},` +
				`"usage":{"input_tokens":12,"output_tokens":3}}`,
			wantCode: ExitOK,
			wantErr: "1 request, 2 questions, 12 in / 3 out, model jev-1.14.0, " +
				"1 attempt, 10s/attempt,",
		},
		{
			// The response carries no questions at all, so a failed record is the case that
			// proves the count came from the body that was sent.
			name:     "should count the questions a failed record carried",
			status:   http.StatusUnprocessableEntity,
			response: `{"error":{"message":"bad body"}}`,
			wantCode: ExitRecords,
			wantErr:  "1 request, 1 failed, 2 questions, 0 in / 0 out, 1 attempt, 10s/attempt,",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, out, errOut, code := runRequestMode(t,
				[]string{"-i", "request", "--stats"}, body+"\n", tc.status, tc.response)

			if code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, tc.wantCode, out, errOut)
			}

			if !strings.Contains(errOut, tc.wantErr) {
				t.Errorf("stderr missing %q\ngot:\n%s", tc.wantErr, errOut)
			}

			if strings.Contains(out, "questions,") {
				t.Errorf("stdout should not carry the summary, got:\n%s", out)
			}
		})
	}
}

func TestNewRootCmdPrintFlagRejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "should reject stats with print-request",
			args: []string{"--ask", "urgent=is this urgent", "--print-request", "--stats"},
			want: "jev: --stats has nothing to report with --print-request, " +
				"which makes no request",
		},
		{
			name: "should reject stats with print-questions",
			args: []string{"--ask", "urgent=is this urgent", "--print-questions", "--stats"},
			want: "jev: --stats has nothing to report with --print-questions, " +
				"which makes no request",
		},
		{
			// The path that returns before a plan is built, so the rule has to sit above it.
			name: "should reject stats with print-request under a request mode stream",
			args: []string{"-i", "request", "--print-request", "--stats"},
			want: "jev: --stats has nothing to report with --print-request, " +
				"which makes no request",
		},
		{
			name: "should reject both print flags at once",
			args: []string{
				"--ask", "urgent=is this urgent", "--print-request", "--print-questions",
			},
			want: "jev: --print-request and --print-questions each write a different thing " +
				"to stdout. Pass one",
		},
		{
			name: "should reject an output mode with print-questions",
			args: []string{
				"--ask", "urgent=is this urgent", "--print-questions", "-o", "json",
			},
			want: "jev: -o does not apply to --print-questions, which writes a question file",
		},
		{
			name: "should reject an output mode with print-request",
			args: []string{"--ask", "urgent=is this urgent", "--print-request", "-o", "json"},
			want: "jev: -o does not apply to --print-request, which writes a request body",
		},
		{
			name: "should reject the raw shorthand with print-request",
			args: []string{"--ask", "urgent=is this urgent", "--print-request", "-r"},
			want: "jev: -r does not apply to --print-request, which writes a request body",
		},
		{
			name: "should reject quiet with print-questions",
			args: []string{"--ask", "urgent=is this urgent", "--print-questions", "-q"},
			want: "jev: -q suppresses output, which leaves --print-questions nothing to write",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out, errOut, code := runOffline(t, tc.args)

			if code != ExitUsage {
				t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
					code, ExitUsage, out, errOut)
			}

			// Rejected before anything reaches stdout, which is the reason the rule lives in
			// CheckFlags rather than in the print path.
			if out != "" {
				t.Errorf("stdout should be empty, got:\n%s", out)
			}

			if !strings.Contains(errOut, tc.want) {
				t.Errorf("stderr = %q, want it to contain %q", errOut, tc.want)
			}
		})
	}
}
