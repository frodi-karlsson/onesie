package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

const (
	mockUsage = `{"input_tokens":0,"output_tokens":0}`
	mockAll   = `{"u":0.9,"t":"billing","r":"curt"}`
	// A line onesie could not read, which answers nothing.
	mockUnread = `{"error":{"kind":"input","status":null,"message":"x"}}`
	// The answers onesie prints for mockAll, as -o json writes them.
	mockAllJSON = `"u":{"value":0.9},"t":{"value":"billing","confidence":1,"p":{"billing":1,"platform":0}},` +
		`"r":{"value":"curt","score":1,"norm":0.5,"confidence":1,"p":{"calm":0,"curt":1,"rude":0}}`
)

func TestMockAnswers(t *testing.T) {
	t.Parallel()

	three := []string{
		"--ask", "u=is it urgent", "--ask", "t=which team", "--pick", "billing,platform",
		"--ask", "r=how rude", "--rate", "calm,curt,rude",
	}
	with := func(extra ...string) []string {
		return append(append([]string{}, three...), extra...)
	}
	lines := func(entries ...string) string {
		return strings.Join(entries, "\n") + "\n"
	}
	shellSafety := filepath.Join("..", "..", "examples", "questions", "shell-safety.yaml")
	fallback := []string{
		"--ask", "t=which team", "--pick", "billing,platform", "--min-confidence", "0.5", "--fallback", "billing",
	}

	tests := []struct {
		name     string
		args     []string
		stdin    string
		mock     string
		env      map[string]string
		wantCode int
		wantOut  *string
		contains []string
		absent   []string
		stderr   []string

		stderrAbsent []string
	}{
		{
			name:     "should answer one record from the object shape as -o json",
			args:     with("-o", "json", "--state", "x"),
			mock:     mockAll,
			wantOut:  fileOf(`{"model":"mock",` + mockAllJSON + "}\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should answer one record as -o values",
			args:     with("-o", "values", "--state", "x"),
			mock:     mockAll,
			wantOut:  fileOf(mockAll + "\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should name the model mock in a table",
			args:     with("-o", "table", "--state", "x"),
			mock:     mockAll,
			contains: []string{"mock", "billing", "curt"},
			wantCode: ExitOK,
		},
		{
			name:     "should print the bare value under -r",
			args:     []string{"is it urgent", "-r", "--state", "x"},
			mock:     `{"answer":0.25}`,
			wantOut:  fileOf("0.25\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should write every record of a stream as csv",
			args:     with("-o", "csv", "-i", "lines"),
			stdin:    "a\nb\n",
			mock:     lines(`{"u":0.1,"t":"billing","r":"calm"}`, `{"u":0.8,"t":"platform","r":"rude"}`),
			contains: []string{"u,t,r", "0.1,billing,calm", "0.8,platform,rude"},
			wantCode: ExitOK,
		},
		{
			name:     "should write every record of a stream as tsv",
			args:     with("-o", "tsv", "-i", "lines"),
			stdin:    "a\n",
			mock:     mockAll,
			contains: []string{"u\tt\tr", "0.9\tbilling\tcurt"},
			wantCode: ExitOK,
		},
		{
			name:     "should write every record of a stream as markdown",
			args:     with("-o", "markdown", "-i", "lines"),
			stdin:    "a\nb\n",
			mock:     mockAll,
			contains: []string{"| `u` | `t` | `r` | error |", "| 0.9 | `billing` | `curt` | |", "_`mock`_"},
			wantCode: ExitOK,
		},
		{
			name:     "should match a jsonl stream by position",
			args:     with("-o", "values", "-i", "jsonl"),
			stdin:    "{\"a\":1}\n{\"a\":2}\n",
			mock:     lines(`{"u":0.1,"t":"billing","r":"calm"}`, `{"u":0.8,"t":"platform","r":"rude"}`),
			wantOut:  fileOf(lines(`{"u":0.1,"t":"billing","r":"calm"}`, `{"u":0.8,"t":"platform","r":"rude"}`)),
			wantCode: ExitOK,
		},
		{
			name:  "should match a jsonl stream by id",
			args:  with("-o", "values", "-i", "jsonl", "--id", ".id"),
			stdin: "{\"id\":\"b\"}\n{\"id\":7}\n",
			mock:  lines(`{"id":7,"u":0.7,"t":"billing","r":"calm"}`, `{"id":"b","u":0.2,"t":"platform","r":"rude"}`),
			wantOut: fileOf(lines(`{"id":"b","u":0.2,"t":"platform","r":"rude"}`,
				`{"id":7,"u":0.7,"t":"billing","r":"calm"}`)),
			wantCode: ExitOK,
		},
		{
			name:     "should fold the answers into the record under --merge",
			args:     with("-o", "values", "-i", "jsonl", "--merge"),
			stdin:    "{\"a\":1}\n",
			mock:     mockAll,
			wantOut:  fileOf(`{"a":1,"answers":` + mockAll + "}\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should pass -q for a yes",
			args:     []string{"is it urgent", "-q", "--state", "x"},
			mock:     `{"answer":0.9}`,
			wantOut:  fileOf(""),
			wantCode: ExitOK,
		},
		{
			name:     "should fail -q for a no",
			args:     []string{"is it urgent", "-q", "--state", "x"},
			mock:     `{"answer":0.2}`,
			wantOut:  fileOf(""),
			wantCode: ExitRejected,
		},
		{
			name:     "should exit 1 for the shell-safety example",
			args:     []string{"-f", shellSafety, "-q", "--state", "rm -rf /"},
			mock:     `{"destroys": 0.93, "secrets": 0.02, "network": 0.1}`,
			wantOut:  fileOf(""),
			wantCode: ExitRejected,
		},
		{
			name:     "should exit 7 when the gate abstains",
			args:     []string{"-f", shellSafety, "-q", "--state", "find . -name '*.pyc' -delete"},
			mock:     `{"destroys": 0.4, "secrets": 0.02, "network": 0.1}`,
			wantCode: ExitAbstain,
		},
		{
			name:     "should exit 0 when the gate holds",
			args:     []string{"-f", shellSafety, "-q", "--state", "ls"},
			mock:     `{"destroys": 0.01, "secrets": 0.02, "network": 0.1}`,
			wantCode: ExitOK,
		},
		{
			name:     "should judge --assert and write the record",
			args:     with("-o", "json", "--assert", "u.value < 0.5", "--state", "x"),
			mock:     mockAll,
			wantOut:  fileOf(`{"assert":false,"model":"mock",` + mockAllJSON + "}\n"),
			wantCode: ExitRejected,
		},
		{
			name: "should judge --abstain-if",
			args: with("-o", "values", "--assert", "u.value < 0.5", "--abstain-if", "u.value < 0.95",
				"--state", "x"),
			mock:     mockAll,
			contains: []string{`"abstain":true`},
			wantCode: ExitAbstain,
		},
		{
			name:     "should report zero usage on every line",
			args:     with("-o", "json", "-i", "lines", "--usage"),
			stdin:    "a\nb\n",
			mock:     mockAll,
			wantOut:  fileOf(lines(`{"model":"mock","usage":`+mockUsage+`,`+mockAllJSON+`}`, `{"model":"mock","usage":`+mockUsage+`,`+mockAllJSON+`}`)),
			wantCode: ExitOK,
		},
		{
			name:     "should count one attempt per request and name the model mock under --stats",
			args:     with("-o", "values", "-i", "lines", "--stats"),
			stdin:    "a\nb\n",
			mock:     mockAll,
			stderr:   []string{"2 requests", "0 in / 0 out", "mock", "2 attempts"},
			wantCode: ExitOK,
		},
		{
			name:     "should exit 3 for one record the file refuses with 401",
			args:     with("--state", "x"),
			mock:     `{"error":401}`,
			stderr:   []string{"onesie: mock status 401"},
			wantCode: ExitAuth,
		},
		{
			name:     "should exit 4 for one record the file fails with 503",
			args:     with("--state", "x", "-o", "json"),
			mock:     `{"error":503}`,
			contains: []string{`"error":{"kind":"http","status":503`},
			wantCode: ExitUnavailable,
		},
		{
			name:     "should exit 5 for one record the file times out, after the run's timeout",
			args:     with("--state", "x", "--timeout", "4"),
			mock:     `{"error":"timeout"}`,
			stderr:   []string{"onesie: request timed out after 4s"},
			wantCode: ExitTransport,
		},
		{
			name:         "should give no credit advice for a mocked 402",
			args:         with("--state", "x"),
			mock:         `{"error":402}`,
			stderr:       []string{"onesie: mock status 402"},
			stderrAbsent: []string{"credits"},
			wantCode:     ExitAuth,
		},
		{
			name:     "should exit 6 for a stream with one 503 line",
			args:     with("-o", "values", "-i", "lines"),
			stdin:    "a\nb\nc\n",
			mock:     lines(mockAll, `{"error":503}`, mockAll),
			contains: []string{mockAll, `"error":{"kind":"http","status":503`},
			wantCode: ExitRecords,
		},
		{
			name:     "should abort a stream with exit 3 at a 401 line",
			args:     with("-o", "values", "-i", "lines"),
			stdin:    "a\nb\nc\n",
			mock:     lines(mockAll, `{"error":401}`, mockAll),
			stderr:   []string{"onesie: mock status 401"},
			wantCode: ExitAuth,
		},
		{
			name:    "should stop at the first record the file does not cover, naming its line",
			args:    with("-o", "values", "-i", "lines", "--skip-blank"),
			stdin:   "a\n\nb\nc\n",
			mock:    lines(mockAll, mockUnread),
			wantOut: fileOf(mockAll + "\n"),
			stderr: []string{"onesie: --mock line 2 replays a line onesie could not read, so input line 3 " +
				"has no answer. Give it answers"},
			wantCode: ExitUsage,
		},
		{
			name:     "should name the id of a record the file does not cover",
			args:     with("-o", "values", "-i", "jsonl", "--id", ".id"),
			stdin:    "{\"id\":\"a\"}\n{\"id\":\"b\"}\n",
			mock:     lines(`{"id":"a",` + mockAll[1:]),
			wantOut:  fileOf(`{"id":"a",` + mockAll[1:] + "\n"),
			stderr:   []string{"onesie: --mock has no line with id 'b', so input line 2 has no answer. Add one"},
			wantCode: ExitUsage,
		},
		{
			name:     "should write only the lines before an uncovered record under -j 4",
			args:     with("-o", "values", "-i", "lines", "-j", "4"),
			stdin:    "a\nb\nc\nd\ne\nf\ng\nh\n",
			mock:     lines(mockAll, mockAll, mockUnread, mockAll, mockAll, mockAll, mockAll, mockAll),
			wantOut:  fileOf(lines(mockAll, mockAll)),
			stderr:   []string{"line 3"},
			wantCode: ExitUsage,
		},
		{
			name:     "should count an uncovered record as a record and not a request",
			args:     with("-o", "values", "-i", "lines", "--stats"),
			stdin:    "a\nb\n",
			mock:     lines(mockAll, mockUnread),
			stderr:   []string{"2 records, 1 failed, 1 request", "1 attempt"},
			wantCode: ExitUsage,
		},
		{
			name:     "should write nothing for one record the file does not cover, fallback included",
			args:     append(fallback, "-o", "json", "--state", "x"),
			mock:     mockUnread,
			wantOut:  fileOf(""),
			stderr:   []string{"onesie: --mock replays a line onesie could not read, so input line 1 has no answer"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse a file that does not match the questions before writing a line",
			args:     with("-o", "values", "-i", "lines"),
			stdin:    "a\nb\n",
			mock:     lines(mockAll, `{"u":0.9,"t":"sales","r":"curt"}`),
			wantOut:  fileOf(""),
			stderr:   []string{"onesie: --mock line 2: question 't' picked 'sales', which is not an option. Options: billing, platform"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse a file that leaves a question out",
			args:     with("--state", "x"),
			mock:     `{"u":0.9,"t":"billing"}`,
			stderr:   []string{"onesie: --mock: question 'r' has no answer"},
			wantCode: ExitUsage,
		},
		{
			name:     "should ignore --provider, --base-url and -m",
			args:     with("-o", "json", "--state", "x", "--provider", "bogus", "--base-url", "http://example.com", "-m", "other"),
			mock:     mockAll,
			wantOut:  fileOf(`{"model":"mock",` + mockAllJSON + "}\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should ignore ONESIE_PROVIDER",
			args:     with("-o", "values", "--state", "x"),
			env:      map[string]string{"ONESIE_PROVIDER": "bogus"},
			mock:     mockAll,
			wantOut:  fileOf(mockAll + "\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should refuse --print-request",
			args:     with("--state", "x", "--print-request"),
			mock:     mockAll,
			stderr:   []string{"onesie: --mock answers requests, and --print-request sends none. Drop one"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse -i request",
			args:     []string{"-i", "request"},
			mock:     mockAll,
			stderr:   []string{"onesie: -i request sends each body as written, so --mock has nothing to answer. Drop one"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --list-models",
			args:     []string{"--list-models"},
			mock:     mockAll,
			stderr:   []string{"onesie: --list-models asks no question, so --mock has nothing to answer. Drop one"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse auth test under --mock",
			args:     []string{"auth", "test"},
			mock:     mockAll,
			stderr:   []string{"onesie: auth reads and writes a real key, so it does not run under --mock. Drop it"},
			wantCode: ExitUsage,
		},
		{
			name:     "should allow --print-questions",
			args:     with("--print-questions"),
			mock:     mockAll,
			contains: []string{"u:", "billing"},
			wantCode: ExitOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeMock(t, tc.mock)

			args := append(append([]string{}, tc.args...), "--mock", path)

			out, errOut, code := runMocked(t, t.Context(), args, tc.stdin, tc.env, nil)

			checkMocked(t, out, errOut, code, tc.wantCode, tc.wantOut, tc.contains, tc.absent, tc.stderr)

			for _, unwanted := range tc.stderrAbsent {
				if strings.Contains(errOut, unwanted) {
					t.Errorf("stderr holds %q\nstderr:\n%s", unwanted, errOut)
				}
			}
		})
	}
}

func TestMockUncovered(t *testing.T) {
	t.Parallel()

	eight := "a\nb\nc\nd\ne\nf\ng\nh\n"
	values := []string{"is it urgent", "-o", "values", "-i", "lines", "-j", "4"}
	yes := `{"answer":0.9}`
	answers := strings.Join([]string{yes, yes, mockUnread, yes, yes, yes, yes, yes}, "\n") + "\n"

	t.Run("should drop the records the engine flushes after a slow uncovered one", func(t *testing.T) {
		t.Parallel()

		out, errOut, code := runMocked(t, t.Context(), append(values, "--mock", writeMock(t, answers)), eight, nil, nil,
			delaying(3, 300*time.Millisecond))

		checkMocked(t, out, errOut, code, ExitUsage, fileOf(yes+"\n"+yes+"\n"), nil, nil, []string{"input line 3"})
	})

	t.Run("should keep the lines that arrived before an uncovered record under --unordered", func(t *testing.T) {
		t.Parallel()

		out, errOut, code := runMocked(t, t.Context(),
			append(values, "--unordered", "--mock", writeMock(t, answers)), eight, nil, nil,
			delaying(3, 300*time.Millisecond))

		checkMocked(t, out, errOut, code, ExitUsage, fileOf(strings.Repeat(yes+"\n", 7)), nil, nil,
			[]string{"input line 3"})
	})

	t.Run("should keep calibrate's answers file to the records before an uncovered one", func(t *testing.T) {
		t.Parallel()

		ids := []string{"a", "b", "c", "d", "e", "f"}

		var stdin, partial, full strings.Builder

		for _, id := range ids {
			fmt.Fprintf(&stdin, "{\"id\":%q,\"body\":\"x\",\"u\":true}\n", id)
			fmt.Fprintf(&full, "{\"id\":%q,\"u\":0.9}\n", id)

			if id != "c" {
				fmt.Fprintf(&partial, "{\"id\":%q,\"u\":0.9}\n", id)
			}
		}

		out := filepath.Join(t.TempDir(), "answers.jsonl")
		args := []string{
			"calibrate", "--ask", "u=is it urgent", "-i", "jsonl", "--map", ".body", "--id", ".id",
			"--label", "u=.u", "-j", "4", "--out", out, "--resume",
		}

		_, errOut, code := runMocked(t, t.Context(), append(args, "--mock", writeMock(t, partial.String())),
			stdin.String(), nil, nil, delaying(3, 300*time.Millisecond))
		if code != ExitUsage || !strings.Contains(errOut, "no line with id 'c'") {
			t.Fatalf("first run exit %d\n%s", code, errOut)
		}

		want := `{"id":"a","model":"mock","u":{"value":0.9}}` + "\n" + `{"id":"b","model":"mock","u":{"value":0.9}}` + "\n"
		if got := readText(t, out); got != want {
			t.Errorf("file = %q, want %q", got, want)
		}

		_, errOut, code = runMocked(t, t.Context(), append(args, "--mock", writeMock(t, full.String())),
			stdin.String(), nil, nil)
		if code != ExitOK || !strings.Contains(errOut, "asking 4 of 6 records") {
			t.Errorf("second run exit %d\n%s", code, errOut)
		}
	})
}

func TestMockReplay(t *testing.T) {
	t.Parallel()

	t.Run("should give a replayed gated line the verdict the real run gave", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			body := `{"model":"m","answers":{"r":{"type":"score","score":1.3,"confidence":0.8,` +
				`"legend":{"0":"calm","1":"curt","2":"rude"},"probabilities":{"0":0.1,"1":0.5,"2":0.4}}},"usage":{}}`
			if _, err := io.WriteString(w, body); err != nil {
				t.Errorf("writing the stub response: %v", err)
			}
		}))
		t.Cleanup(srv.Close)

		args := []string{
			"--ask", "r=how rude", "--rate", "calm,curt,rude", "-i", "jsonl", "-o", "json",
			"--assert", "r.p.rude < 0.3",
		}
		stdin := "{\"a\":1}\n"
		out := filepath.Join(t.TempDir(), "answers.jsonl")

		if _, errOut, code := runReal(t, append(args, "--out", out), stdin, srv.URL); code != ExitRejected {
			t.Fatalf("real run exit %d, want 1\n%s", code, errOut)
		}

		replay, errOut, code := runMocked(t, t.Context(), append(args, "--mock", out), stdin, nil, nil)
		if code != ExitRejected {
			t.Errorf("replay exit %d, want 1\n%s\n%s", code, replay, errOut)
		}

		for _, want := range []string{`"score":1.3`, `"norm":0.65`, `"p":{"calm":0.1,"curt":0.5,"rude":0.4}`} {
			if !strings.Contains(replay, want) {
				t.Errorf("replay missing %s\n%s", want, replay)
			}
		}
	})
}

func TestMockSource(t *testing.T) {
	t.Parallel()

	answer := []string{"is it urgent", "-o", "values", "--state", "x"}

	tests := []struct {
		name     string
		args     []string
		env      func(path string) map[string]string
		flag     bool
		wantCode int
		wantOut  *string
		stderr   []string
	}{
		{
			name:     "should answer from ONESIE_MOCK with the command unchanged",
			args:     answer,
			env:      func(path string) map[string]string { return map[string]string{envMock: path} },
			wantOut:  fileOf(`{"answer":0.9}` + "\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should let --mock beat ONESIE_MOCK",
			args:     answer,
			env:      func(string) map[string]string { return map[string]string{envMock: "/nonexistent/answers.json"} },
			flag:     true,
			wantOut:  fileOf(`{"answer":0.9}` + "\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should read an empty ONESIE_MOCK as unset",
			args:     []string{"is it urgent", "--state", "x", "--print-request"},
			env:      func(string) map[string]string { return map[string]string{envMock: ""} },
			wantCode: ExitOK,
		},
		{
			name:     "should name ONESIE_MOCK in a file error",
			args:     []string{"--ask", "u=is it urgent", "--state", "x"},
			env:      func(path string) map[string]string { return map[string]string{envMock: path} },
			stderr:   []string{"onesie: ONESIE_MOCK: 'answer' is not a question. Questions: u"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --print-request under ONESIE_MOCK",
			args:     []string{"is it urgent", "--state", "x", "--print-request"},
			env:      func(path string) map[string]string { return map[string]string{envMock: path} },
			stderr:   []string{"onesie: ONESIE_MOCK answers requests, and --print-request sends none. Unset it or drop --print-request"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse -i request under ONESIE_MOCK",
			args:     []string{"-i", "request"},
			env:      func(path string) map[string]string { return map[string]string{envMock: path} },
			stderr:   []string{"onesie: -i request sends each body as written, so ONESIE_MOCK has nothing to answer. Unset it or drop -i request"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse --list-models under ONESIE_MOCK",
			args:     []string{"--list-models"},
			env:      func(path string) map[string]string { return map[string]string{envMock: path} },
			stderr:   []string{"onesie: --list-models asks no question, so ONESIE_MOCK has nothing to answer. Unset it or drop --list-models"},
			wantCode: ExitUsage,
		},
		{
			name: "should refuse calibrate --print-request under ONESIE_MOCK",
			args: []string{
				"calibrate", "is it urgent", "-i", "jsonl", "--map", ".body", "--label", ".u", "--print-request",
			},
			env: func(path string) map[string]string { return map[string]string{envMock: path} },
			stderr: []string{"onesie: ONESIE_MOCK answers requests, and --print-request sends none. " +
				"Unset it or drop --print-request"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse auth status under ONESIE_MOCK",
			args:     []string{"auth", "status"},
			env:      func(path string) map[string]string { return map[string]string{envMock: path} },
			stderr:   []string{"onesie: auth reads and writes a real key, so it does not run under ONESIE_MOCK. Unset it"},
			wantCode: ExitUsage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeMock(t, `{"answer":0.9}`)

			args := tc.args
			if tc.flag {
				args = append(append([]string{}, args...), "--mock", path)
			}

			out, errOut, code := runMocked(t, t.Context(), args, "", tc.env(path), nil)

			checkMocked(t, out, errOut, code, tc.wantCode, tc.wantOut, nil, nil, tc.stderr)
		})
	}
}

func TestMockResume(t *testing.T) {
	t.Parallel()

	question := []string{"is it urgent", "-o", "json", "-i", "jsonl", "--id", ".id"}
	input := "{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n"
	full := "{\"id\":1,\"answer\":0.1}\n{\"id\":2,\"answer\":0.2}\n{\"id\":3,\"answer\":0.3}\n"
	wantFile := `{"id":1,"model":"mock","answer":{"value":0.1}}` + "\n" +
		`{"id":2,"model":"mock","answer":{"value":0.2}}` + "\n" +
		`{"id":3,"model":"mock","answer":{"value":0.3}}` + "\n"

	t.Run("should keep the lines before an uncovered record and ask the rest on resume", func(t *testing.T) {
		t.Parallel()

		out := filepath.Join(t.TempDir(), "answers.jsonl")
		partial := writeMock(t, "{\"id\":1,\"answer\":0.1}\n{\"id\":3,\"answer\":0.3}\n")
		args := append(append([]string{}, question...), "--out", out, "--resume")

		_, errOut, code := runMocked(t, t.Context(), append(args, "--mock", partial), input, nil, nil)
		if code != ExitUsage {
			t.Fatalf("first run exit %d, want 2\n%s", code, errOut)
		}

		if got := readText(t, out); got != `{"id":1,"model":"mock","answer":{"value":0.1}}`+"\n" {
			t.Errorf("after the first run the file holds %q", got)
		}

		_, errOut, code = runMocked(t, t.Context(), append(args, "--mock", writeMock(t, full), "--stats"), input, nil, nil)
		if code != ExitOK {
			t.Fatalf("second run exit %d, want 0\n%s", code, errOut)
		}

		if !strings.Contains(errOut, "1 skipped") || !strings.Contains(errOut, "2 requests") {
			t.Errorf("the second run did not ask exactly the last two records: %s", errOut)
		}

		if got := readText(t, out); got != wantFile {
			t.Errorf("file = %q, want %q", got, wantFile)
		}
	})

	t.Run("should carry on after an interrupt", func(t *testing.T) {
		t.Parallel()

		out := filepath.Join(t.TempDir(), "answers.jsonl")
		mockFile := writeMock(t, full)
		args := append(append([]string{}, question...), "--out", out, "--resume", "--mock", mockFile)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		interrupting := &cancellingReader{data: input, after: 2, cancel: cancel, ready: func() bool {
			data, err := os.ReadFile(out)

			return err == nil && bytes.Count(data, []byte("\n")) >= 1
		}}

		_, errOut, code := runMocked(t, ctx, args, "", nil, interrupting)
		if code != ExitInterrupt {
			t.Fatalf("first run exit %d, want 130\n%s", code, errOut)
		}

		if got := readText(t, out); !strings.HasPrefix(wantFile, got) {
			t.Errorf("the interrupted file %q is not a prefix of %q", got, wantFile)
		}

		_, errOut, code = runMocked(t, t.Context(), append(args, "--stats"), input, nil, nil)
		if code != ExitOK {
			t.Fatalf("second run exit %d, want 0\n%s", code, errOut)
		}

		if !strings.Contains(errOut, " skipped") {
			t.Errorf("the second run skipped no record: %s", errOut)
		}

		if got := readText(t, out); got != wantFile {
			t.Errorf("file = %q, want %q", got, wantFile)
		}
	})

	for _, tc := range []struct {
		name        string
		firstMocked bool
	}{
		{name: "should refuse to resume a mock run's file with a real run", firstMocked: true},
		{name: "should refuse to resume a real run's file with a mock run", firstMocked: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if _, err := io.WriteString(w, `{"model":"m","answers":{"answer":{"type":"noul","noul":0.5}},"usage":{}}`); err != nil {
					t.Errorf("writing the stub response: %v", err)
				}
			}))
			t.Cleanup(srv.Close)

			out := filepath.Join(t.TempDir(), "answers.jsonl")
			args := append(append([]string{}, question...), "--out", out, "--resume")
			mocked := append(append([]string{}, args...), "--mock", writeMock(t, full))

			first, second := mocked, args
			if !tc.firstMocked {
				first, second = args, mocked
			}

			if _, errOut, code := runReal(t, first, input, srv.URL); code != ExitOK {
				t.Fatalf("first run exit %d\n%s", code, errOut)
			}

			_, errOut, code := runReal(t, second, input, srv.URL)
			if code != ExitUsage || !strings.Contains(errOut, "changed since") {
				t.Errorf("second run exit %d, want 2 with the changed message\n%s", code, errOut)
			}
		})
	}
}

func TestCalibrateMock(t *testing.T) {
	t.Parallel()

	t.Run("should print a report from the file with no request made", func(t *testing.T) {
		t.Parallel()

		mockFile := writeMock(t, "{\"id\":\"a\",\"u\":0.9}\n{\"id\":\"b\",\"u\":0.1}\n{\"id\":\"c\",\"u\":0.8}\n")
		args := []string{
			"calibrate", "--ask", "u=is it urgent", "-i", "jsonl", "--map", ".body", "--id", ".id",
			"--label", "u=.u", "-o", "json", "--mock", mockFile,
		}
		stdin := "{\"id\":\"a\",\"body\":\"x\",\"u\":true}\n{\"id\":\"b\",\"body\":\"y\",\"u\":false}\n" +
			"{\"id\":\"c\",\"body\":\"z\",\"u\":false}\n"

		out, errOut, code := runMocked(t, t.Context(), args, stdin, nil, nil)
		if code != ExitOK {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}

		for _, want := range []string{`"models":["mock"]`, `"labelled":3`} {
			if !strings.Contains(out, want) {
				t.Errorf("report missing %s\n%s", want, out)
			}
		}
	})

	t.Run("should refuse calibrate --print-request under --mock", func(t *testing.T) {
		t.Parallel()

		args := []string{
			"calibrate", "--ask", "u=is it urgent", "-i", "jsonl", "--map", ".body", "--label", "u=.u",
			"--print-request", "--mock", writeMock(t, `{"u":0.9}`),
		}

		_, errOut, code := runMocked(t, t.Context(), args, "{\"body\":\"x\",\"u\":true}\n", nil, nil)
		if code != ExitUsage || !strings.Contains(errOut, "--mock answers requests, and --print-request sends none") {
			t.Errorf("exit %d\n%s", code, errOut)
		}
	})
}

func runMocked(
	t *testing.T, ctx context.Context, args []string, stdin string, env map[string]string, reader io.Reader,
	extra ...RootOption,
) (string, string, int) {
	t.Helper()

	lookup := map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()}
	for name, value := range env {
		lookup[name] = value
	}

	if reader == nil {
		reader = strings.NewReader(stdin)
	}

	var out, errOut bytes.Buffer

	opts := []RootOption{
		WithKeychain(noKeychain()),
		WithStdin(reader),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(lookup)),
		WithClientFactory(func(context.Context, ...jev.Option) (*jev.Client, error) {
			t.Error("a mock run built an API client")

			return nil, os.ErrInvalid
		}),
	}

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, append(opts, extra...)...)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(ctx, root)

	return out.String(), errOut.String(), code
}

func runReal(t *testing.T, args []string, stdin, base string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithKeychain(noKeychain()),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
		WithClientFactory(stubFactory(base)),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}

func checkMocked(
	t *testing.T, out, errOut string, code, wantCode int, wantOut *string, contains, absent, stderr []string,
) {
	t.Helper()

	if code != wantCode {
		t.Errorf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, wantCode, out, errOut)
	}

	if wantOut != nil && out != *wantOut {
		t.Errorf("stdout\n got: %q\nwant: %q\nstderr:\n%s", out, *wantOut, errOut)
	}

	for _, want := range contains {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\nstdout:\n%s", want, out)
		}
	}

	for _, unwanted := range absent {
		if strings.Contains(out, unwanted) {
			t.Errorf("stdout holds %q\nstdout:\n%s", unwanted, out)
		}
	}

	for _, want := range stderr {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr missing %q\nstderr:\n%s", want, errOut)
		}
	}
}

func writeMock(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "answers.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing the mock file: %v", err)
	}

	return path
}

