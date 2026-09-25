package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const dedupAnswer = `{"model":"m","usage":{"input_tokens":5,"output_tokens":2},` +
	`"answers":{"answer":{"type":"noul","noul":0.9}}}`

func TestStream(t *testing.T) {
	t.Parallel()

	jsonl := func(bodies ...string) string {
		var lines strings.Builder
		for i, body := range bodies {
			fmt.Fprintf(&lines, "{\"id\":%d,\"body\":%q}\n", i+1, body)
		}

		return lines.String()
	}
	byID := []string{"is this urgent", "-i", "jsonl", "--map", ".body", "--id", ".id", "-o", "json"}
	with := func(extra ...string) []string {
		return append(append([]string{}, byID...), extra...)
	}

	t.Run("should ask identical requests once and print every record in input order", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name         string
			bodies       []string
			args         []string
			wantRequests int32
		}{
			{
				name:   "should ask two identical records in a row once",
				bodies: []string{"x", "x", "y", "z"}, args: with(), wantRequests: 3,
			},
			{
				name:   "should ask identical records far apart once",
				bodies: []string{"x", "y", "z", "w", "v", "x"}, args: with(), wantRequests: 5,
			},
			{
				name:   "should ask once under -j 4",
				bodies: []string{"x", "x", "y", "x", "y", "x", "z", "z"}, args: with("-j", "4"), wantRequests: 3,
			},
			{
				name:   "should ask every record under --no-dedup",
				bodies: []string{"x", "x", "y", "z"}, args: with("--no-dedup"), wantRequests: 4,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				stub := newCountingStub(t, nil)

				out, errOut, code := runStub(t, t.Context(), tc.args, jsonl(tc.bodies...), stub.url)
				if code != ExitOK {
					t.Fatalf("exit code = %d\n%s", code, errOut)
				}

				if got := stub.requests.Load(); got != tc.wantRequests {
					t.Errorf("%d requests, want %d", got, tc.wantRequests)
				}

				lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
				if len(lines) != len(tc.bodies) {
					t.Fatalf("%d lines, want %d\n%s", len(lines), len(tc.bodies), out)
				}

				for i, written := range lines {
					if want := fmt.Sprintf(`{"id":%d,`, i+1); !strings.HasPrefix(written, want) {
						t.Errorf("line %d = %s, want it to start %s", i+1, written, want)
					}

					if !strings.Contains(written, `"answer":{"value":0.9}`) {
						t.Errorf("line %d holds no answer: %s", i+1, written)
					}
				}
			})
		}
	})

	t.Run("should print one line per record under --unordered", func(t *testing.T) {
		t.Parallel()

		stub := newCountingStub(t, nil)
		bodies := []string{"x", "x", "y", "x", "y", "x", "z", "z"}

		out, errOut, code := runStub(t, t.Context(), with("-j", "4", "--unordered"), jsonl(bodies...), stub.url)
		if code != ExitOK {
			t.Fatalf("exit code = %d\n%s", code, errOut)
		}

		if got := stub.requests.Load(); got != 3 {
			t.Errorf("%d requests, want 3", got)
		}

		for i := range bodies {
			if got := strings.Count(out, fmt.Sprintf(`{"id":%d,`, i+1)); got != 1 {
				t.Errorf("id %d written %d times, want once\n%s", i+1, got, out)
			}
		}
	})

	t.Run("should make a duplicate of a request in flight wait for it", func(t *testing.T) {
		t.Parallel()

		release := make(chan struct{})

		var held atomic.Bool

		stub := newCountingStub(t, func() {
			if held.CompareAndSwap(false, true) {
				<-release
			}
		})

		labels := pprof.Labels("dedup", t.Name())
		exited := make(chan struct{})

		var (
			out, errOut string
			code        int
		)

		go pprof.Do(t.Context(), labels, func(ctx context.Context) {
			defer close(exited)

			out, errOut, code = runStub(t, ctx, with("-j", "2"), jsonl("x", "x"), stub.url)
		})

		if !awaitWaiter(t, labels) {
			t.Error("the second record never waited for the first")
		}

		close(release)
		<-exited

		if code != ExitOK {
			t.Fatalf("exit code = %d\n%s", code, errOut)
		}

		if got := stub.requests.Load(); got != 1 {
			t.Errorf("%d requests, want 1", got)
		}

		if got := strings.Count(out, "\n"); got != 2 {
			t.Errorf("%d lines, want 2\n%s", got, out)
		}
	})

	t.Run("should fail every record that shares a failed request and ask them again on --resume", func(t *testing.T) {
		t.Parallel()

		var refusing atomic.Bool
		refusing.Store(true)

		var requests atomic.Int32

		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)

			if refusing.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)

				if _, err := w.Write([]byte(`{"error":{"message":"busy"}}`)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}

				return
			}

			if _, err := w.Write([]byte(dedupAnswer)); err != nil {
				t.Errorf("writing stub response: %v", err)
			}
		}))
		t.Cleanup(stub.Close)

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		args := with("--out", answers, "--resume")

		_, errOut, code := runStub(t, t.Context(), args, jsonl("x", "x", "y"), stub.URL)
		if code != ExitRecords {
			t.Fatalf("first run exit code = %d, want %d\n%s", code, ExitRecords, errOut)
		}

		first := readText(t, answers)
		if got := strings.Count(first, `"status":503`); got != 3 {
			t.Errorf("%d failed lines, want 3\n%s", got, first)
		}

		if got := requests.Swap(0); got != 2 {
			t.Errorf("the first run made %d requests, want 2", got)
		}

		refusing.Store(false)

		_, errOut, code = runStub(t, t.Context(), args, jsonl("x", "x", "y"), stub.URL)
		if code != ExitOK {
			t.Fatalf("second run exit code = %d\n%s", code, errOut)
		}

		second := readText(t, answers)
		if got := strings.Count(second, `"answer":{"value":0.9}`); got != 3 {
			t.Errorf("%d answered lines, want 3\n%s", got, second)
		}

		if got := requests.Load(); got != 2 {
			t.Errorf("the resumed run made %d requests, want 2", got)
		}
	})

	t.Run("should put the tokens on the first line written for a group under --usage", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			args    []string
			ordered bool
		}{
			{name: "should bill the first record in input order", args: with("--usage", "-j", "4"), ordered: true},
			{
				name: "should bill one line of each group under --unordered",
				args: with("--usage", "-j", "4", "--unordered"),
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				stub := newCountingStub(t, nil)

				out, errOut, code := runStub(t, t.Context(), tc.args, jsonl("x", "x", "y", "x", "y"), stub.url)
				if code != ExitOK {
					t.Fatalf("exit code = %d\n%s", code, errOut)
				}

				billed, zero, input, output := 0, 0, 0, 0

				for _, written := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
					var parsed struct {
						Usage *struct {
							Input  int `json:"input_tokens"`
							Output int `json:"output_tokens"`
						} `json:"usage"`
					}

					if err := json.Unmarshal([]byte(written), &parsed); err != nil || parsed.Usage == nil {
						t.Fatalf("line %s carries no usage: %v", written, err)
					}

					input += parsed.Usage.Input
					output += parsed.Usage.Output

					if parsed.Usage.Input == 0 && parsed.Usage.Output == 0 {
						zero++
					} else {
						billed++
					}
				}

				requests := int(stub.requests.Load())
				if billed != requests || zero != 5-requests {
					t.Errorf("%d billed and %d zero lines for %d requests\n%s", billed, zero, requests, out)
				}

				if input != 5*requests || output != 2*requests {
					t.Errorf("summed usage %d in / %d out, want %d / %d", input, output, 5*requests, 2*requests)
				}

				if tc.ordered && !strings.HasPrefix(out, `{"id":1,"model":"m","usage":{"input_tokens":5`) {
					t.Errorf("the first record is not the one billed\n%s", out)
				}
			})
		}
	})

	t.Run("should report the deduplicated records under --stats", func(t *testing.T) {
		t.Parallel()

		stub := newCountingStub(t, nil)

		_, errOut, code := runStub(t, t.Context(), with("--stats"), jsonl("x", "x", "y"), stub.url)
		if code != ExitOK || !strings.Contains(errOut, "3 records, 1 deduplicated, 2 requests, 2 questions") {
			t.Errorf("exit code = %d\n%s", code, errOut)
		}
	})

	t.Run("should say once past the limit and ask the new records after it", func(t *testing.T) {
		t.Parallel()

		stub := newCountingStub(t, nil)

		_, errOut, code := runStub(t, t.Context(), with(), jsonl("x", "x", "y", "y", "z"), stub.url,
			func(s *rootSettings) { s.dedupLimit = 1 })
		if code != ExitOK {
			t.Fatalf("exit code = %d\n%s", code, errOut)
		}

		if got := stub.requests.Load(); got != 4 {
			t.Errorf("%d requests, want 4", got)
		}

		note := "onesie: 1 distinct requests held, so later records are deduplicated only against those\n"
		if strings.Count(errOut, note) != 1 {
			t.Errorf("stderr = %q, want the note once", errOut)
		}
	})

	t.Run("should keep each record's own input under --merge", func(t *testing.T) {
		t.Parallel()

		stub := newCountingStub(t, nil)

		out, errOut, code := runStub(t, t.Context(),
			[]string{"is this urgent", "-i", "jsonl", "--map", ".body", "--merge"},
			"{\"n\":1,\"body\":\"x\"}\n{\"n\":2,\"body\":\"x\"}\n", stub.url)
		if code != ExitOK {
			t.Fatalf("exit code = %d\n%s", code, errOut)
		}

		want := `{"n":1,"body":"x","answers":{"model":"m","answer":{"value":0.9}}}` + "\n" +
			`{"n":2,"body":"x","answers":{"model":"m","answer":{"value":0.9}}}` + "\n"
		if out != want {
			t.Errorf("stdout = %q, want %q", out, want)
		}

		if got := stub.requests.Load(); got != 1 {
			t.Errorf("%d requests, want 1", got)
		}
	})

	t.Run("should still print every body under --print-request", func(t *testing.T) {
		t.Parallel()

		out, errOut, code := runOfflineStdin(t, []string{"is this urgent", "-i", "lines", "--print-request"}, "x\nx\n")
		if code != ExitOK || strings.Count(out, `"state":"x"`) != 2 {
			t.Errorf("exit code = %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}
	})

	t.Run("should deduplicate under --mock only records the file answers alike", func(t *testing.T) {
		t.Parallel()

		values := []string{"is it urgent", "-o", "values", "-i", "lines", "--stats"}
		low, high := `{"answer":0.1}`, `{"answer":0.8}`

		tests := []struct {
			name     string
			jobs     string
			mock     string
			wantCode int
			wantOut  string
			stderr   string
		}{
			{
				name: "should give identical records their own different answers", jobs: "1",
				mock: low + "\n" + high + "\n", wantOut: low + "\n" + high + "\n", stderr: "2 requests",
			},
			{
				name: "should ask identical records with identical answers once", jobs: "1",
				mock: low + "\n" + low + "\n", wantOut: low + "\n" + low + "\n", stderr: "2 records, 1 deduplicated, 1 request",
			},
			{
				name: "should never share a covered record's answer with an uncovered one", jobs: "1",
				mock: low + "\n" + mockUnread + "\n", wantCode: ExitUsage, wantOut: low + "\n", stderr: "input line 2",
			},
			{
				name: "should never share a covered record's answer with an uncovered one under -j 2", jobs: "2",
				mock: low + "\n" + mockUnread + "\n", wantCode: ExitUsage, wantOut: low + "\n", stderr: "input line 2",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				args := append(append([]string{}, values...), "-j", tc.jobs, "--mock", writeMock(t, tc.mock))

				out, errOut, code := runMocked(t, t.Context(), args, "a\na\n", nil, nil)
				checkMocked(t, out, errOut, code, tc.wantCode, &tc.wantOut, nil, nil, []string{tc.stderr})
			})
		}
	})

	t.Run("should name the first uncovered input line under --mock and -j 8 every time", func(t *testing.T) {
		t.Parallel()

		covered := writeMock(t, `{"answer":0.1}`+"\n"+`{"answer":0.1}`+"\n")
		args := []string{"is it urgent", "-o", "values", "-i", "lines", "-j", "8", "--mock", covered}

		for range 20 {
			_, errOut, code := runMocked(t, t.Context(), args, strings.Repeat("a\n", 16), nil, nil)
			if code != ExitUsage || !strings.Contains(errOut, "so input line 3 has no answer") {
				t.Fatalf("exit code = %d, want %d naming input line 3\n%s", code, ExitUsage, errOut)
			}
		}
	})

	t.Run("should write only the failing line under --stop-on-error and -j 4 every time", func(t *testing.T) {
		t.Parallel()

		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("reading the request: %v", err)
			}

			if bytes.Contains(body, []byte(`"state":"fail"`)) {
				w.WriteHeader(http.StatusInternalServerError)

				if _, err := w.Write([]byte(`{"error":{"message":"boom"}}`)); err != nil {
					t.Errorf("writing stub response: %v", err)
				}

				return
			}

			if _, err := w.Write([]byte(dedupAnswer)); err != nil {
				t.Errorf("writing stub response: %v", err)
			}
		}))
		t.Cleanup(stub.Close)

		args := []string{"is this urgent", "-i", "lines", "-o", "json", "-j", "4", "--stop-on-error", "--retries", "0"}

		for range 30 {
			out, errOut, code := runStub(t, t.Context(), args, "fail\nfail\nx\n", stub.URL)
			if code != ExitUnavailable {
				t.Fatalf("exit code = %d, want %d\n%s", code, ExitUnavailable, errOut)
			}

			if strings.Count(out, "\n") != 1 || !strings.Contains(out, `"status":500`) {
				t.Fatalf("stdout = %q, want only the first record's error line", out)
			}
		}
	})
}

