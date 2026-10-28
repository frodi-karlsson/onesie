package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/plan"
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

				stub := newCountingStub(t)

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

		stub := newCountingStub(t)
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

	t.Run("should stop a runaway --id once the stream ends early", func(t *testing.T) {
		t.Parallel()

		const runaway = `if .id == "b" then until(false; .) else .id end`

		tests := []struct {
			name     string
			args     []string
			breaks   bool
			wantCode int
		}{
			{
				name:     "should stop it when an authentication failure aborts the stream",
				args:     []string{"x", "-i", "jsonl", "--id", runaway, "-j", "1"},
				wantCode: ExitAuth,
			},
			{
				name:     "should stop it when the output pipe closes under --print-request",
				args:     []string{"x", "-i", "jsonl", "--id", runaway, "--print-request", "-m", "m1"},
				breaks:   true,
				wantCode: ExitOK,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				args := tc.args
				if !tc.breaks {
					args = append(slices.Clone(args), "--mock", writeMock(t, `{"id":"a","error":401}`))
				}

				// synctest.Test returns only once every goroutine the run started has ended, so a
				// runaway left spinning on the second record fails the test by never returning.
				synctest.Test(t, func(t *testing.T) {
					stdin := &handingLines{data: "{\"id\":\"a\"}\n{\"id\":\"b\"}\n", at: 2, handed: make(chan struct{})}

					var out io.Writer = &bytes.Buffer{}
					if tc.breaks {
						out = closingPipe{after: stdin.handed}
					}

					var errOut bytes.Buffer

					// The first record's outcome waits for the second record to be read, so its --id
					// starts before the stream ends.
					root := NewRootCmd(BuildInfo{Version: "1.2.3"},
						withKeychain(noKeychain()),
						WithStdin(stdin),
						WithStdinTTY(false),
						WithStdoutTTY(false),
						WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
						holding(`"id":"a"`, 1, stdin.handed, nil),
					)

					root.Command.SetOut(out)
					root.Command.SetErr(&errOut)
					root.Command.SetArgs(args)

					if code := root.Execute(t.Context()); code != tc.wantCode {
						t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, tc.wantCode, errOut.String())
					}
				})
			})
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

				stub := newCountingStub(t)

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

	t.Run("should answer a repeated stream from the cache", func(t *testing.T) {
		t.Parallel()

		cached := func(t *testing.T, env map[string]string, args []string, stdin, url string) (string, string, int) {
			t.Helper()

			return runStub(t, t.Context(), args, stdin, url, WithLookupEnv(lookupFrom(env)))
		}

		t.Run("should make four requests for four distinct records run twice under -j 4", func(t *testing.T) {
			t.Parallel()

			stub := newAnswerStub(t)
			env := cacheEnv(t)
			stdin := jsonl("a", "b", "c", "d")

			for range 2 {
				if _, errOut, code := cached(t, env, with("--cache", "-j", "4"), stdin, stub.url); code != ExitOK {
					t.Fatalf("exit %d\n%s", code, errOut)
				}
			}

			if got := stub.requests.Load(); got != 4 {
				t.Errorf("%d requests, want 4", got)
			}
		})

		t.Run("should count a group whose first record hits as one cached and the rest deduplicated", func(t *testing.T) {
			t.Parallel()

			stub := newAnswerStub(t)
			env := cacheEnv(t)

			cached(t, env, with("--cache"), jsonl("x"), stub.url)

			_, errOut, code := cached(t, env, with("--cache", "--stats"), jsonl("x", "x"), stub.url)
			if code != ExitOK || !strings.Contains(errOut, "2 records, 1 deduplicated, 1 cached, 0 requests") {
				t.Errorf("exit %d, stderr %q, want 1 cached and 1 deduplicated", code, errOut)
			}
		})

		exits := []struct {
			name   string
			extra  []string
			status int32
			want   int
			asked  int32
		}{
			{name: "should exit 0 on both runs", want: ExitOK, asked: 1},
			{name: "should exit 1 for a false assertion on both runs", extra: []string{"--assert", "answer.value < 0.5"}, want: ExitRejected, asked: 1},
			{
				name: "should exit 7 for an abstain on both runs", want: ExitAbstain, asked: 1,
				extra: []string{"--assert", "answer.value < 0.5", "--abstain-if", "answer.value > 0.5"},
			},
			{name: "should exit 6 for a failed record on both runs and ask it again", status: 503, extra: []string{"--retries", "0"}, want: ExitRecords, asked: 2},
		}

		for _, tc := range exits {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				stub := newAnswerStub(t)
				stub.status.Store(tc.status)
				env := cacheEnv(t)
				args := with(append([]string{"--cache"}, tc.extra...)...)

				first, _, firstCode := cached(t, env, args, jsonl("x"), stub.url)
				second, errOut, code := cached(t, env, args, jsonl("x"), stub.url)

				if firstCode != tc.want || code != tc.want || second != first {
					t.Errorf("exit %d then %d, want %d, stdout %q then %q\n%s", firstCode, code, tc.want, first, second, errOut)
				}

				if got := stub.requests.Load(); got != tc.asked {
					t.Errorf("%d requests, want %d", got, tc.asked)
				}
			})
		}

		t.Run("should write zero usage for a cached line in --out", func(t *testing.T) {
			t.Parallel()

			stub := newAnswerStub(t)
			env := cacheEnv(t)
			answers := filepath.Join(t.TempDir(), "answers.jsonl")

			cached(t, env, with("--cache"), jsonl("x"), stub.url)

			if _, errOut, code := cached(t, env, with("--cache", "--usage", "--out", answers), jsonl("x"), stub.url); code != ExitOK {
				t.Fatalf("exit %d\n%s", code, errOut)
			}

			written, err := os.ReadFile(answers)
			if err != nil || !strings.Contains(string(written), `"usage":{"input_tokens":0,"output_tokens":0}`) {
				t.Errorf("--out = %s, %v, want zero usage", written, err)
			}
		})

		for _, order := range []struct {
			name         string
			first, later []string
		}{
			{name: "should resume a file written with --cache without it", first: []string{"--cache"}},
			{name: "should resume a file written without --cache with it", later: []string{"--cache"}},
		} {
			t.Run(order.name, func(t *testing.T) {
				t.Parallel()

				stub := newAnswerStub(t)
				env := cacheEnv(t)
				answers := filepath.Join(t.TempDir(), "answers.jsonl")

				if _, errOut, code := cached(t, env, with(append(order.first, "--out", answers)...), jsonl("a", "b"), stub.url); code != ExitOK {
					t.Fatalf("first run exit %d\n%s", code, errOut)
				}

				args := with(append(order.later, "--out", answers, "--resume")...)
				if _, errOut, code := cached(t, env, args, jsonl("a", "b", "c"), stub.url); code != ExitOK {
					t.Fatalf("resumed run exit %d\n%s", code, errOut)
				}

				if got := stub.requests.Load(); got != 3 {
					t.Errorf("%d requests, want 3", got)
				}
			})
		}

		t.Run("should exit 2 before any request for a cache dir others can read", func(t *testing.T) {
			t.Parallel()

			if runtime.GOOS == "windows" {
				t.Skip("windows carries no unix permission bits, so the mode check does not apply")
			}

			stub := newAnswerStub(t)
			env := cacheEnv(t)
			mustMkdirMode(t, env["ONESIE_CACHE_DIR"], 0o755)

			out, errOut, code := cached(t, env, with("--cache", "-j", "4"), jsonl("a", "b", "c"), stub.url)
			if code != ExitUsage || out != "" || !strings.Contains(errOut, "chmod 700") {
				t.Errorf("exit %d, stdout %q, stderr %q, want exit 2 with the chmod advice and no line", code, out, errOut)
			}

			if got := stub.requests.Load(); got != 0 {
				t.Errorf("%d requests, want none", got)
			}
		})

		t.Run("should leave the cache dir absent for a run --resume finds fully answered", func(t *testing.T) {
			t.Parallel()

			stub := newAnswerStub(t)
			env := cacheEnv(t)
			answers := filepath.Join(t.TempDir(), "answers.jsonl")

			cached(t, env, with("--out", answers), jsonl("a", "b"), stub.url)

			if _, errOut, code := cached(t, env, with("--cache", "--out", answers, "--resume"), jsonl("a", "b"), stub.url); code != ExitOK {
				t.Fatalf("exit %d\n%s", code, errOut)
			}

			if _, err := os.Stat(env["ONESIE_CACHE_DIR"]); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the cache dir exists: %v", err)
			}
		})
	})

	t.Run("should report the deduplicated records under --stats", func(t *testing.T) {
		t.Parallel()

		stub := newCountingStub(t)

		_, errOut, code := runStub(t, t.Context(), with("--stats"), jsonl("x", "x", "y"), stub.url)
		if code != ExitOK || !strings.Contains(errOut, "3 records, 1 deduplicated, 2 requests, 2 questions") {
			t.Errorf("exit code = %d\n%s", code, errOut)
		}
	})

	t.Run("should say once past the limit and ask the new records after it", func(t *testing.T) {
		t.Parallel()

		stub := newCountingStub(t)

		_, errOut, code := runStub(t, t.Context(), with(), jsonl("x", "x", "y", "y", "z"), stub.url,
			func(s *rootSettings) { s.dedupLimit = 1 })
		if code != ExitOK {
			t.Fatalf("exit code = %d\n%s", code, errOut)
		}

		if got := stub.requests.Load(); got != 4 {
			t.Errorf("%d requests, want 4", got)
		}

		note := "1 distinct requests held, so later records are deduplicated only against those\n"
		if strings.Count(errOut, note) != 1 {
			t.Errorf("stderr = %q, want the note once", errOut)
		}
	})

	t.Run("should keep each record's own input under --merge", func(t *testing.T) {
		t.Parallel()

		stub := newCountingStub(t)

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
		lowAt1, lowAt2, highAt2 := `{"line":1,"answer":0.1}`, `{"line":2,"answer":0.1}`, `{"line":2,"answer":0.8}`

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
				mock: low + "\n" + high + "\n", wantOut: lowAt1 + "\n" + highAt2 + "\n", stderr: "2 records answered from the mock",
			},
			{
				name: "should ask identical records with identical answers once", jobs: "1",
				mock: low + "\n" + low + "\n", wantOut: lowAt1 + "\n" + lowAt2 + "\n", stderr: "2 records, 1 deduplicated, 1 answered from the mock",
			},
			{
				name: "should never share a covered record's answer with an uncovered one", jobs: "1",
				mock: low + "\n" + mockUnread + "\n", wantCode: ExitUsage, wantOut: lowAt1 + "\n", stderr: "input line 2",
			},
			{
				name: "should never share a covered record's answer with an uncovered one under -j 2", jobs: "2",
				mock: low + "\n" + mockUnread + "\n", wantCode: ExitUsage, wantOut: lowAt1 + "\n", stderr: "input line 2",
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

	t.Run("should attach images per record", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		square := writeImage(t, dir, "square.png", squarePNG(t))
		empty := writeImage(t, dir, "empty.png", emptyPNG(t))

		tests := []struct {
			name      string
			args      []string
			stdin     string
			wantCode  int
			wantLines int
			wantOut   []string
		}{
			{
				name:      "should read each record's field as an image",
				args:      []string{"-i", "jsonl", "--image-field", "photo", "is there a red square in `photo`", "-o", "json"},
				stdin:     `{"photo":` + jsonText(t, square) + `}` + "\n" + `{"photo":` + jsonText(t, empty) + `}` + "\n",
				wantCode:  ExitOK,
				wantLines: 2,
			},
			{
				name:      "should take each line as the path under -i lines",
				args:      []string{"-i", "lines", "--image-field", "photo", "q", "-o", "json"},
				stdin:     square + "\n" + empty + "\n",
				wantCode:  ExitOK,
				wantLines: 2,
			},
			{
				name:      "should write an input error line for a missing file and go on",
				args:      []string{"-i", "jsonl", "--image-field", "photo", "q", "-o", "json"},
				stdin:     `{"photo":` + jsonText(t, filepath.Join(dir, "nope.png")) + `}` + "\n" + `{"photo":` + jsonText(t, square) + `}` + "\n",
				wantCode:  ExitRecords,
				wantLines: 2,
				wantOut:   []string{`"error":{"kind":"input"`, `--image-field photo: reading`},
			},
			{
				name:      "should read the field from a csv row",
				args:      []string{"-i", "csv", "--image-field", "photo", "q", "-o", "json"},
				stdin:     "id,photo\n1," + square + "\n2," + empty + "\n",
				wantCode:  ExitOK,
				wantLines: 2,
			},
			{
				name:      "should add a fixed --image to every record",
				args:      []string{"-i", "lines", "--image", "known=" + square, "does `text` describe `known`", "-o", "json"},
				stdin:     "a red square\nnothing at all\n",
				wantCode:  ExitOK,
				wantLines: 2,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				run := runImaged(t, tc.args, tc.stdin, noulAnswer("answer"), nil)

				if run.code != tc.wantCode {
					t.Fatalf("exit code = %d, want %d\nstderr:\n%s", run.code, tc.wantCode, run.stderr)
				}

				if lines := strings.Count(strings.TrimSpace(run.stdout), "\n") + 1; lines != tc.wantLines {
					t.Errorf("lines = %d, want %d\nstdout:\n%s", lines, tc.wantLines, run.stdout)
				}

				for _, want := range tc.wantOut {
					if !strings.Contains(run.stdout, want) {
						t.Errorf("stdout = %q, want it to contain %q", run.stdout, want)
					}
				}

				for i, body := range run.bodies {
					if !strings.Contains(body, `"assets":{"image-`) {
						t.Errorf("body %d = %.200s, want an image asset", i, body)
					}
				}
			})
		}
	})

	t.Run("should keep the stdout, stderr, exit code and --out file of each pinned run", func(t *testing.T) {
		t.Parallel()

		checkPinnedRuns(t, pinnedStreamRuns())
	})

	t.Run("should resume a run from its --out file", func(t *testing.T) {
		t.Parallel()

		printed := func(input, idSource, output, mergeKey string) string {
			return fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
				Provider: "typesafe", Model: onesie.TypeSafe().DefaultModel(), IDSource: idSource, Output: output,
				Input: input, MergeKey: mergeKey,
			})
		}

		byID := printed("jsonl", ".id", "values", "")
		byPosition := printed("jsonl", "", "values", "")
		byItself := printed("jsonl", ".", "values", "answers")
		byItselfInLines := printed("lines", ".", "values", "answers")
		byPositionInLines := printed("lines", "", "values", "")
		byPositionInLinesSkippingBlanks := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
			Provider: "typesafe", Model: onesie.TypeSafe().DefaultModel(), Output: "values", Input: "lines", SkipBlank: true,
		})
		byStateKey := printed("jsonl", ".state", "values", "answers")
		byIDInTSV := printed("jsonl", ".id", "tsv", "")
		byIDInCSV := printed("jsonl", ".id", "csv", "")
		byIDMergedInCSV := printed("csv", ".id", "csv", "answers")
		abstaining := answersfile.FingerprintInputs{
			Provider: "typesafe", Model: onesie.TypeSafe().DefaultModel(), IDSource: ".id", Output: "values", Input: "jsonl",
			Assert: "answer.value > 0.9", AbstainIf: "answer.value > 0.4",
		}
		byIDAbstaining := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, abstaining)
		gateArgs := []string{"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4"}
		abstaining.AbstainIf = "answer.value > 0.6"
		byIDRejecting := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, abstaining)

		values := []string{"-i", "jsonl", "-o", "values", "--id", ".id", "--resume"}
		csvCR := "id,answer,error\n,,\"line 1: --id: 'a\\rb' holds a carriage return, which -o csv cannot read back\"\n" +
			"\"a\nb\",0.5,\n"
		dupError := `{"error":{"kind":"input","status":null,"message":"line 2: --id: '1' is also the id of line 1"}}`

		tests := []resumeCase{
			{
				name:     "should skip the answered ids with no request and ask the rest",
				existing: fileOf(idLines(1, 2)),
				sidecar:  byID,
				stdin:    idRecords(1, 4),
				runs: []resumeRun{{
					args:     values,
					wantFile: idLines(1, 4),
					wantSent: []string{`{"id":3}`, `{"id":4}`},
				}},
			},
			{
				name:     "should ask again a record whose last line was an error",
				existing: fileOf("{\"id\":1,\"answer\":0.5}\n{\"id\":2,\"error\":{\"kind\":\"http\",\"status\":500,\"message\":\"boom\"}}\n"),
				sidecar:  byID,
				stdin:    idRecords(1, 3),
				runs: []resumeRun{{
					args:     values,
					wantFile: idLines(1, 3),
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				}},
			},
			{
				name: "should keep the newest answer for an id and keep an id no longer in the input after the input's records",
				existing: fileOf("{\"id\":2,\"answer\":0.1}\n{\"id\":8,\"answer\":0.5}\n{\"id\":1,\"answer\":0.5}\n" +
					"{\"id\":9,\"answer\":0.5}\n{\"id\":2,\"answer\":0.9}\n"),
				sidecar: byID,
				stdin:   idRecords(1, 3),
				runs: []resumeRun{{
					args: values,
					wantFile: "{\"id\":1,\"answer\":0.5}\n{\"id\":2,\"answer\":0.9}\n{\"id\":3,\"answer\":0.5}\n" +
						"{\"id\":8,\"answer\":0.5}\n{\"id\":9,\"answer\":0.5}\n",
					wantSent: []string{`{"id":3}`},
				}},
			},
			{
				name: "should drop an id no longer in the input under --prune",
				existing: fileOf("{\"id\":2,\"answer\":0.1}\n{\"id\":1,\"answer\":0.5}\n{\"id\":9,\"answer\":0.5}\n" +
					"{\"id\":2,\"answer\":0.9}\n"),
				sidecar: byID,
				stdin:   idRecords(1, 3),
				runs: []resumeRun{{
					args:     append([]string{"--prune"}, values...),
					wantFile: "{\"id\":1,\"answer\":0.5}\n{\"id\":2,\"answer\":0.9}\n{\"id\":3,\"answer\":0.5}\n",
					wantSent: []string{`{"id":3}`},
				}},
			},
			{
				name:     "should keep every answer when the input is cut short",
				existing: fileOf(idLines(1, 5)),
				sidecar:  byID,
				stdin:    idRecords(1, 2),
				runs: []resumeRun{{
					args:     values,
					wantFile: idLines(1, 5),
				}},
			},
			{
				name:     "should keep every answer in file order when the input is empty",
				existing: fileOf(idLines(3, 3) + "{\"id\":4,\"error\":{\"kind\":\"http\",\"status\":500,\"message\":\"boom\"}}\n" + idLines(1, 2)),
				sidecar:  byID,
				runs: []resumeRun{{
					args:     values,
					wantFile: idLines(3, 3) + idLines(1, 2),
				}},
			},
			{
				name:     "should keep only the input's records when a cut short input runs under --prune",
				existing: fileOf(idLines(1, 5)),
				sidecar:  byID,
				stdin:    idRecords(1, 2),
				runs: []resumeRun{{
					args:     append([]string{"--prune"}, values...),
					wantFile: idLines(1, 2),
				}},
			},
			{
				name:     "should empty the file when the input is empty under --prune",
				existing: fileOf(idLines(1, 3)),
				sidecar:  byID,
				runs: []resumeRun{{
					args:     append([]string{"--prune"}, values...),
					wantFile: "",
				}},
			},
			{
				name:     "should keep the line of a record with no id in its input place",
				existing: fileOf(idLines(1, 1)),
				sidecar:  byID,
				stdin:    "{\"id\":1}\nnot json\n{\"body\":\"x\"}\n{\"id\":2}\n",
				runs: []resumeRun{{
					args:     values,
					wantCode: ExitRecords,
					wantFile: idLines(1, 1) +
						`{"error":{"kind":"input","status":null,"message":"line 2: line is not one complete JSON value: invalid character 'o' in literal null (expecting 'u')"}}` + "\n" +
						`{"error":{"kind":"input","status":null,"message":"line 3: --id: id must be a string or a finite number, got null"}}` + "\n" +
						idLines(2, 2),
					wantSent: []string{`{"id":2}`},
				}},
			},
			{
				name:     "should keep every appended answer when a run stops short and finish it on the next resume",
				existing: fileOf(idLines(4, 4)),
				sidecar:  byID,
				stdin:    idRecords(1, 5),
				runs: []resumeRun{
					{
						args:     values,
						failFrom: 3,
						wantCode: ExitAuth,
						wantFile: idLines(4, 4) + idLines(1, 2) +
							`{"id":3,"error":{"kind":"auth","status":401,"message":"401 bad key"}}` + "\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
					},
					{
						args:     values,
						wantFile: idLines(1, 5),
						wantSent: []string{`{"id":3}`, `{"id":5}`},
					},
				},
			},
			{
				name:      "should remove a compaction file an interrupted run left behind",
				existing:  fileOf(idLines(4, 4)),
				sidecar:   byID,
				stdin:     idRecords(1, 5),
				stalePart: true,
				runs: []resumeRun{{
					args:     values,
					failFrom: 3,
					wantCode: ExitAuth,
					wantFile: idLines(4, 4) + idLines(1, 2) +
						`{"id":3,"error":{"kind":"auth","status":401,"message":"401 bad key"}}` + "\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
				}},
			},
			{
				name:      "should remove a compaction file an interrupted run left behind when a fresh run writes the file",
				stdin:     idRecords(1, 1),
				stalePart: true,
				runs: []resumeRun{{
					args:     []string{"-i", "jsonl", "-o", "values"},
					wantFile: "{\"answer\":0.5}\n",
					wantSent: []string{`{"id":1}`},
				}},
			},
			{
				name:     "should refuse to resume while another run holds the file",
				existing: fileOf(idLines(1, 1)),
				sidecar:  byID,
				stdin:    idRecords(1, 2),
				held:     true,
				runs: []resumeRun{{
					args:       values,
					wantCode:   ExitUsage,
					wantFile:   idLines(1, 1),
					wantStderr: "is in use by another onesie run. Wait for it to finish, then run again",
				}},
			},
			{
				name:     "should refuse a fresh run while a resume holds the file",
				existing: fileOf(idLines(1, 1)),
				sidecar:  byID,
				stdin:    idRecords(1, 2),
				held:     true,
				runs: []resumeRun{{
					args:       []string{"-i", "jsonl", "-o", "values", "--id", ".id"},
					wantCode:   ExitUsage,
					wantFile:   idLines(1, 1),
					wantStderr: "is in use by another onesie run. Wait for it to finish, then run again",
				}},
			},
			{
				name:     "should refuse a resume through a link while another run holds its target",
				existing: fileOf(idLines(1, 1)),
				sidecar:  byID,
				stdin:    idRecords(1, 2),
				held:     true,
				link:     true,
				runs: []resumeRun{{
					args:       values,
					wantCode:   ExitUsage,
					wantFile:   idLines(1, 1),
					wantStderr: "is in use by another onesie run. Wait for it to finish, then run again",
				}},
			},
			{
				name:     "should resume through a link from the fingerprint beside its target",
				existing: fileOf(idLines(1, 1)),
				sidecar:  byID,
				stdin:    idRecords(1, 2),
				link:     true,
				runs: []resumeRun{{
					args:     values,
					wantFile: idLines(1, 2),
					wantSent: []string{`{"id":2}`},
				}},
			},
			{
				name:  "should compact through a link whose target does not exist yet and leave the link in place",
				stdin: idRecords(1, 2),
				link:  true,
				runs: []resumeRun{{
					args:     values,
					wantFile: idLines(1, 2),
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				}},
			},
			{
				name:     "should let go of the lock when a resume is refused",
				existing: fileOf(idLines(1, 1)),
				sidecar:  byPosition,
				stdin:    idRecords(1, 2),
				runs: []resumeRun{{
					args:       values,
					wantCode:   ExitUsage,
					wantFile:   idLines(1, 1),
					wantStderr: "changed since",
				}},
			},
			{
				name:     "should leave the appended answers uncompacted when the run is cancelled and finish on the next resume",
				existing: fileOf(idLines(4, 4)),
				sidecar:  byID,
				stdin:    idRecords(1, 5),
				runs: []resumeRun{
					{
						args:     values,
						cancelAt: 3,
						wantCode: ExitInterrupt,
						wantFile: idLines(4, 4) + idLines(1, 2),
						wantSent: []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
					},
					{
						args:     values,
						wantFile: idLines(1, 5),
						wantSent: []string{`{"id":3}`, `{"id":5}`},
					},
				},
			},
			{
				name:     "should leave the appended answers uncompacted when --stop-on-error ends the run and finish on the next resume",
				existing: fileOf(idLines(4, 4)),
				sidecar:  byID,
				stdin:    idRecords(1, 5),
				runs: []resumeRun{
					{
						args:       append([]string{"--stop-on-error", "--retries", "0"}, values...),
						failFrom:   3,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitUsage,
						wantFile: idLines(4, 4) + idLines(1, 2) +
							`{"id":3,"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
					},
					{
						args:     values,
						wantFile: idLines(1, 5),
						wantSent: []string{`{"id":3}`, `{"id":5}`},
					},
				},
			},
			{
				name:     "should resume tsv output with the header written once",
				existing: fileOf("id\tanswer\terror\n1\t0.5\t\n"),
				sidecar:  byIDInTSV,
				stdin:    idRecords(1, 3),
				runs: []resumeRun{{
					args:     []string{"-i", "jsonl", "-o", "tsv", "--id", ".id", "--resume"},
					wantFile: "id\tanswer\terror\n1\t0.5\t\n2\t0.5\t\n3\t0.5\t\n",
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				}},
			},
			{
				name:    "should compact an unordered run into input order",
				sidecar: byID,
				stdin:   idRecords(1, 6),
				runs: []resumeRun{{
					args:     append([]string{"--unordered", "-j", "6"}, values...),
					reversed: true,
					wantFile: idLines(1, 6),
					wantSent: []string{`{"id":1}`, `{"id":2}`, `{"id":3}`, `{"id":4}`, `{"id":5}`, `{"id":6}`},
					anyOrder: true,
				}},
			},
			{
				name:     "should report the skipped records under --stats",
				existing: fileOf(idLines(1, 2)),
				sidecar:  byID,
				stdin:    idRecords(1, 3),
				runs: []resumeRun{{
					args:       append([]string{"--stats"}, values...),
					wantFile:   idLines(1, 3),
					wantSent:   []string{`{"id":3}`},
					wantStderr: "3 records, 2 skipped by --resume, ",
				}},
			},
			{
				name:     "should resume csv output with the header written once",
				existing: fileOf("id,answer,error\n1,0.5,\n"),
				sidecar:  byIDInCSV,
				stdin:    idRecords(1, 3),
				runs: []resumeRun{{
					args:     []string{"-i", "jsonl", "-o", "csv", "--id", ".id", "--resume"},
					wantFile: "id,answer,error\n1,0.5,\n2,0.5,\n3,0.5,\n",
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				}},
			},
			{
				name:    "should write the header once when a fresh csv run finishes out of order",
				sidecar: byID,
				stdin:   idRecords(1, 4),
				runs: []resumeRun{{
					args:     []string{"-i", "jsonl", "-o", "csv", "--id", ".id", "--resume", "--unordered", "-j", "4"},
					reversed: true,
					wantFile: "id,answer,error\n1,0.5,\n2,0.5,\n3,0.5,\n4,0.5,\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`, `{"id":3}`, `{"id":4}`},
					anyOrder: true,
				}},
			},
			{
				name:     "should resume merged csv output by running --id on each row",
				existing: fileOf("id,body,answer,error\n1,\"a\nb\",0.5,\n"),
				sidecar:  byIDMergedInCSV,
				stdin:    "id,body\n1,\"a\nb\"\n2,c\n3,d\n",
				runs: []resumeRun{{
					args:     []string{"-i", "csv", "-o", "csv", "--merge", "--id", ".id", "--resume"},
					wantFile: "id,body,answer,error\n1,\"a\nb\",0.5,\n2,c,0.5,\n3,d,0.5,\n",
					wantSent: []string{`{"id":"2","body":"c"}`, `{"id":"3","body":"d"}`},
				}},
			},
			{
				name:    "should never take a duplicate's merged error line for the first record's answer",
				sidecar: byID,
				stdin:   "{\"id\":1}\n{\"id\":1}\n{\"id\":2}\n",
				runs: []resumeRun{
					{
						args:     []string{"-i", "jsonl", "-o", "values", "--merge", "--id", ".id", "--resume"},
						wantCode: ExitRecords,
						wantFile: "{\"id\":1,\"answers\":{\"answer\":0.5}}\n" +
							"{\"id\":1,\"answers\":" + dupError + "}\n" +
							"{\"id\":2,\"answers\":{\"answer\":0.5}}\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:     []string{"-i", "jsonl", "-o", "values", "--merge", "--id", ".id", "--resume"},
						wantCode: ExitRecords,
						wantFile: "{\"id\":1,\"answers\":{\"answer\":0.5}}\n" +
							"{\"id\":1,\"answers\":" + dupError + "}\n" +
							"{\"id\":2,\"answers\":{\"answer\":0.5}}\n",
					},
				},
			},
			{
				name:     "should resume merged -i lines output by running --id on the wrapper's state",
				existing: fileOf("{\"state\":\"a\",\"answers\":{\"line\":1,\"answer\":0.5}}\n"),
				sidecar:  byItselfInLines,
				stdin:    "a\nb\n",
				runs: []resumeRun{{
					args: []string{"-i", "lines", "-o", "values", "--merge", "--id", ".", "--resume"},
					wantFile: "{\"state\":\"a\",\"answers\":{\"line\":1,\"answer\":0.5}}\n" +
						"{\"state\":\"b\",\"answers\":{\"line\":2,\"answer\":0.5}}\n",
					wantSent: []string{`"b"`},
				}},
			},
			{
				name:     "should resume a merged file an older onesie wrote without line numbers",
				existing: fileOf("{\"state\":\"a\",\"answers\":{\"answer\":0.5}}\n"),
				sidecar:  byItselfInLines,
				stdin:    "a\nb\n",
				runs: []resumeRun{{
					args: []string{"-i", "lines", "-o", "values", "--merge", "--id", ".", "--resume"},
					wantFile: "{\"state\":\"a\",\"answers\":{\"answer\":0.5}}\n" +
						"{\"state\":\"b\",\"answers\":{\"line\":2,\"answer\":0.5}}\n",
					wantSent: []string{`"b"`},
				}},
			},
			{
				name:     "should resume merged jsonl strings by running --id on the wrapper's state",
				existing: fileOf("{\"state\":\"a\",\"answers\":{\"answer\":0.5}}\n"),
				sidecar:  byItself,
				stdin:    "\"a\"\n\"b\"\n",
				runs: []resumeRun{{
					args: []string{"-i", "jsonl", "-o", "values", "--merge", "--id", ".", "--resume"},
					wantFile: "{\"state\":\"a\",\"answers\":{\"answer\":0.5}}\n" +
						"{\"state\":\"b\",\"answers\":{\"answer\":0.5}}\n",
					wantSent: []string{`"b"`},
				}},
			},
			{
				name:     "should resume a merged object whose only key is state by running --id on the whole line",
				existing: fileOf("{\"state\":5,\"answers\":{\"answer\":0.5}}\n"),
				sidecar:  byStateKey,
				stdin:    "{\"state\":5}\n{\"state\":6}\n",
				runs: []resumeRun{{
					args: []string{"-i", "jsonl", "-o", "values", "--merge", "--id", ".state", "--resume"},
					wantFile: "{\"state\":5,\"answers\":{\"answer\":0.5}}\n" +
						"{\"state\":6,\"answers\":{\"answer\":0.5}}\n",
					wantSent: []string{`{"state":6}`},
				}},
			},
			{
				name:    "should refuse a tsv id holding a tab, so it never reads back as another record's id",
				sidecar: byID,
				runs: []resumeRun{
					{
						args:     []string{"-i", "jsonl", "-o", "tsv", "--id", ".id", "--resume"},
						input:    "{\"id\":\"a\\tb\"}\n",
						wantCode: ExitRecords,
						wantFile: "id\tanswer\terror\n\t\tline 1: --id: 'a\\tb' holds a tab, carriage return or newline, which -o tsv cannot write\n",
					},
					{
						args:     []string{"-i", "jsonl", "-o", "tsv", "--id", ".id", "--resume"},
						input:    "{\"id\":\"a\\tb\"}\n{\"id\":\"a b\"}\n",
						wantCode: ExitRecords,
						wantFile: "id\tanswer\terror\n\t\tline 1: --id: 'a\\tb' holds a tab, carriage return or newline, which -o tsv cannot write\n" + "a b\t0.5\t\n",
						wantSent: []string{`{"id":"a b"}`},
					},
				},
			},
			{
				name:    "should refuse a tsv id holding a carriage return or a newline",
				sidecar: byID,
				stdin:   "{\"id\":\"a\\rb\"}\n{\"id\":\"a\\nb\"}\n",
				runs: []resumeRun{{
					args:     []string{"-i", "jsonl", "-o", "tsv", "--id", ".id", "--resume"},
					wantCode: ExitRecords,
					wantFile: "id\tanswer\terror\n\t\tline 1: --id: 'a\\rb' holds a tab, carriage return or newline, which -o tsv cannot write\n" +
						"\t\tline 2: --id: 'a\\nb' holds a tab, carriage return or newline, which -o tsv cannot write\n",
				}},
			},
			{
				name:    "should refuse a csv id holding a carriage return, which csv reads back without it",
				sidecar: byID,
				stdin:   "{\"id\":\"a\\rb\"}\n{\"id\":\"a\\nb\"}\n",
				runs: []resumeRun{
					{
						args:     []string{"-i", "jsonl", "-o", "csv", "--id", ".id", "--resume"},
						wantCode: ExitRecords,
						wantFile: csvCR,
						wantSent: []string{`{"id":"a\nb"}`},
					},
					{
						args:     []string{"-i", "jsonl", "-o", "csv", "--id", ".id", "--resume"},
						wantCode: ExitRecords,
						wantFile: csvCR,
					},
				},
			},
			{
				name:  "should exit 1 on a resume whose skipped records all failed their assertion",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:     append([]string{"--assert", "answer.value > 0.9"}, values...),
						wantCode: ExitRejected,
						wantFile: rejectedLines(1, 2),
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:       append([]string{"--assert", "answer.value > 0.9", "--stats"}, values...),
						wantCode:   ExitRejected,
						wantFile:   rejectedLines(1, 2),
						wantStderr: "2 records, 2 skipped by --resume, 2 false assertions, ",
					},
				},
			},
			{
				name:  "should exit 7 on a resume whose skipped records abstained",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:     append([]string{"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4"}, values...),
						wantCode: ExitAbstain,
						wantFile: abstainedLines(1, 2),
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args: append([]string{
							"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4", "--stats",
						}, values...),
						wantCode:   ExitAbstain,
						wantFile:   abstainedLines(1, 2),
						wantStderr: "2 records, 2 skipped by --resume, 2 abstains, ",
					},
				},
			},
			{
				name:  "should exit 0 on a resume whose skipped records passed their assertion",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:     append([]string{"--assert", "answer.value > 0.1"}, values...),
						wantFile: idLines(1, 2),
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:     append([]string{"--assert", "answer.value > 0.1"}, values...),
						wantFile: idLines(1, 2),
					},
				},
			},
			{
				name: "should exit 6 over a skipped false assertion when a record asked this run failed",
				runs: []resumeRun{
					{
						args:     append([]string{"--assert", "answer.value > 0.9"}, values...),
						input:    idRecords(1, 1),
						wantCode: ExitRejected,
						wantFile: rejectedLines(1, 1),
						wantSent: []string{`{"id":1}`},
					},
					{
						args:       append([]string{"--assert", "answer.value > 0.9", "--retries", "0"}, values...),
						input:      idRecords(1, 2),
						failFrom:   1,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitRecords,
						wantFile: rejectedLines(1, 1) +
							`{"id":2,"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n",
						wantSent: []string{`{"id":2}`},
					},
				},
			},
			{
				name:  "should exit 1 on a resume whose skipped merged records failed their assertion",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:     append([]string{"--assert", "answer.value > 0.9", "--merge"}, values...),
						wantCode: ExitRejected,
						wantFile: "{\"id\":1,\"answers\":{\"assert\":false,\"answer\":0.5}}\n" +
							"{\"id\":2,\"answers\":{\"assert\":false,\"answer\":0.5}}\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:     append([]string{"--assert", "answer.value > 0.9", "--merge"}, values...),
						wantCode: ExitRejected,
						wantFile: "{\"id\":1,\"answers\":{\"assert\":false,\"answer\":0.5}}\n" +
							"{\"id\":2,\"answers\":{\"assert\":false,\"answer\":0.5}}\n",
					},
				},
			},
			{
				name:  "should exit 1 on a csv resume whose skipped rows failed their assertion",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:     []string{"-i", "jsonl", "-o", "csv", "--id", ".id", "--resume", "--assert", "answer.value > 0.9"},
						wantCode: ExitRejected,
						wantFile: "id,answer,assert,error\n1,0.5,false,\n2,0.5,false,\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:     []string{"-i", "jsonl", "-o", "csv", "--id", ".id", "--resume", "--assert", "answer.value > 0.9"},
						wantCode: ExitRejected,
						wantFile: "id,answer,assert,error\n1,0.5,false,\n2,0.5,false,\n",
					},
				},
			},
			{
				name:  "should exit 7 on a tsv resume whose skipped rows abstained",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args: []string{
							"-i", "jsonl", "-o", "tsv", "--id", ".id", "--resume",
							"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4",
						},
						wantCode: ExitAbstain,
						wantFile: "id\tanswer\tassert\terror\n1\t0.5\tabstain\t\n2\t0.5\tabstain\t\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args: []string{
							"-i", "jsonl", "-o", "tsv", "--id", ".id", "--resume",
							"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4",
						},
						wantCode: ExitAbstain,
						wantFile: "id\tanswer\tassert\terror\n1\t0.5\tabstain\t\n2\t0.5\tabstain\t\n",
					},
				},
			},
			{
				name:  "should read an input column named assert as input, not an assertion, on a merged csv resume by id with no gate",
				stdin: "id,assert\n1,false\n2,true\n",
				runs: []resumeRun{
					{
						args:     []string{"-i", "csv", "-o", "csv", "--merge", "--id", ".id", "--resume"},
						input:    "id,assert\n1,false\n",
						wantFile: "id,assert,answer,error\n1,false,0.5,\n",
						wantSent: []string{`{"id":"1","assert":"false"}`},
					},
					{
						args:     []string{"-i", "csv", "-o", "csv", "--merge", "--id", ".id", "--resume"},
						wantFile: "id,assert,answer,error\n1,false,0.5,\n2,true,0.5,\n",
						wantSent: []string{`{"id":"2","assert":"true"}`},
					},
				},
			},
			{
				name:  "should read an input column named assert as input, not an abstain, on a merged csv resume by id with no gate",
				stdin: "id,assert\n1,abstain\n2,true\n",
				runs: []resumeRun{
					{
						args:     []string{"-i", "csv", "-o", "csv", "--merge", "--id", ".id", "--resume"},
						input:    "id,assert\n1,abstain\n",
						wantFile: "id,assert,answer,error\n1,abstain,0.5,\n",
						wantSent: []string{`{"id":"1","assert":"abstain"}`},
					},
					{
						args:     []string{"-i", "csv", "-o", "csv", "--merge", "--id", ".id", "--resume"},
						wantFile: "id,assert,answer,error\n1,abstain,0.5,\n2,true,0.5,\n",
						wantSent: []string{`{"id":"2","assert":"true"}`},
					},
				},
			},
			{
				name:     "should stop at a skipped false assertion under --stop-on-assert and ask nothing after it",
				existing: fileOf(rejectedLines(2, 2)),
				sidecar:  byIDAbstaining,
				stdin:    idRecords(1, 4),
				runs: []resumeRun{{
					args:     append(append([]string{"--stop-on-assert"}, gateArgs...), values...),
					wantCode: ExitRejected,
					wantFile: rejectedLines(2, 2) + abstainedLines(1, 1),
					wantSent: []string{`{"id":1}`},
				}},
			},
			{
				name:  "should read a skipped record's verdict under a --merge-key other than answers",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:     append([]string{"--assert", "answer.value > 0.9", "--merge-key", "verdict"}, values...),
						input:    idRecords(1, 1),
						wantCode: ExitRejected,
						wantFile: "{\"id\":1,\"verdict\":{\"assert\":false,\"answer\":0.5}}\n",
						wantSent: []string{`{"id":1}`},
					},
					{
						args:     append([]string{"--assert", "answer.value > 0.9", "--merge-key", "verdict", "--stats"}, values...),
						wantCode: ExitRejected,
						wantFile: "{\"id\":1,\"verdict\":{\"assert\":false,\"answer\":0.5}}\n" +
							"{\"id\":2,\"verdict\":{\"assert\":false,\"answer\":0.5}}\n",
						wantSent:   []string{`{"id":2}`},
						wantStderr: "1 skipped by --resume, 2 false assertions, ",
					},
				},
			},
			{
				name:  "should exit 1 on a merged csv resume whose skipped rows failed their assertion",
				stdin: "id,body\n1,a\n2,b\n",
				runs: []resumeRun{
					{
						args: []string{
							"-i", "csv", "-o", "csv", "--merge", "--id", ".id", "--resume", "--assert", "answer.value > 0.9",
						},
						input:    "id,body\n1,a\n",
						wantCode: ExitRejected,
						wantFile: "id,body,answer,assert,error\n1,a,0.5,false,\n",
						wantSent: []string{`{"id":"1","body":"a"}`},
					},
					{
						args: []string{
							"-i", "csv", "-o", "csv", "--merge", "--id", ".id", "--resume", "--assert", "answer.value > 0.9",
							"--stats",
						},
						wantCode:   ExitRejected,
						wantFile:   "id,body,answer,assert,error\n1,a,0.5,false,\n2,b,0.5,false,\n",
						wantSent:   []string{`{"id":"2","body":"b"}`},
						wantStderr: "1 skipped by --resume, 2 false assertions, ",
					},
				},
			},
			{
				name:     "should exit 1 over a skipped abstain when a record asked this run failed its assertion",
				existing: fileOf(abstainedLines(1, 1)),
				sidecar:  byIDRejecting,
				stdin:    idRecords(1, 2),
				runs: []resumeRun{{
					args: append([]string{
						"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.6", "--stats",
					}, values...),
					wantCode:   ExitRejected,
					wantFile:   abstainedLines(1, 1) + rejectedLines(2, 2),
					wantSent:   []string{`{"id":2}`},
					wantStderr: "1 skipped by --resume, 1 false assertion, 1 abstain, ",
				}},
			},
			{
				name:  "should ask a stored error line again under --id and --stop-on-error and stop as a fresh run does",
				stdin: idRecords(1, 3),
				runs: []resumeRun{
					{
						args:       append([]string{"--retries", "0"}, values...),
						input:      idRecords(1, 2),
						failFrom:   2,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitRecords,
						wantFile: "{\"id\":1,\"answer\":0.5}\n" +
							`{"id":2,"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:       append([]string{"--retries", "0", "--stop-on-error"}, values...),
						failFrom:   1,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitUsage,
						wantFile: "{\"id\":1,\"answer\":0.5}\n" +
							`{"id":2,"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n" +
							`{"id":2,"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n",
						wantSent:   []string{`{"id":2}`},
						wantStderr: "onesie: 400 bad key",
					},
				},
			},
			{
				name:     "should number the lines a resume by position asks by their input line",
				existing: fileOf("{\"line\":1,\"answer\":0.5}\n"),
				sidecar:  byPositionInLines,
				stdin:    "a\nb\n",
				runs: []resumeRun{{
					args:     []string{"-i", "lines", "-o", "values", "--resume"},
					wantFile: "{\"line\":1,\"answer\":0.5}\n{\"line\":2,\"answer\":0.5}\n",
					wantSent: []string{`"b"`},
				}},
			},
			{
				name:     "should count the blank lines --skip-blank drops in a resume by position",
				existing: fileOf("{\"line\":1,\"answer\":0.5}\n"),
				sidecar:  byPositionInLinesSkippingBlanks,
				stdin:    "a\n\n\nd\n",
				runs: []resumeRun{{
					args:     []string{"-i", "lines", "-o", "values", "--skip-blank", "--resume"},
					wantFile: "{\"line\":1,\"answer\":0.5}\n{\"line\":4,\"answer\":0.5}\n",
					wantSent: []string{`"d"`},
				}},
			},
			{
				name:     "should still resume by position without --id",
				existing: fileOf("old one\nold two\n"),
				sidecar:  byPosition,
				stdin:    idRecords(1, 4),
				runs: []resumeRun{{
					args:     []string{"-i", "jsonl", "-o", "values", "--resume"},
					wantFile: "old one\nold two\n" + strings.Repeat("{\"answer\":0.5}\n", 2),
					wantSent: []string{`{"id":3}`, `{"id":4}`},
				}},
			},
			{
				name:  "should refuse a resume by position into raw output under --assert before any request",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:     []string{"-i", "jsonl", "-o", "raw", "--assert", "answer.value > 0.9"},
						input:    idRecords(1, 1),
						wantCode: ExitRejected,
						wantFile: "0.5\n",
						wantSent: []string{`{"id":1}`},
					},
					{
						args:       []string{"-i", "jsonl", "-o", "raw", "--resume", "--assert", "answer.value > 0.9"},
						wantCode:   ExitUsage,
						wantFile:   "0.5\n",
						wantStderr: "which raw lines do not. Use -o values or -o json",
					},
				},
			},
			{
				name:  "should refuse a resume by position into raw output with no gate before any request",
				stdin: idRecords(1, 3),
				runs: []resumeRun{
					{
						args:       []string{"-i", "jsonl", "-o", "raw", "--retries", "0"},
						input:      idRecords(1, 2),
						failFrom:   2,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitRecords,
						wantFile:   "0.5\n\n",
						wantSent:   []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:     []string{"-i", "jsonl", "-o", "raw", "--resume"},
						wantCode: ExitUsage,
						wantFile: "0.5\n\n",
						wantStderr: "onesie: --resume needs output that keeps each record's outcome, " +
							"which raw lines do not. Use -o values or -o json",
					},
					{
						args:     []string{"-i", "jsonl", "-r", "--resume"},
						wantCode: ExitUsage,
						wantFile: "0.5\n\n",
						wantStderr: "onesie: --resume needs output that keeps each record's outcome, " +
							"which raw lines do not. Use -o values or -o json",
					},
				},
			},
			{
				name:     "should still refuse --unordered with a resume by position",
				existing: fileOf("old one\n"),
				sidecar:  byPosition,
				stdin:    idRecords(1, 4),
				runs: []resumeRun{{
					args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--unordered"},
					wantCode: ExitUsage,
					wantFile: "old one\n",
				}},
			},
		}

		runResumeCases(t, tests)
	})

	t.Run("should keep each skipped line's stored verdict on resume", func(t *testing.T) {
		t.Parallel()

		runResumeCases(t, []resumeCase{
			{
				name:  "should read a skipped line's verdict under a --merge-key other than answers",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args: []string{
							"-i", "jsonl", "-o", "values", "--resume", "--merge-key", "verdict",
							"--assert", "answer.value > 0.9",
						},
						input:    idRecords(1, 1),
						wantCode: ExitRejected,
						wantFile: "{\"id\":1,\"verdict\":{\"assert\":false,\"answer\":0.5}}\n",
						wantSent: []string{`{"id":1}`},
					},
					{
						args: []string{
							"-i", "jsonl", "-o", "values", "--resume", "--merge-key", "verdict",
							"--assert", "answer.value > 0.9", "--stats",
						},
						wantCode: ExitRejected,
						wantFile: "{\"id\":1,\"verdict\":{\"assert\":false,\"answer\":0.5}}\n" +
							"{\"id\":2,\"verdict\":{\"assert\":false,\"answer\":0.5}}\n",
						wantSent:   []string{`{"id":2}`},
						wantStderr: "1 skipped by --resume, 2 false assertions, ",
					},
				},
			},
			{
				name:  "should exit 1 on a merged csv resume by position whose skipped rows failed their assertion",
				stdin: "id,body\n1,a\n2,b\n",
				runs: []resumeRun{
					{
						args:     []string{"-i", "csv", "-o", "csv", "--merge", "--resume", "--assert", "answer.value > 0.9"},
						input:    "id,body\n1,a\n",
						wantCode: ExitRejected,
						wantFile: "id,body,answer,assert,error\n1,a,0.5,false,\n",
						wantSent: []string{`{"id":"1","body":"a"}`},
					},
					{
						args: []string{
							"-i", "csv", "-o", "csv", "--merge", "--resume", "--assert", "answer.value > 0.9", "--stats",
						},
						wantCode:   ExitRejected,
						wantFile:   "id,body,answer,assert,error\n1,a,0.5,false,\n2,b,0.5,false,\n",
						wantSent:   []string{`{"id":"2","body":"b"}`},
						wantStderr: "1 skipped by --resume, 2 false assertions, ",
					},
				},
			},
			{
				name:  "should read an input column named assert as input, not an assertion, on a merged csv resume by position with no gate",
				stdin: "id,assert\n1,false\n2,true\n",
				runs: []resumeRun{
					{
						args:     []string{"-i", "csv", "-o", "csv", "--merge", "--resume"},
						input:    "id,assert\n1,false\n",
						wantFile: "id,assert,answer,error\n1,false,0.5,\n",
						wantSent: []string{`{"id":"1","assert":"false"}`},
					},
					{
						args:     []string{"-i", "csv", "-o", "csv", "--merge", "--resume"},
						wantFile: "id,assert,answer,error\n1,false,0.5,\n2,true,0.5,\n",
						wantSent: []string{`{"id":"2","assert":"true"}`},
					},
				},
			},
			{
				name:  "should read an input column named assert as input, not an abstain, on a merged csv resume by position with no gate",
				stdin: "id,assert\n1,abstain\n2,true\n",
				runs: []resumeRun{
					{
						args:     []string{"-i", "csv", "-o", "csv", "--merge", "--resume"},
						input:    "id,assert\n1,abstain\n",
						wantFile: "id,assert,answer,error\n1,abstain,0.5,\n",
						wantSent: []string{`{"id":"1","assert":"abstain"}`},
					},
					{
						args:     []string{"-i", "csv", "-o", "csv", "--merge", "--resume"},
						wantFile: "id,assert,answer,error\n1,abstain,0.5,\n2,true,0.5,\n",
						wantSent: []string{`{"id":"2","assert":"true"}`},
					},
				},
			},
			{
				name:  "should exit 1 on a resume by position whose skipped merged records failed their assertion",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--merge", "--assert", "answer.value > 0.9"},
						wantCode: ExitRejected,
						wantFile: "{\"id\":1,\"answers\":{\"assert\":false,\"answer\":0.5}}\n" +
							"{\"id\":2,\"answers\":{\"assert\":false,\"answer\":0.5}}\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--merge", "--assert", "answer.value > 0.9"},
						wantCode: ExitRejected,
						wantFile: "{\"id\":1,\"answers\":{\"assert\":false,\"answer\":0.5}}\n" +
							"{\"id\":2,\"answers\":{\"assert\":false,\"answer\":0.5}}\n",
					},
				},
			},
			{
				name:  "should exit 1 on a csv resume by position whose skipped rows failed their assertion",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:     []string{"-i", "jsonl", "-o", "csv", "--resume", "--assert", "answer.value > 0.9"},
						wantCode: ExitRejected,
						wantFile: "answer,assert,error\n0.5,false,\n0.5,false,\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:     []string{"-i", "jsonl", "-o", "csv", "--resume", "--assert", "answer.value > 0.9"},
						wantCode: ExitRejected,
						wantFile: "answer,assert,error\n0.5,false,\n0.5,false,\n",
					},
				},
			},
			{
				name:  "should exit 6 on a resume by position whose skipped lines hold a false assertion and an error",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:       []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--assert", "answer.value > 0.9"},
						failFrom:   2,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitRecords,
						wantFile: "{\"assert\":false,\"answer\":0.5}\n" +
							`{"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args: []string{
							"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--assert", "answer.value > 0.9", "--stats",
						},
						wantCode: ExitRecords,
						wantFile: "{\"assert\":false,\"answer\":0.5}\n" +
							`{"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n",
						wantStderr: "2 skipped by --resume, 1 failed, 1 false assertion, ",
					},
				},
			},
			{
				name:  "should exit 6 on a resume by position with no gate whose skipped lines hold an error",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:       []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0"},
						failFrom:   2,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitRecords,
						wantFile: "{\"answer\":0.5}\n" +
							`{"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stats"},
						wantCode: ExitRecords,
						wantFile: "{\"answer\":0.5}\n" +
							`{"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n",
						wantStderr: "2 skipped by --resume, 1 failed, ",
					},
				},
			},
			{
				name:  "should exit 6 on a merged resume by position whose skipped lines hold an error under the merge key",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args: []string{
							"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--merge", "--merge-key", "verdict",
							"--assert", "answer.value > 0.9",
						},
						failFrom:   2,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitRecords,
						wantFile: "{\"id\":1,\"verdict\":{\"assert\":false,\"answer\":0.5}}\n" +
							`{"id":2,"verdict":{"error":{"kind":"http","status":400,"message":"400 bad key"}}}` + "\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args: []string{
							"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--merge", "--merge-key", "verdict",
							"--assert", "answer.value > 0.9", "--stats",
						},
						wantCode: ExitRecords,
						wantFile: "{\"id\":1,\"verdict\":{\"assert\":false,\"answer\":0.5}}\n" +
							`{"id":2,"verdict":{"error":{"kind":"http","status":400,"message":"400 bad key"}}}` + "\n",
						wantStderr: "2 skipped by --resume, 1 failed, 1 false assertion, ",
					},
				},
			},
			{
				name:  "should exit 6 on a csv resume by position whose skipped rows hold an error",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args:       []string{"-i", "jsonl", "-o", "csv", "--resume", "--retries", "0", "--assert", "answer.value > 0.9"},
						failFrom:   2,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitRecords,
						wantFile:   "answer,assert,error\n0.5,false,\n,,400 bad key\n",
						wantSent:   []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args: []string{
							"-i", "jsonl", "-o", "csv", "--resume", "--retries", "0", "--assert", "answer.value > 0.9", "--stats",
						},
						wantCode:   ExitRecords,
						wantFile:   "answer,assert,error\n0.5,false,\n,,400 bad key\n",
						wantStderr: "2 skipped by --resume, 1 failed, 1 false assertion, ",
					},
				},
			},
			{
				name:  "should exit 6 on a merged tsv resume by position with no gate whose skipped rows hold an error",
				stdin: "id\tbody\n1\ta\n2\tb\n",
				runs: []resumeRun{
					{
						args:       []string{"-i", "tsv", "-o", "tsv", "--merge", "--resume", "--retries", "0"},
						failFrom:   2,
						failStatus: http.StatusBadRequest,
						wantCode:   ExitRecords,
						wantFile:   "id\tbody\tanswer\terror\n1\ta\t0.5\t\n2\tb\t\t400 bad key\n",
						wantSent:   []string{`{"id":"1","body":"a"}`, `{"id":"2","body":"b"}`},
					},
					{
						args:       []string{"-i", "tsv", "-o", "tsv", "--merge", "--resume", "--retries", "0", "--stats"},
						wantCode:   ExitRecords,
						wantFile:   "id\tbody\tanswer\terror\n1\ta\t0.5\t\n2\tb\t\t400 bad key\n",
						wantStderr: "2 skipped by --resume, 1 failed, ",
					},
				},
			},
			{
				name:  "should exit 7 on a csv resume by position whose skipped rows abstained",
				stdin: idRecords(1, 2),
				runs: []resumeRun{
					{
						args: []string{
							"-i", "jsonl", "-o", "csv", "--resume",
							"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4",
						},
						wantCode: ExitAbstain,
						wantFile: "answer,assert,error\n0.5,abstain,\n0.5,abstain,\n",
						wantSent: []string{`{"id":1}`, `{"id":2}`},
					},
					{
						args: []string{
							"-i", "jsonl", "-o", "csv", "--resume",
							"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4",
						},
						wantCode: ExitAbstain,
						wantFile: "answer,assert,error\n0.5,abstain,\n0.5,abstain,\n",
					},
				},
			},
		})
	})
}

func newCountingStub(t *testing.T) *countingStub {
	t.Helper()

	stub := &countingStub{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		stub.requests.Add(1)

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
		withKeychain(noKeychain()),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
		WithClientFactory(stubFactory(base)),
	}

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, append(opts, extra...)...)

	root.Command.SetOut(&out)
	root.Command.SetErr(&errOut)
	root.Command.SetArgs(args)

	code := root.Execute(ctx)

	return out.String(), errOut.String(), code
}

func (r *handingLines) Read(p []byte) (int, error) {
	end := strings.IndexByte(r.data[r.offset:], '\n')
	if end < 0 {
		return 0, io.EOF
	}

	n := copy(p, r.data[r.offset:r.offset+end+1])
	r.offset += n
	r.lines++

	if r.lines == r.at {
		close(r.handed)
	}

	return n, nil
}

type handingLines struct {
	data   string
	offset int
	lines  int
	at     int
	handed chan struct{}
}

func (w closingPipe) Write([]byte) (int, error) {
	<-w.after

	return 0, io.ErrClosedPipe
}

type closingPipe struct {
	after <-chan struct{}
}