func readText(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(data)
}

func (r *cancellingReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.lines == r.after {
		// Held until a line is on disk, so the run the interrupt ends has something to resume from.
		for deadline := time.Now().Add(5 * time.Second); !r.ready() && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}

		r.cancel()

		return 0, context.Canceled
	}

	end := strings.IndexByte(r.data[r.offset:], '\n')
	if end < 0 {
		return 0, io.EOF
	}

	n := copy(p, r.data[r.offset:r.offset+end+1])
	r.offset += n
	r.lines++

	return n, nil
}

type cancellingReader struct {
	mu     sync.Mutex
	data   string
	offset int
	lines  int
	after  int
	cancel context.CancelFunc
	ready  func() bool
}

func delaying(position int, delay time.Duration) RootOption {
	return func(s *rootSettings) {
		s.wrapAnswerer = func(inner answerer) answerer {
			return delayedAnswerer{inner: inner, position: position, delay: delay}
		}
	}
}

func (d delayedAnswerer) answer(ctx context.Context, key recordKey, req jev.Request) (*jev.Result, error) {
	if key.position == d.position {
		select {
		case <-time.After(d.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	return d.inner.answer(ctx, key, req)
}

func (d delayedAnswerer) salt(key recordKey) (string, bool) {
	return d.inner.salt(key)
}

type delayedAnswerer struct {
	inner    answerer
	position int
	delay    time.Duration
}
