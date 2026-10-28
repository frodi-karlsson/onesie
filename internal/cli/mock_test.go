package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/mock"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	mockUsage   = `{"input_tokens":0,"output_tokens":0}`
	mockAll     = `{"u":0.9,"t":"billing","r":"curt"}`
	mockUnread  = `{"error":{"kind":"input","status":null,"message":"x"}}`
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
	threeFile := writeCalibrateFile(t, t.TempDir(), "three.yaml", threeQuestions)
	calibrating := func(labels ...string) []string {
		return append([]string{
			"calibrate", "-f", threeFile, "-i", "jsonl", "--map", ".body", "--id", ".id", "--cuts", "0.5",
		}, labels...)
	}
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
			wantOut:  fileOf(`{"model":"jev-latest",` + mockAllJSON + "}\n"),
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
			name:     "should name the resolved model in a table",
			args:     with("-o", "table", "--state", "x"),
			mock:     mockAll,
			contains: []string{"jev-latest", "billing", "curt"},
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
			name:    "should replay a one line -o json file for its record alone",
			args:    []string{"is it urgent", "-o", "json", "-i", "jsonl"},
			stdin:   "\"a\"\n\"b\"\n\"c\"\n",
			mock:    `{"model":"mock","answer":{"value":0.9}}` + "\n",
			wantOut: fileOf(`{"model":"jev-latest","answer":{"value":0.9}}` + "\n"),
			stderr: []string{"onesie: --mock has no line 2, so input line 2 has no answer. The file was read as " +
				"-o json lines, since its one line carries model. Add a line per record, or drop model to " +
				"answer every record"},
			wantCode: ExitUsage,
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
			contains: []string{"| `u` | `t` | `r` | error |", "| 0.9 | `billing` | `curt` | |", "_`jev-latest`_"},
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
			wantOut:  fileOf(`{"assert":false,"model":"jev-latest",` + mockAllJSON + "}\n"),
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
			name:  "should report zero usage on every line",
			args:  with("-o", "json", "-i", "lines", "--usage"),
			stdin: "a\nb\n",
			mock:  mockAll,
			wantOut: fileOf(lines(`{"line":1,"model":"jev-latest","usage":`+mockUsage+`,`+mockAllJSON+`}`,
				`{"line":2,"model":"jev-latest","usage":`+mockUsage+`,`+mockAllJSON+`}`)),
			wantCode: ExitOK,
		},
		{
			name:  "should say the records were answered from the mock under --stats",
			args:  with("-o", "values", "-i", "lines", "--stats"),
			stdin: "a\nb\n",
			mock:  mockAll,
			stderr: []string{"2 records answered from the mock, 6 questions asked, 0 in / 0 out tokens, " +
				"model jev-latest, 2 attempts, 10s/attempt, "},
			wantCode: ExitOK,

			stderrAbsent: []string{"request", "$"},
		},
		{
			name: "should count the retries of a mocked 503 under --stats",
			args: with("-o", "values", "--state", "x", "--stats"),
			mock: `{"error":503}`,
			stderr: []string{
				"1 record, 1 failed, 0 answered from the mock, 3 questions asked, 0 in / 0 out tokens, " +
					"3 attempts (2 retries: 503×2), 10s/attempt, ",
			},
			wantCode: ExitUnavailable,
		},
		{
			name:     "should replay a stored cloudflare auth row as the refusal it was, with kind auth and exit 3",
			args:     with("-i", "lines", "-o", "json", "--provider", "cloudflare"),
			stdin:    "a\n",
			mock:     `{"error":{"kind":"auth","status":400,"message":"400 Authentication error"}}` + "\n",
			contains: []string{`"error":{"kind":"auth","status":400,"message":"400 Authentication error"}`},
			wantCode: ExitAuth,
		},
		{
			name:     "should exit 3 for one record the file refuses with 401, naming no key",
			args:     with("--state", "x"),
			mock:     `{"error":401}`,
			stderr:   []string{"onesie: 401 status code, no body"},
			wantCode: ExitAuth,

			stderrAbsent: []string{"The key came from"},
		},
		{
			name:     "should exit 4 for one record the file fails with 503",
			args:     with("--state", "x", "-o", "json"),
			mock:     `{"error":503}`,
			contains: []string{`"error":{"kind":"http","status":503`},
			wantCode: ExitUnavailable,
		},
		{
			name:     "should exit 5 for one record the file times out, as a timed out connection reads",
			args:     with("--state", "x", "--timeout", "4"),
			mock:     `{"error":"timeout"}`,
			stderr:   []string{"onesie: request to mock.invalid:443 timed out"},
			wantCode: ExitTransport,
		},
		{
			name:     "should exit 4 for a response entry, as a live 200 with no answers reads",
			args:     with("--state", "x"),
			mock:     `{"error":{"kind":"response","status":200,"message":"x"}}`,
			stderr:   []string{"onesie: 200 response could not be used: the response is not a System One result, since it has no answers"},
			wantCode: ExitUnavailable,
		},
		{
			name:     "should give a mocked 402 the credit advice a live one gets",
			args:     with("--state", "x"),
			mock:     `{"error":402}`,
			stderr:   []string{"onesie: 402 status code, no body. Add credits to the account this key belongs to"},
			wantCode: ExitAuth,
		},
		{
			name:     "should give a mocked unknown model the advice a live one gets",
			args:     with("--state", "x"),
			mock:     `{"error":{"kind":"http","status":400,"body":{"detail":"Unknown model: jev-latest"}}}`,
			stderr:   []string{"onesie: model 'jev-latest' not found. Try --list-models"},
			wantCode: ExitUsage,
		},
		{
			name: "should give a mocked html body the not JSON advice a live one gets",
			args: with("--state", "x"),
			mock: `{"error":{"kind":"http","status":405,"body":"<html>nope</html>"}}`,
			stderr: []string{"onesie: 405 Method Not Allowed, with a response body that is not JSON. " +
				"Check that the base URL points at the System One API"},
			wantCode: ExitUsage,
		},
		{
			name:     "should fail a mocked null body as a mocked status with no body",
			args:     with("--state", "x"),
			mock:     `{"error":{"kind":"http","status":402,"body":null}}`,
			stderr:   []string{"onesie: 402 status code, no body. Add credits to the account this key belongs to"},
			wantCode: ExitAuth,
		},
		{
			name:     "should fail a mocked connection as a live one reads",
			args:     with("--state", "x"),
			mock:     `{"error":"connection"}`,
			stderr:   []string{"onesie: could not reach mock.invalid: mock connection error"},
			wantCode: ExitTransport,
		},
		{
			name:     "should exit 6 for a stream with one 503 line",
			args:     with("-o", "values", "-i", "lines"),
			stdin:    "a\nb\nc\n",
			mock:     lines(mockAll, `{"error":503}`, mockAll),
			contains: []string{`{"line":1,` + mockAll[1:], `{"line":2,"error":{"kind":"http","status":503`},
			wantCode: ExitRecords,
		},
		{
			name:     "should abort a stream with exit 3 at a 401 line",
			args:     with("-o", "values", "-i", "lines"),
			stdin:    "a\nb\nc\n",
			mock:     lines(mockAll, `{"error":401}`, mockAll),
			stderr:   []string{"onesie: 401 status code, no body"},
			wantCode: ExitAuth,

			stderrAbsent: []string{"The key came from"},
		},
		{
			name:    "should stop at the first record the file does not cover, naming its line",
			args:    with("-o", "values", "-i", "lines", "--skip-blank"),
			stdin:   "a\n\nb\nc\n",
			mock:    lines(mockAll, mockUnread),
			wantOut: fileOf(`{"line":1,` + mockAll[1:] + "\n"),
			stderr: []string{"onesie: --mock line 2 replays a line onesie could not read, so input line 3 " +
				"has no answer. Give it answers"},
			wantCode: ExitUsage,
		},
		{
			name:     "should name the id of a record the file does not cover",
			args:     with("-o", "values", "-i", "jsonl", "--id", ".id"),
			stdin:    "{\"id\":\"a\"}\n{\"id\":\"b\"}\n",
			mock:     lines(`{"id":"a",`+mockAll[1:], `{"id":"c",`+mockAll[1:]),
			wantOut:  fileOf(`{"id":"a",` + mockAll[1:] + "\n"),
			stderr:   []string{"onesie: --mock has no line with id 'b', so input line 2 has no answer. Add one"},
			wantCode: ExitUsage,
		},
		{
			name:     "should write only the lines before an uncovered record under -j 4",
			args:     with("-o", "values", "-i", "lines", "-j", "4"),
			stdin:    "a\nb\nc\nd\ne\nf\ng\nh\n",
			mock:     lines(mockAll, mockAll, mockUnread, mockAll, mockAll, mockAll, mockAll, mockAll),
			wantOut:  fileOf(lines(`{"line":1,`+mockAll[1:], `{"line":2,`+mockAll[1:])),
			stderr:   []string{"line 3"},
			wantCode: ExitUsage,
		},
		{
			name:  "should count an uncovered record as a record and not a request",
			args:  with("-o", "values", "-i", "lines", "--stats"),
			stdin: "a\nb\n",
			mock:  lines(mockAll, mockUnread),
			stderr: []string{"2 records, 1 failed, 1 answered from the mock, 3 questions asked, 0 in / 0 out tokens, " +
				"model jev-latest, 1 attempt, 10s/attempt, "},
			wantCode: ExitUsage,
		},
		{
			name:     "should write nothing for one record the file does not cover, fallback included",
			args:     append(fallback, "-o", "json", "--state", "x"),
			mock:     mockUnread,
			wantOut:  fileOf(""),
			stderr:   []string{"onesie: --mock line 1 replays a line onesie could not read, so input line 1 has no answer"},
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
			stderr:   []string{"onesie: --mock: the entry has no answer for question 'r'"},
			wantCode: ExitUsage,
		},
		{
			name:     "should ignore --base-url",
			args:     with("-o", "json", "--state", "x", "--base-url", "http://example.com"),
			mock:     mockAll,
			wantOut:  fileOf(`{"model":"jev-latest",` + mockAllJSON + "}\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should show the model -m names",
			args:     with("-o", "json", "--state", "x", "-m", "other"),
			mock:     mockAll,
			wantOut:  fileOf(`{"model":"other",` + mockAllJSON + "}\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should show the default model of the provider",
			args:     with("-o", "json", "--state", "x", "--provider", "cloudflare"),
			mock:     mockAll,
			wantOut:  fileOf(`{"model":"typesafe/jev",` + mockAllJSON + "}\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should show a cloudflare model as -m names it",
			args:     with("-o", "json", "--state", "x", "--provider", "cloudflare", "-m", "clef-flash"),
			mock:     mockAll,
			wantOut:  fileOf(`{"model":"clef-flash",` + mockAllJSON + "}\n"),
			wantCode: ExitOK,
		},
		{
			name:     "should refuse a misspelled --provider",
			args:     with("--state", "x", "--provider", "bogus"),
			mock:     mockAll,
			stderr:   []string{"onesie: unknown provider 'bogus'"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse a misspelled ONESIE_PROVIDER",
			args:     with("-o", "values", "--state", "x"),
			env:      map[string]string{"ONESIE_PROVIDER": "bogus"},
			mock:     mockAll,
			stderr:   []string{"onesie: unknown provider 'bogus'"},
			wantCode: ExitUsage,
		},
		{
			name:     "should answer under cloudflare with no account",
			args:     with("-o", "values", "--state", "x", "--provider", "cloudflare"),
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
			name:     "should refuse a misspelled ONESIE_PROVIDER before -i request",
			args:     []string{"-i", "request"},
			env:      map[string]string{"ONESIE_PROVIDER": "bogus"},
			mock:     mockAll,
			stderr:   []string{"onesie: unknown provider 'bogus'"},
			wantCode: ExitUsage,
		},
		{
			name:     "should refuse a misspelled ONESIE_PROVIDER before --list-models",
			args:     []string{"--list-models"},
			env:      map[string]string{"ONESIE_PROVIDER": "bogus"},
			mock:     mockAll,
			stderr:   []string{"onesie: unknown provider 'bogus'"},
			wantCode: ExitUsage,
		},
		{
			name: "should refuse --mock on profile test, which takes no such flag",
			args: []string{"profile", "test"},
			mock: mockAll,
			stderr: []string{
				"onesie: profile test has no flag --mock. --mock belongs to a run, not to profile test",
			},
			wantCode: ExitUsage,
		},
		{
			name:     "should answer calibrate with one label from a mock that answers every question of the file",
			args:     calibrating("--label", "urgent=.u"),
			stdin:    threeStdin,
			mock:     threeMock,
			contains: []string{"urgent, yes/no: labelled 4, 2 yes, 2 no, 0 failed"},
			absent:   []string{"team,", "tone,"},
			stderr:   []string{"onesie: skipping 'team' and 'tone', which have no --label\n"},
			wantCode: ExitOK,
		},
		{
			name:     "should answer calibrate with two labels from a mock that answers every question of the file",
			args:     calibrating("--label", "team=.t", "--label", "tone=.r"),
			stdin:    threeStdin,
			mock:     threeMock,
			contains: []string{"team, pick: labelled 4", "tone, rate: labelled 4"},
			absent:   []string{"urgent,"},
			wantCode: ExitOK,
		},
		{
			name:     "should answer calibrate with every label from a mock that answers every question of the file",
			args:     calibrating("--label", "urgent=.u", "--label", "team=.t", "--label", "tone=.r"),
			stdin:    threeStdin,
			mock:     threeMock,
			contains: []string{"urgent, yes/no", "team, pick", "tone, rate"},
			wantCode: ExitOK,
		},
		{
			name:     "should answer calibrate with one label from a mock that answers only that question",
			args:     calibrating("--label", "urgent=.u"),
			stdin:    threeStdin,
			mock:     lines(`{"id":"a","urgent":0.9}`, `{"id":"b","urgent":0.2}`, `{"id":"c","urgent":0.7}`, `{"id":"d","urgent":0.1}`),
			contains: []string{"urgent, yes/no: labelled 4"},
			wantCode: ExitOK,
		},
		{
			name:  "should refuse a calibrate mock that misses the asked question",
			args:  calibrating("--label", "urgent=.u"),
			stdin: threeStdin,
			mock:  lines(`{"id":"a","team":"billing","tone":"calm"}`),
			stderr: []string{"the entry has no answer for question 'urgent'. " +
				"Every entry answers every question the run asks"},
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

	t.Run("should write only the asked answers to --out from a mock that answers every question", func(t *testing.T) {
		t.Parallel()

		answers := filepath.Join(t.TempDir(), "answers.jsonl")
		args := append(calibrating("--label", "urgent=.u"), "--out", answers, "--mock", writeMock(t, threeMock))

		out, errOut, code := runMocked(t, t.Context(), args, threeStdin, nil, nil)
		if code != ExitOK {
			t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
		}

		for _, line := range answerLines(t, answers) {
			if !strings.Contains(line, `"urgent":`) || strings.Contains(line, `"team"`) || strings.Contains(line, `"tone"`) {
				t.Errorf("answers line %s, want the urgent answer alone", line)
			}
		}
	})

	t.Run("should never touch the keychain", func(t *testing.T) {
		t.Parallel()

		chain := noKeychain()

		_, errOut, code := runMocked(t, t.Context(),
			[]string{"is it urgent", "-o", "values", "--state", "x", "--mock", writeMock(t, `{"answer":0.9}`)},
			"", nil, nil, withKeychain(chain))
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, errOut)
		}

		if calls := chain.calls(); calls != 0 {
			t.Errorf("keychain calls = %d, want none", calls)
		}
	})

	t.Run("should answer with the provider and the model of the profile", func(t *testing.T) {
		t.Parallel()

		dir := configWith(t, `{"default":"lv","profiles":{"lv":{"provider":"levanto","api_key":"SECRET-LV"},`+
			`"or":{"provider":"openrouter","store":"keychain","account":"or@abc","model":"typesafe/jev-1.13"}}}`)
		chain := workingKeychain(nil)

		asks := []struct {
			args  []string
			model string
		}{
			{args: with("-o", "json", "--state", "x"), model: "sage-latest"},
			{args: with("-o", "json", "--state", "x", "--profile", "or"), model: "typesafe/jev-1.13"},
		}

		for _, ask := range asks {
			out, errOut, code := runMocked(t, t.Context(), append(ask.args, "--mock", writeMock(t, mockAll)), "",
				map[string]string{"ONESIE_CONFIG_DIR": dir}, nil, withKeychain(chain))

			checkMocked(t, out, errOut, code, ExitOK, fileOf(`{"model":"`+ask.model+`",`+mockAllJSON+"}\n"),
				nil, nil, nil)
		}

		if calls := chain.calls(); calls != 0 {
			t.Errorf("keychain calls = %d, want none", calls)
		}
	})

	t.Run("should refuse a profile that does not exist", func(t *testing.T) {
		t.Parallel()

		dir := configWith(t, `{"profiles":{"lv":{"provider":"levanto","api_key":"SECRET-LV"}}}`)

		out, errOut, code := runMocked(t, t.Context(),
			with("--state", "x", "--profile", "nope", "--mock", writeMock(t, mockAll)), "",
			map[string]string{"ONESIE_CONFIG_DIR": dir}, nil)

		checkMocked(t, out, errOut, code, ExitUsage, nil, nil, nil,
			[]string{"onesie: --profile names profile 'nope', which does not exist. The profiles are 'lv'"})
	})

	t.Run("should stop at a record the file does not answer", func(t *testing.T) {
		t.Parallel()

		const jobs = 4

		values := []string{"is it urgent", "-o", "values", "-i", "jsonl", "-j", strconv.Itoa(jobs)}
		yes := `{"answer":0.9}`

		// The ordered engine lets -j records wait behind the one the writer holds and lets the writer
		// hold one more, so while it waits on record 2 the dispatched records are 1 to jobs + 1. Of
		// those, record 2 is held and record 3 fails before any request, which leaves jobs - 1 to answer.
		others := jobs - 1

		t.Run("should drop the records the engine flushes after an uncovered one", func(t *testing.T) {
			t.Parallel()

			eight := "\"item-1\"\n\"item-2\"\n\"item-3\"\n\"item-4\"\n\"item-5\"\n\"item-6\"\n\"item-7\"\n\"item-8\"\n"
			answers := strings.Join([]string{yes, yes, mockUnread, yes, yes, yes, yes, yes}, "\n") + "\n"

			// Record 2 is held until every other record in flight is answered. Record 3 fails at once,
			// so the engine holds answers past it when the writer reaches it.
			out, errOut, code := runMocked(t, t.Context(), append(values, "--mock", writeMock(t, answers)), eight, nil, nil,
				delaying("item-2", others))

			checkMocked(t, out, errOut, code, ExitUsage, fileOf(yes+"\n"+yes+"\n"), nil, nil, []string{"input line 3"})
		})

		t.Run("should keep the lines that arrived before an uncovered record under --unordered", func(t *testing.T) {
			t.Parallel()

			answers := strings.Repeat(yes+"\n", 7) + mockUnread + "\n"

			// The uncovered record is read only once the seven before it are written, so its error
			// cannot race their lines.
			out := &signalledLines{written: make(chan struct{}, 8)}
			stdin := &gatedLines{
				lines:   []string{`"a"`, `"b"`, `"c"`, `"d"`, `"e"`, `"f"`, `"g"`, `"h"`},
				gatedAt: 7, waits: 7, gate: out.written, done: beforeDeadline(t).Done(),
			}

			errOut, code := runMockedTo(t, t.Context(), out,
				append(values, "--unordered", "--mock", writeMock(t, answers)), "", nil, stdin)

			checkMocked(t, out.String(), errOut, code, ExitUsage, fileOf(strings.Repeat(yes+"\n", 7)), nil, nil,
				[]string{"input line 8"})
		})

		t.Run("should keep the lines of records after an uncovered one that finished first under --unordered", func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			square := squarePNG(t)
			paths := make([]string, 8)

			for i := range paths {
				paths[i] = writeImage(t, dir, strconv.Itoa(i+1)+".png", square)
			}

			answers := strings.Join([]string{yes, yes, mockUnread, yes, yes, yes, yes, yes}, "\n") + "\n"
			out := &signalledLines{written: make(chan struct{}, 8)}
			done := beforeDeadline(t).Done()

			// Under --unordered every other record runs past the one held, so record 3 reads its image
			// only once the other seven lines are written, and its error lands after all of them.
			read := WithReadFile(func(path string) ([]byte, error) {
				if path == paths[2] {
					for range len(paths) - 1 {
						select {
						case <-out.written:
						case <-done:
							return nil, errors.New("the other records were never written")
						}
					}
				}

				return os.ReadFile(path)
			})

			args := []string{
				"is it urgent", "-o", "values", "-i", "lines", "--image-field", "photo", "-j", strconv.Itoa(jobs),
				"--unordered", "--mock", writeMock(t, answers),
			}

			errOut, code := runMockedTo(t, t.Context(), out, args, strings.Join(paths, "\n")+"\n", nil, nil, read)

			var want strings.Builder
			for _, line := range []int{1, 2, 4, 5, 6, 7, 8} {
				fmt.Fprintf(&want, "{\"line\":%d,\"answer\":0.9}\n", line)
			}

			checkMocked(t, sortedLines(out.String()), errOut, code, ExitUsage, fileOf(want.String()), nil, nil,
				[]string{"input line 3"})
		})

		t.Run("should keep calibrate's answers file to the records before an uncovered one", func(t *testing.T) {
			t.Parallel()

			ids := []string{"a", "b", "c", "d", "e", "f"}

			var stdin, partial, full strings.Builder

			for _, id := range ids {
				fmt.Fprintf(&stdin, "{\"id\":%q,\"body\":\"item-%s\",\"u\":true}\n", id, id)
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
				stdin.String(), nil, nil, delaying("item-b", others))
			if code != ExitUsage || !strings.Contains(errOut, "no line with id 'c'") {
				t.Fatalf("first run exit %d\n%s", code, errOut)
			}

			want := `{"id":"a","model":"jev-latest","u":{"value":0.9}}` + "\n" + `{"id":"b","model":"jev-latest","u":{"value":0.9}}` + "\n"
			if got := readText(t, out); got != want {
				t.Errorf("file = %q, want %q", got, want)
			}

			_, errOut, code = runMocked(t, t.Context(), append(args, "--mock", writeMock(t, full.String())),
				stdin.String(), nil, nil)
			if code != ExitOK || !strings.Contains(errOut, "onesie: asking 4 of 6 records") {
				t.Errorf("second run exit %d\n%s", code, errOut)
			}
		})
	})

	t.Run("should replay the lines a real run wrote", func(t *testing.T) {
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
	})

	t.Run("should resume a run from the file", func(t *testing.T) {
		t.Parallel()

		question := []string{"is it urgent", "-o", "json", "-i", "jsonl", "--id", ".id"}
		input := "{\"id\":1}\n{\"id\":2}\n{\"id\":3}\n"
		full := "{\"id\":1,\"answer\":0.1}\n{\"id\":2,\"answer\":0.2}\n{\"id\":3,\"answer\":0.3}\n"
		wantFile := `{"id":1,"model":"jev-latest","answer":{"value":0.1}}` + "\n" +
			`{"id":2,"model":"jev-latest","answer":{"value":0.2}}` + "\n" +
			`{"id":3,"model":"jev-latest","answer":{"value":0.3}}` + "\n"

		t.Run("should stop a resume at a stored refused key with the code the refusal exits with", func(t *testing.T) {
			t.Parallel()

			tests := []struct {
				name     string
				provider string
				row      string
			}{
				{
					name: "should exit 3 for a stored 401", provider: "typesafe",
					row: `{"error":{"kind":"auth","status":401,"message":"401 the key was refused"}}`,
				},
				{
					name: "should exit 3 for a stored 401 written with kind http", provider: "typesafe",
					row: `{"error":{"kind":"http","status":401,"message":"401 the key was refused"}}`,
				},
				{
					name: "should exit 3 for a stored cloudflare 400 that refused the token", provider: "cloudflare",
					row: `{"error":{"kind":"auth","status":400,"message":"400 Authentication failed (status: 400)"}}`,
				},
			}

			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					t.Parallel()

					out := filepath.Join(t.TempDir(), "answers.jsonl")
					args := []string{
						"is it urgent", "-o", "json", "-i", "jsonl", "--provider", tc.provider, "--out", out, "--resume",
						"--mock", writeMock(t, "{\"answer\":0.2}\n{\"answer\":0.3}\n{\"answer\":0.4}\n"),
					}
					env := map[string]string{"CLOUDFLARE_ACCOUNT_ID": "acct"}
					stdin := "\"one\"\n\"two\"\n\"three\"\n"

					// A clean run writes the fingerprint. The file then holds the refusal followed by an
					// answer, the shape a run the refusal aborts leaves when a later record finishes first.
					if _, errOut, code := runMocked(t, t.Context(), args, stdin, env, nil); code != ExitOK {
						t.Fatalf("clean run exit %d\n%s", code, errOut)
					}

					answered := strings.SplitAfter(readText(t, out), "\n")[1]
					if err := os.WriteFile(out, []byte(tc.row+"\n"+answered), 0o600); err != nil {
						t.Fatal(err)
					}

					_, errOut, code := runMocked(t, t.Context(), append(args, "--stop-on-error"), stdin, env, nil)
					if code != ExitAuth {
						t.Errorf("resume exit %d, want %d as the live refusal exits\n%s", code, ExitAuth, errOut)
					}

					// Without the flag the stored refusal is skipped as any failed line is, and counts as failed.
					_, errOut, code = runMocked(t, t.Context(), args, stdin, env, nil)
					if code != ExitRecords {
						t.Errorf("resume without --stop-on-error exit %d, want %d\n%s", code, ExitRecords, errOut)
					}
				})
			}
		})

		t.Run("should keep the lines before an uncovered record and ask the rest on resume", func(t *testing.T) {
			t.Parallel()

			out := filepath.Join(t.TempDir(), "answers.jsonl")
			partial := writeMock(t, "{\"id\":1,\"answer\":0.1}\n{\"id\":3,\"answer\":0.3}\n")
			args := append(append([]string{}, question...), "--out", out, "--resume")

			_, errOut, code := runMocked(t, t.Context(), append(args, "--mock", partial), input, nil, nil)
			if code != ExitUsage {
				t.Fatalf("first run exit %d, want 2\n%s", code, errOut)
			}

			if got := readText(t, out); got != `{"id":1,"model":"jev-latest","answer":{"value":0.1}}`+"\n" {
				t.Errorf("after the first run the file holds %q", got)
			}

			_, errOut, code = runMocked(t, t.Context(), append(args, "--mock", writeMock(t, full), "--stats"), input, nil, nil)
			if code != ExitOK {
				t.Fatalf("second run exit %d, want 0\n%s", code, errOut)
			}

			if !strings.Contains(errOut, "3 records, 1 skipped by --resume, 2 answered from the mock") {
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
			args := append(append([]string{}, question...), "--out", out, "--resume", "--mock", mockFile, "-j", "1")

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			_, errOut, code := runMocked(t, ctx, args, input, nil, nil, interruptingAt(2, cancel))
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
			name: "should refuse profile show under ONESIE_MOCK",
			args: []string{"profile", "show"},
			env:  func(path string) map[string]string { return map[string]string{envMock: path} },
			stderr: []string{
				"onesie: profile reads and writes real configuration, so it does not run under ONESIE_MOCK. Unset it",
			},
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

func TestMockAnswerer_Answer(t *testing.T) {
	t.Parallel()

	questions := onesie.Questions{{ID: "answer", Question: onesie.Noul{Instructions: "q"}}}
	known := []plan.Question{{ID: "answer", Shape: plan.Noul}}

	asker := func(t *testing.T, file string, provider onesie.Provider, retries int) (mockAnswerer, *collector) {
		t.Helper()

		answers, err := mock.Load(strings.NewReader(file), known, mock.Options{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		stats := &collector{}
		settings := defaultSettings(BuildInfo{})
		settings.lookupEnv = lookupFrom(map[string]string{})

		client, err := mockClient(settings, runConfig{runFlags: runFlags{retries: retries}}, provider, stats)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		return mockAnswerer{answers: answers, provider: provider, live: newLiveAnswerer(client)}, stats
	}

	t.Run("should count no attempt for a request the provider refuses", func(t *testing.T) {
		t.Parallel()

		img, err := onesie.NewImage(squarePNG(t))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		a, stats := asker(t, `{"answer":0.9}`, onesie.Levanto(), 0)

		_, err = a.answer(t.Context(), recordKey{position: 1}, onesie.Request{
			Model: onesie.Levanto().DefaultModel(), State: map[string]any{"photo": "img"}, Questions: questions,
			Assets: onesie.Assets{"img": img},
		})
		if err == nil || !strings.Contains(err.Error(), "takes no images") {
			t.Fatalf("error = %v, want levanto's refusal", err)
		}

		if got := stats.snapshot(0).Attempts; got != 0 {
			t.Errorf("attempts = %d, want 0", got)
		}
	})

	t.Run("should refuse an entry that leaves a question the request asks unanswered", func(t *testing.T) {
		t.Parallel()

		a, _ := asker(t, `{"answer":0.9}`, onesie.TypeSafe(), 0)

		_, err := a.answer(t.Context(), recordKey{position: 1}, onesie.Request{
			Model: "m", State: "s", Questions: onesie.Questions{
				{ID: "answer", Question: onesie.Noul{Instructions: "q"}},
				{ID: "other", Question: onesie.Noul{Instructions: "q"}},
			},
		})

		var unusable *onesie.ResponseError
		if !errors.As(err, &unusable) || unusable.Status != http.StatusOK ||
			text(err) != "200 response could not be used: question 'other' has no answer" {
			t.Errorf("error = %v, want a 200 ResponseError naming the unanswered question", err)
		}
	})

	t.Run("should refuse a request ValidateRequest refuses before the provider sees it", func(t *testing.T) {
		t.Parallel()

		a, stats := asker(t, `{"answer":0.9}`, onesie.Cloudflare(), 0)

		_, err := a.answer(t.Context(), recordKey{position: 1}, onesie.Request{
			Model: "m", State: "s",
			Questions: onesie.Questions{{ID: "answer", Question: onesie.Box{Instructions: "q"}}},
		})
		if err == nil || !strings.Contains(err.Error(), "needs an image") {
			t.Fatalf("error = %v, want ValidateRequest's refusal", err)
		}

		if got := stats.snapshot(0).Attempts; got != 0 {
			t.Errorf("attempts = %d, want 0", got)
		}
	})

	t.Run("should answer with the request's model", func(t *testing.T) {
		t.Parallel()

		a, stats := asker(t, `{"answer":0.9}`, onesie.TypeSafe(), 0)

		got, err := a.answer(t.Context(), recordKey{position: 1}, onesie.Request{
			Model: "other", State: "s", Questions: questions,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if got.result.Model != "other" {
			t.Errorf("model = %q, want other", got.result.Model)
		}

		if attempts := stats.snapshot(0).Attempts; attempts != 1 {
			t.Errorf("attempts = %d, want 1", attempts)
		}
	})

	t.Run("should retry a retried entry as a live one is, without waiting", func(t *testing.T) {
		t.Parallel()

		// The bubble's clock moves only when something waits on it, so any backoff shows as time passed.
		synctest.Test(t, func(t *testing.T) {
			a, stats := asker(t, `{"error":503}`, onesie.TypeSafe(), 2)

			started := time.Now()

			_, err := a.answer(t.Context(), recordKey{position: 1}, onesie.Request{
				Model: "m", State: "s", Questions: questions,
			})
			if err == nil || !strings.Contains(err.Error(), "503 status code, no body") {
				t.Fatalf("error = %v, want the mocked 503", err)
			}

			if got := stats.snapshot(0); got.Attempts != 3 || got.Retries[http.StatusServiceUnavailable] != 3 {
				t.Errorf("attempts = %d, failed 503 attempts = %v, want 3 and 3", got.Attempts, got.Retries)
			}

			if waited := time.Since(started); waited != 0 {
				t.Errorf("the retries waited %s, want no wait", waited)
			}
		})
	})

	t.Run("should pass an error entry's error on", func(t *testing.T) {
		t.Parallel()

		a, _ := asker(t, `{"error":402}`, onesie.TypeSafe(), 0)

		got, err := a.answer(t.Context(), recordKey{position: 1}, onesie.Request{
			Model: "m", State: "s", Questions: questions,
		})
		if err == nil || !strings.Contains(err.Error(), "402 status code, no body") {
			t.Fatalf("error = %v, want the mocked 402", err)
		}

		if got.result != nil {
			t.Errorf("result = %+v, want none", got.result)
		}
	})
}

func runMocked(
	t *testing.T, ctx context.Context, args []string, stdin string, env map[string]string, reader io.Reader,
	extra ...RootOption,
) (string, string, int) {
	t.Helper()

	var out bytes.Buffer

	errOut, code := runMockedTo(t, ctx, &out, args, stdin, env, reader, extra...)

	return out.String(), errOut, code
}

func runMockedTo(
	t *testing.T, ctx context.Context, out io.Writer, args []string, stdin string, env map[string]string,
	reader io.Reader, extra ...RootOption,
) (string, int) {
	t.Helper()

	lookup := map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()}
	for name, value := range env {
		lookup[name] = value
	}

	if reader == nil {
		reader = strings.NewReader(stdin)
	}

	var errOut bytes.Buffer

	opts := []RootOption{
		withKeychain(noKeychain()),
		WithStdin(reader),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(lookup)),
		WithClientFactory(func(context.Context, ...onesie.Option) (*onesie.Client, error) {
			t.Error("a mock run built an API client")

			return nil, os.ErrInvalid
		}),
	}

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, append(opts, extra...)...)

	root.Command.SetOut(out)
	root.Command.SetErr(&errOut)
	root.Command.SetArgs(args)

	code := root.Execute(ctx)

	return errOut.String(), code
}

func runReal(t *testing.T, args []string, stdin, base string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		withKeychain(noKeychain()),
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
		WithClientFactory(stubFactory(base)),
	)

	root.Command.SetOut(&out)
	root.Command.SetErr(&errOut)
	root.Command.SetArgs(args)

	code := root.Execute(t.Context())

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

func interruptingAt(request int, interrupt context.CancelFunc) RootOption {
	var arrived atomic.Int32

	return wrappingMock(func(next http.RoundTripper) http.RoundTripper {
		return roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if int(arrived.Add(1)) != request {
				return next.RoundTrip(req)
			}

			// Under -j 1 the engine asks a record only once the record before it is written, so an
			// interrupt as this request arrives leaves exactly the lines before it.
			interrupt()

			return nil, errors.Join(req.Body.Close(), req.Context().Err())
		})
	})
}

func delaying(marker string, others int) RootOption {
	answered := make(chan struct{}, 64)

	return holding(marker, others, answered, answered)
}

func holding(marker string, others int, released <-chan struct{}, answered chan<- struct{}) RootOption {
	return wrappingMock(func(next http.RoundTripper) http.RoundTripper {
		return roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body, err := io.ReadAll(req.Body)
			if err = errors.Join(err, req.Body.Close()); err != nil {
				return nil, err
			}

			req.Body = io.NopCloser(bytes.NewReader(body))

			if !bytes.Contains(body, []byte(marker)) {
				got, err := next.RoundTrip(req)
				if answered != nil {
					answered <- struct{}{}
				}

				return got, err
			}

			for range others {
				select {
				case <-released:
				case <-req.Context().Done():
					return nil, req.Context().Err()
				}
			}

			return next.RoundTrip(req)
		})
	})
}

func wrappingMock(wrap func(http.RoundTripper) http.RoundTripper) RootOption {
	return func(s *rootSettings) {
		next := s.mockTransport
		s.mockTransport = func(provider onesie.Provider) http.RoundTripper {
			return wrap(next(provider))
		}
	}
}

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (r *gatedLines) Read(p []byte) (int, error) {
	if r.next >= len(r.lines) {
		return 0, io.EOF
	}

	if r.next == r.gatedAt {
		for range r.waits {
			select {
			case <-r.gate:
			case <-r.done:
				return 0, errors.New("the gate never opened")
			}
		}
	}

	n := copy(p, r.lines[r.next]+"\n")
	r.next++

	return n, nil
}

type gatedLines struct {
	lines   []string
	next    int
	gatedAt int
	waits   int
	gate    <-chan struct{}
	done    <-chan struct{}
}

func beforeDeadline(t *testing.T) context.Context {
	t.Helper()

	// A test that waits on the run fails a little before go test would kill the binary, so the
	// failure names the test.
	deadline, set := t.Deadline()
	if !set {
		return t.Context()
	}

	ctx, cancel := context.WithDeadline(t.Context(), deadline.Add(-10*time.Second))
	t.Cleanup(cancel)

	return ctx
}

func (w *signalledLines) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n, err := w.out.Write(p)
	for range bytes.Count(p[:n], []byte("\n")) {
		w.written <- struct{}{}
	}

	return n, err
}

func (w *signalledLines) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.out.String()
}

type signalledLines struct {
	mu      sync.Mutex
	out     bytes.Buffer
	written chan struct{}
}
