package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

var lineKey = regexp.MustCompile(`"line":\d+,`)

func TestResumed(t *testing.T) {
	t.Parallel()

	t.Run("should number the records a resume asks by their input line", func(t *testing.T) {
		t.Parallel()

		out := filepath.Join(t.TempDir(), "answers.jsonl")
		args := []string{"is it urgent", "-i", "jsonl", "-o", "values", "--out", out, "--resume", "--mock", writeMock(t, `{"answer":0.9}`+"\n")}

		if _, errOut, code := runMocked(t, t.Context(), args, "\"a\"\n\"b\"\n", nil, nil); code != ExitOK {
			t.Fatalf("first run exit %d\n%s", code, errOut)
		}

		if _, errOut, code := runMocked(t, t.Context(), args, "\"a\"\n\"b\"\n\"c\"\n", nil, nil); code != ExitOK {
			t.Fatalf("resumed run exit %d\n%s", code, errOut)
		}

		want := `{"line":1,"answer":0.9}` + "\n" + `{"line":2,"answer":0.9}` + "\n" + `{"line":3,"answer":0.9}` + "\n"
		if got := readText(t, out); got != want {
			t.Errorf("file = %q, want %q", got, want)
		}
	})

	answeredBody := `{"model":"m","answers":{"answer":{"type":"noul","noul":0.5}},"usage":{}}` + "\n"
	failedBody := `{"error":{"kind":"http","status":400,"message":"400 bad key"}}` + "\n"
	unavailableBody := `{"error":{"kind":"http","status":503,"message":"503 bad key"}}` + "\n"
	forwardedPrint, err := answersfile.Fingerprint(nil, plan.WireAll(nil), answersfile.FingerprintInputs{Provider: "typesafe", Input: "request"})
	if err != nil {
		t.Fatalf("fingerprinting -i request: %v", err)
	}

	forwarded := forwardedPrint.String()

	byPositionAbstaining := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
		Provider: "typesafe", Model: onesie.TypeSafe().DefaultModel(), Output: "values", Input: "jsonl",
		Assert: "answer.value > 0.9", AbstainIf: "answer.value > 0.4",
	})
	stored := "{\"verdict\":\"unsure\",\"answer\":0.5}\n{\"verdict\":\"fail\",\"answer\":0.5}\n"
	byPositionFailing := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
		Provider: "typesafe", Model: onesie.TypeSafe().DefaultModel(), Output: "values", Input: "jsonl",
		Assert: "answer.value > 0.9", AbstainIf: "answer.value > 0.6",
	})

	byPositionGated := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, answersfile.FingerprintInputs{
		Provider: "typesafe", Model: onesie.TypeSafe().DefaultModel(), Output: "values", Input: "jsonl",
		Assert: "answer.value > 0.9",
	})
	contradictory := `{"error":{"kind":"http","status":503,"message":"503 bad key"},"verdict":"fail","verdict":"unsure","answer":0.5}` + "\n"

	firstMock, secondMock := writeMock(t, `{"answer": 0.1}`), writeMock(t, `{"answer": 0.2}`)

	runResumeCases(t, []resumeCase{
		{
			name:  "should resume with a different --mock file",
			stdin: idRecords(1, 2),
			runs: []resumeRun{
				{
					args:     []string{"-i", "jsonl", "-o", "values", "--mock", firstMock},
					input:    idRecords(1, 1),
					wantFile: "{\"answer\":0.1}\n",
				},
				{
					args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--mock", secondMock},
					wantFile: "{\"answer\":0.1}\n{\"answer\":0.2}\n",
				},
			},
		},
		{
			name:  "should refuse a resume after --usage was added",
			stdin: idRecords(1, 2),
			runs: []resumeRun{
				{
					args:     []string{"-i", "jsonl", "-o", "json"},
					input:    idRecords(1, 1),
					wantFile: "{\"model\":\"m\",\"answer\":{\"value\":0.5}}\n",
					wantSent: []string{`{"id":1}`},
				},
				{
					args:       []string{"-i", "jsonl", "-o", "json", "--resume", "--usage"},
					wantCode:   ExitUsage,
					wantFile:   "{\"model\":\"m\",\"answer\":{\"value\":0.5}}\n",
					wantStderr: "--usage changed since",
				},
			},
		},
		{
			name:  "should ask a trailing stored error line again under --stop-on=error and carry on",
			stdin: idRecords(1, 4),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "values", "--retries", "0", "--stop-on=error"},
					failFrom:   2,
					failOnce:   true,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitUnavailable,
					wantFile:   "{\"answer\":0.5}\n" + unavailableBody,
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stop-on=error", "--stats",
					},
					wantFile:   strings.Repeat("{\"answer\":0.5}\n", 4),
					wantSent:   []string{`{"id":2}`, `{"id":3}`, `{"id":4}`},
					wantStderr: "4 records, 1 skipped by --resume, ",
				},
			},
		},
		{
			name:  "should write the error line once again when the record asked again fails again under --stop-on=error",
			stdin: idRecords(1, 4),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "values", "--retries", "0", "--stop-on=error"},
					failFrom:   2,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitUnavailable,
					wantFile:   "{\"answer\":0.5}\n" + unavailableBody,
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args:       []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stop-on=error"},
					failFrom:   1,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitUnavailable,
					wantFile:   "{\"answer\":0.5}\n" + unavailableBody,
					wantSent:   []string{`{"id":2}`},
					wantStderr: "onesie: 503 bad key",
				},
			},
		},
		{
			name:  "should ask a trailing stored json error line again under --stop-on=error",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "json", "--retries", "0"},
					input:      idRecords(1, 2),
					failFrom:   2,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitRecords,
					wantFile:   "{\"model\":\"m\",\"answer\":{\"value\":0.5}}\n" + unavailableBody,
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "json", "--resume", "--retries", "0", "--stop-on=error"},
					wantFile: strings.Repeat("{\"model\":\"m\",\"answer\":{\"value\":0.5}}\n", 3),
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				},
			},
		},
		{
			name:  "should stop at a stored error line followed by more lines under --stop-on=error with the code a fresh run gave",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "values", "--retries", "0"},
					failFrom:   2,
					failOnce:   true,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitRecords,
					wantFile:   "{\"answer\":0.5}\n" + unavailableBody + "{\"answer\":0.5}\n",
					wantSent:   []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stop-on=error"},
					wantCode: ExitUnavailable,
					wantFile: "{\"answer\":0.5}\n" + unavailableBody + "{\"answer\":0.5}\n",
					wantStderr: "onesie: 503 bad key. The stored error for record 2 is followed by more lines, " +
						"so a run without --stop-on=error wrote it. Pass --id or drop --stop-on=error to carry on",
				},
			},
		},
		{
			name:  "should stop at the first of two trailing stored error lines under --stop-on=error",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "values", "--retries", "0"},
					failFrom:   2,
					failStatus: http.StatusBadRequest,
					wantCode:   ExitRecords,
					wantFile:   "{\"answer\":0.5}\n" + failedBody + failedBody,
					wantSent:   []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
				},
				{
					args:       []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stop-on=error"},
					wantCode:   ExitUsage,
					wantFile:   "{\"answer\":0.5}\n" + failedBody + failedBody,
					wantStderr: "The stored error for record 2 is followed by more lines",
				},
			},
		},
		{
			name:  "should ask a trailing stored error under the merge key again under --stop-on=error",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--retries", "0", "--merge", "--merge-key", "verdict",
					},
					input:      idRecords(1, 2),
					failFrom:   2,
					failStatus: http.StatusBadRequest,
					wantCode:   ExitRecords,
					wantFile: "{\"id\":1,\"verdict\":{\"answer\":0.5}}\n" +
						`{"id":2,"verdict":{"error":{"kind":"http","status":400,"message":"400 bad key"}}}` + "\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--merge", "--merge-key", "verdict",
						"--stop-on=error",
					},
					wantFile: "{\"id\":1,\"verdict\":{\"answer\":0.5}}\n{\"id\":2,\"verdict\":{\"answer\":0.5}}\n" +
						"{\"id\":3,\"verdict\":{\"answer\":0.5}}\n",
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				},
			},
		},
		{
			name:  "should ask a trailing stored error line again under --stop-on=error under -i request",
			stdin: requestRecords(1, 3),
			runs: []resumeRun{
				{
					args:       []string{"-i", "request", "--retries", "0"},
					bare:       true,
					input:      requestRecords(1, 2),
					failFrom:   2,
					failStatus: http.StatusBadRequest,
					wantCode:   ExitRecords,
					wantFile:   answeredBody + failedBody,
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args:     []string{"-i", "request", "--resume", "--retries", "0", "--stop-on=error"},
					bare:     true,
					wantFile: strings.Repeat(answeredBody, 3),
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				},
			},
		},
		{
			name:  "should stop at a stored error line followed by more lines under --stop-on=error under -i request",
			stdin: requestRecords(1, 3),
			runs: []resumeRun{
				{
					args:       []string{"-i", "request", "--retries", "0"},
					bare:       true,
					failFrom:   2,
					failOnce:   true,
					failStatus: http.StatusBadRequest,
					wantCode:   ExitRecords,
					wantFile:   answeredBody + failedBody + answeredBody,
					wantSent:   []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
				},
				{
					args:     []string{"-i", "request", "--resume", "--retries", "0", "--stop-on=error"},
					bare:     true,
					wantCode: ExitUsage,
					wantFile: answeredBody + failedBody + answeredBody,
					wantStderr: "onesie: 400 bad key. The stored error for record 2 is followed by more lines, " +
						"so a run without --stop-on=error wrote it. Pass --id or drop --stop-on=error to carry on",
				},
			},
		},
		{
			name:  "should ask a trailing stored csv error row again under --stop-on=error",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "csv", "--retries", "0", "--stop-on=error"},
					failFrom:   2,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitUnavailable,
					wantFile:   "answer,error\n0.5,\n,503 bad key\n",
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "csv", "--resume", "--retries", "0", "--stop-on=error"},
					wantFile: "answer,error\n0.5,\n0.5,\n0.5,\n",
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				},
			},
		},
		{
			name:  "should keep the header when the only stored tsv row is an error asked again under --stop-on=error",
			stdin: idRecords(1, 2),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "tsv", "--retries", "0", "--stop-on=error"},
					failFrom:   1,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitUnavailable,
					wantFile:   "answer\terror\n\t503 bad key\n",
					wantSent:   []string{`{"id":1}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "tsv", "--resume", "--retries", "0", "--stop-on=error"},
					wantFile: "answer\terror\n0.5\t\n0.5\t\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
			},
		},
		{
			name:  "should resume a csv file holding no error row under --stop-on=error",
			stdin: idRecords(1, 2),
			runs: []resumeRun{
				{
					args:     []string{"-i", "jsonl", "-o", "csv"},
					input:    idRecords(1, 1),
					wantFile: "answer,error\n0.5,\n",
					wantSent: []string{`{"id":1}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "csv", "--resume", "--stop-on=error"},
					wantFile: "answer,error\n0.5,\n0.5,\n",
					wantSent: []string{`{"id":2}`},
				},
			},
		},
		{
			name:  "should refuse a stored csv error row followed by more rows under --stop-on=error",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "csv", "--retries", "0"},
					failFrom:   2,
					failOnce:   true,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitRecords,
					wantFile:   "answer,error\n0.5,\n,503 bad key\n0.5,\n",
					wantSent:   []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "csv", "--resume", "--retries", "0", "--stop-on=error"},
					wantCode: ExitUsage,
					wantFile: "answer,error\n0.5,\n,503 bad key\n0.5,\n",
					wantStderr: "onesie: the stored error for record 2 is followed by more rows, so a run without " +
						"--stop-on=error wrote it, and a csv row keeps only its message, not the code to stop with. " +
						"Pass --id or drop --stop-on=error to carry on",
				},
			},
		},
		{
			name:  "should count a skipped error line as failed on a resume by position under -i request",
			stdin: requestRecords(1, 2),
			runs: []resumeRun{
				{
					args:       []string{"-i", "request", "--retries", "0"},
					bare:       true,
					failFrom:   2,
					failStatus: http.StatusBadRequest,
					wantCode:   ExitRecords,
					wantFile:   answeredBody + failedBody,
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args:       []string{"-i", "request", "--resume", "--retries", "0", "--stats"},
					bare:       true,
					wantCode:   ExitRecords,
					wantFile:   answeredBody + failedBody,
					wantStderr: "2 skipped by --resume, 1 error, ",
				},
			},
		},
		{
			name:     "should not count a stored response body with an error key of its own as failed under -i request",
			existing: fileOf(`{"error":{"code":502,"message":"upstream"}}` + "\n"),
			sidecar:  forwarded,
			stdin:    requestRecords(1, 2),
			runs: []resumeRun{{
				args:       []string{"-i", "request", "--resume", "--stats"},
				bare:       true,
				wantFile:   `{"error":{"code":502,"message":"upstream"}}` + "\n" + answeredBody,
				wantSent:   []string{`{"id":2}`},
				wantStderr: "1 skipped by --resume, ",
			}},
		},
		{
			name:     "should exit 1 over a skipped abstain when a record asked this run failed its assertion",
			existing: fileOf("{\"verdict\":\"unsure\",\"answer\":0.5}\n"),
			sidecar:  byPositionFailing,
			stdin:    idRecords(1, 2),
			runs: []resumeRun{{
				args: []string{
					"-i", "jsonl", "-o", "values", "--resume", "--stats",
					"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.6",
				},
				wantCode:   ExitFail,
				wantFile:   "{\"verdict\":\"unsure\",\"answer\":0.5}\n{\"verdict\":\"fail\",\"answer\":0.5}\n",
				wantSent:   []string{`{"id":2}`},
				wantStderr: "1 skipped by --resume, 1 fail, 1 unsure, ",
			}},
		},
		{
			name:     "should stop at a skipped false assertion under --stop-on=fail and ask nothing after it",
			existing: fileOf(stored),
			sidecar:  byPositionAbstaining,
			stdin:    idRecords(1, 4),
			runs: []resumeRun{{
				args: []string{
					"-i", "jsonl", "-o", "values", "--resume", "--stop-on=fail",
					"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4",
				},
				wantCode: ExitFail,
				wantFile: stored,
			}},
		},
		{
			name:     "should stop at a skipped unsure under --stop-on=unsure and ask nothing after it",
			existing: fileOf(stored),
			sidecar:  byPositionAbstaining,
			stdin:    idRecords(1, 4),
			runs: []resumeRun{{
				args: []string{
					"-i", "jsonl", "-o", "values", "--resume", "--stop-on=unsure",
					"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4",
				},
				wantCode: ExitAbstain,
				wantFile: stored,
			}},
		},
		{
			name:  "should exit 1 on a resume by position whose skipped records failed their assertion",
			stdin: idRecords(1, 2),
			runs: []resumeRun{
				{
					args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--assert", "answer.value > 0.9"},
					wantCode: ExitFail,
					wantFile: strings.Repeat("{\"verdict\":\"fail\",\"answer\":0.5}\n", 2),
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--assert", "answer.value > 0.9", "--stats",
					},
					wantCode:   ExitFail,
					wantFile:   strings.Repeat("{\"verdict\":\"fail\",\"answer\":0.5}\n", 2),
					wantStderr: "2 records, 2 skipped by --resume, 2 fail, ",
				},
			},
		},
		{
			name:  "should exit 7 on a resume by position whose skipped records abstained",
			stdin: idRecords(1, 2),
			runs: []resumeRun{
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume",
						"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4",
					},
					wantCode: ExitAbstain,
					wantFile: strings.Repeat("{\"verdict\":\"unsure\",\"answer\":0.5}\n", 2),
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume",
						"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4", "--stats",
					},
					wantCode:   ExitAbstain,
					wantFile:   strings.Repeat("{\"verdict\":\"unsure\",\"answer\":0.5}\n", 2),
					wantStderr: "2 records, 2 skipped by --resume, 2 unsure, ",
				},
			},
		},
		{
			name:     "should count a stored line that claims an error, a fail and an unsure once, as an error",
			existing: fileOf(contradictory),
			sidecar:  byPositionGated,
			stdin:    idRecords(1, 2),
			runs: []resumeRun{{
				args:       []string{"-i", "jsonl", "-o", "values", "--resume", "--stats", "--assert", "answer.value > 0.9"},
				wantCode:   ExitRecords,
				wantFile:   contradictory + "{\"verdict\":\"fail\",\"answer\":0.5}\n",
				wantSent:   []string{`{"id":2}`},
				wantStderr: "2 records, 1 skipped by --resume, 1 error, 1 fail, 1 request, ",
			}},
		},
		{
			name:     "should read past a stored line that claims an error and a fail under --stop-on=fail",
			existing: fileOf(contradictory + "{\"answer\":0.5}\n"),
			sidecar:  byPositionGated,
			stdin:    idRecords(1, 3),
			runs: []resumeRun{{
				args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--stop-on=fail", "--assert", "answer.value > 0.9"},
				wantCode: ExitRecords,
				wantFile: contradictory + "{\"answer\":0.5}\n{\"verdict\":\"fail\",\"answer\":0.5}\n",
				wantSent: []string{`{"id":3}`},
			}},
		},
		{
			name:     "should stop at a stored line that claims an error and a fail under --stop-on=error",
			existing: fileOf(contradictory + "{\"answer\":0.5}\n"),
			sidecar:  byPositionGated,
			stdin:    idRecords(1, 3),
			runs: []resumeRun{{
				args:       []string{"-i", "jsonl", "-o", "values", "--resume", "--stop-on=error", "--assert", "answer.value > 0.9"},
				wantCode:   ExitUnavailable,
				wantFile:   contradictory + "{\"answer\":0.5}\n",
				wantStderr: "The stored error for record 1 is followed by more lines",
			}},
		},
		{
			name:  "should count only the skipped records of a resume by position, asking the rest",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--assert", "answer.value > 0.9"},
					input:    idRecords(1, 1),
					wantCode: ExitFail,
					wantFile: "{\"verdict\":\"fail\",\"answer\":0.5}\n",
					wantSent: []string{`{"id":1}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--assert", "answer.value > 0.9", "--stats",
					},
					wantCode:   ExitFail,
					wantFile:   strings.Repeat("{\"verdict\":\"fail\",\"answer\":0.5}\n", 3),
					wantSent:   []string{`{"id":2}`, `{"id":3}`},
					wantStderr: "3 records, 1 skipped by --resume, 3 fail, ",
				},
			},
		},
	})
}

type resumeCase struct {
	name      string
	existing  *string
	sidecar   string
	stdin     string
	stalePart bool
	held      bool
	link      bool
	runs      []resumeRun
}

func runResumeCases(t *testing.T, tests []resumeCase) {
	t.Helper()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.link && runtime.GOOS == "windows" {
				t.Skip("creating a symlink on windows needs a privilege the test may not have")
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "answers.jsonl")
			if tc.existing != nil {
				if err := os.WriteFile(path, []byte(*tc.existing), 0o600); err != nil {
					t.Fatalf("writing the existing file: %v", err)
				}
			}

			through := path
			if tc.link {
				through = filepath.Join(dir, "link.jsonl")
				if err := os.Symlink(path, through); err != nil {
					t.Fatalf("linking: %v", err)
				}
			}

			writeSidecar(t, path+".onesie", tc.sidecar, false, false)

			if tc.stalePart {
				if err := os.WriteFile(path+".onesie.part", []byte("stale\n"), 0o600); err != nil {
					t.Fatalf("writing the stale compaction file: %v", err)
				}
			}

			if tc.held {
				held, err := lockAnswers(path)
				if err != nil {
					t.Fatalf("holding the lock: %v", err)
				}

				t.Cleanup(func() {
					if err := held.Release(); err != nil {
						t.Errorf("releasing the held lock: %v", err)
					}
				})
			}

			for i, run := range tc.runs {
				runResume(t, fmt.Sprintf("run %d", i+1), through, tc.stdin, run)
			}

			if info, err := os.Lstat(through); err != nil || (info.Mode()&os.ModeSymlink != 0) != tc.link {
				t.Errorf("%s = %v, %v, want a symlink %v", filepath.Base(through), info, err, tc.link)
			}

			if _, err := os.Lstat(through + ".onesie"); tc.link && !os.IsNotExist(err) {
				t.Errorf("a fingerprint was written beside the link rather than its target: %v", err)
			}

			if !tc.held {
				assertUnlocked(t, path)
			}

			if _, err := os.Stat(path + ".onesie.part"); !os.IsNotExist(err) {
				t.Errorf("temporary compaction file left behind: %v", err)
			}

			if tc.existing != nil && runtime.GOOS != "windows" {
				assertMode(t, path, 0o600)
			}
		})
	}
}

type resumeRun struct {
	args       []string
	bare       bool
	input      string
	failFrom   int32
	failOnce   bool
	failStatus int
	cancelAt   int32
	reversed   bool
	wantCode   int
	wantFile   string
	wantSent   []string
	anyOrder   bool
	wantStderr string
}

func runResume(t *testing.T, label, path, stdin string, run resumeRun) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	srv, sent := countingServer(t, run, func(call int32) {
		if call == run.cancelAt {
			cancel()
		}
	})

	if run.input != "" {
		stdin = run.input
	}

	var out, errOut bytes.Buffer

	root := NewRootCmd(
		BuildInfo{Version: "1.2.3"},
		WithStdin(strings.NewReader(stdin)),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		withKeychain(noKeychain()),
		WithLookupEnv(lookupFrom(nil)),
		WithClientFactory(stubFactory(srv.URL)),
	)

	root.Command.SetOut(&out)
	root.Command.SetErr(&errOut)
	question := []string{"is this urgent"}
	if run.bare {
		question = nil
	}

	root.Command.SetArgs(append(append(question, "--out", path), run.args...))

	if code := root.Execute(ctx); code != run.wantCode {
		t.Fatalf("%s: exit code = %d, want %d\nstderr:\n%s", label, code, run.wantCode, errOut.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: reading the file: %v", label, err)
	}

	if got, want := withoutLines(string(data)), withoutLines(run.wantFile); got != want {
		t.Errorf("%s: file = %q, want %q", label, got, want)
	}

	got := sent()
	if run.anyOrder {
		slices.Sort(got)
	}

	if !slices.Equal(got, run.wantSent) {
		t.Errorf("%s: sent = %q, want %q", label, got, run.wantSent)
	}

	if !strings.Contains(errOut.String(), run.wantStderr) {
		t.Errorf("%s: stderr = %q, want it to contain %q", label, errOut.String(), run.wantStderr)
	}
}

func countingServer(t *testing.T, run resumeRun, onCall func(call int32)) (*httptest.Server, func() []string) {
	t.Helper()

	var (
		calls atomic.Int32
		mu    sync.Mutex
		sent  []string
	)

	order := newReverseOrder(len(run.wantSent))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)

		var body struct {
			State json.RawMessage `json:"state"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding the request: %v", err)
		}

		mu.Lock()
		sent = append(sent, string(body.State))
		mu.Unlock()

		// After the record, since the cancel lets the run end and the test read what was sent
		// before this handler returns.
		onCall(call)

		if run.reversed {
			var state struct {
				ID int `json:"id"`
			}

			if err := json.Unmarshal(body.State, &state); err != nil {
				t.Errorf("reading the record id: %v", err)
			}

			// Later records answer first, so the order the lines land in is not input order.
			order.await(state.ID)
			defer order.answered(state.ID, w)
		}

		w.Header().Set("Content-Type", "application/json")

		if run.failFrom > 0 && call >= run.failFrom && (!run.failOnce || call == run.failFrom) {
			status := run.failStatus
			if status == 0 {
				status = http.StatusUnauthorized
			}

			w.WriteHeader(status)

			if _, err := io.WriteString(w, `{"error":{"message":"bad key"}}`); err != nil {
				t.Errorf("writing the stub response: %v", err)
			}

			return
		}

		body200 := `{"model":"m","answers":{"answer":{"type":"noul","noul":0.5}},"usage":{}}`
		if _, err := io.WriteString(w, body200); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()

		return slices.Clone(sent)
	}
}