func newCountingStub(t *testing.T, hold func()) *countingStub {
	t.Helper()

	stub := &countingStub{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		stub.requests.Add(1)

		if hold != nil {
			hold()
		}

		if _, err := w.Write([]byte(dedupAnswer)); err != nil {
			t.Errorf("writing stub response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	stub.url = srv.URL

	return stub
}

type countingStub struct {
	url      string
	requests atomic.Int32
}

func runStub(
	t *testing.T, ctx context.Context, args []string, stdin, base string, extra ...RootOption,
) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	opts := []RootOption{
		WithKeychain(noKeychain()),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
		WithClientFactory(stubFactory(base)),
	}

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, append(opts, extra...)...)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(ctx, root)

	return out.String(), errOut.String(), code
}

func awaitWaiter(t *testing.T, labels pprof.LabelSet) bool {
	t.Helper()

	var want string

	pprof.ForLabels(pprof.WithLabels(context.Background(), labels), func(key, value string) bool {
		want = fmt.Sprintf("# labels: {%q:%q}", key, value)

		return true
	})

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		if waiting(t, want) {
			return true
		}

		time.Sleep(time.Millisecond)
	}

	return false
}

func waiting(t *testing.T, labelLine string) bool {
	t.Helper()

	var profile strings.Builder
	if err := pprof.Lookup("goroutine").WriteTo(&profile, 1); err != nil {
		t.Errorf("writing the goroutine profile: %v", err)

		return false
	}

	// A waiter sits in share with no request of its own below it.
	for _, stack := range strings.Split(profile.String(), "\n\n") {
		if strings.Contains(stack, labelLine) && strings.Contains(stack, "cli.(*dedup).share") &&
			!strings.Contains(stack, "cli.evaluate") {
			return true
		}
	}

	return false
}