func newReverseOrder(records int) *reverseOrder {
	order := &reverseOrder{all: make(chan struct{}), done: map[int]chan struct{}{}, records: records}
	for id := 1; id <= records; id++ {
		order.done[id] = make(chan struct{})
	}

	return order
}

type reverseOrder struct {
	mu      sync.Mutex
	arrived int
	records int
	all     chan struct{}
	done    map[int]chan struct{}
}

func (o *reverseOrder) await(id int) {
	o.mu.Lock()
	o.arrived++
	if o.arrived == o.records {
		close(o.all)
	}
	o.mu.Unlock()

	<-o.all

	if next, found := o.done[id+1]; found {
		<-next
	}
}

func (o *reverseOrder) answered(id int, w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	close(o.done[id])
}

func idRecords(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"id\":%d}\n", id)
	}

	return lines.String()
}

func requestRecords(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"questions\":{\"answer\":{\"type\":\"noul\",\"instructions\":\"is this urgent\"}},\"state\":{\"id\":%d}}\n", id)
	}

	return lines.String()
}

func fileOf(s string) *string {
	return &s
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the mode of %s: %v", filepath.Base(path), err)
	}

	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %o, want %o", filepath.Base(path), got, want)
	}
}

func assertUnlocked(t *testing.T, path string) {
	t.Helper()

	held, err := lockAnswers(path)
	if err != nil {
		t.Fatalf("the run left %s locked: %v", filepath.Base(path), err)
	}

	if err := held.Release(); err != nil {
		t.Fatalf("releasing the lock: %v", err)
	}

	if _, err := os.Stat(path + ".onesie.lock"); !os.IsNotExist(err) {
		t.Errorf("lock file left behind: %v", err)
	}
}

func lockAnswers(path string) (*answersfile.File, error) {
	held, _, err := answersfile.Open(answersfile.Options{}, answersfile.Config{Path: path})

	return held, err
}

func passedLines(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"id\":%d,\"verdict\":\"pass\",\"answer\":0.5}\n", id)
	}

	return lines.String()
}

func failedLines(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"id\":%d,\"verdict\":\"fail\",\"answer\":0.5}\n", id)
	}

	return lines.String()
}

func unsureLines(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"id\":%d,\"verdict\":\"unsure\",\"answer\":0.5}\n", id)
	}

	return lines.String()
}

func withoutLines(text string) string {
	// A stream numbers its records by input line, which TestStream pins. These tests are about which
	// records a run keeps, asks and stores, so they compare the records without it.
	return lineKey.ReplaceAllString(text, "")
}
