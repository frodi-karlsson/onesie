package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie-go"
)

const (
	pinnedOut      = "OUT"
	pinnedAnswered = `{"model":"m","usage":{"input_tokens":5,"output_tokens":2},` +
		`"answers":{"answer":{"type":"noul","noul":0.9}}}`
	pinnedRefused     = `{"error":{"message":"the key was refused"}}`
	pinnedServerFault = `{"error":{"message":"the server failed"}}`
	pinnedByID        = "{\"id\":1,\"answer\":0.1}\n{\"id\":2,\"answer\":0.8}\n{\"id\":3,\"answer\":0.3}\n" +
		"{\"id\":4,\"answer\":0.9}\n"
	pinnedRecords = "{\"id\":1,\"body\":\"a\"}\n{\"id\":2,\"body\":\"b\"}\n{\"id\":3,\"body\":\"c\"}\n" +
		"{\"id\":4,\"body\":\"d\"}\n"
)

func pinnedStreamRuns() []pinnedRun {
	byID := func(extra ...string) []string {
		return append([]string{"is it urgent", "-i", "jsonl", "--map", ".body", "--id", ".id"}, extra...)
	}
	lines := func(extra ...string) []string {
		return append([]string{"is it urgent", "-i", "lines"}, extra...)
	}

	return []pinnedRun{
		{
			name: "should keep input order under -j 1",
			steps: []pinnedStep{{
				args: byID("-o", "json"), stdin: pinnedRecords, mock: pinnedByID,
				stdout: "{\"id\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.1}}\n" +
					"{\"id\":2,\"model\":\"jev-latest\",\"answer\":{\"value\":0.8}}\n" +
					"{\"id\":3,\"model\":\"jev-latest\",\"answer\":{\"value\":0.3}}\n" +
					"{\"id\":4,\"model\":\"jev-latest\",\"answer\":{\"value\":0.9}}\n",
			}},
		},
		{
			name: "should keep input order under -j 4",
			steps: []pinnedStep{{
				args: byID("-o", "json", "-j", "4"), stdin: pinnedRecords, mock: pinnedByID,
				stdout: "{\"id\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.1}}\n" +
					"{\"id\":2,\"model\":\"jev-latest\",\"answer\":{\"value\":0.8}}\n" +
					"{\"id\":3,\"model\":\"jev-latest\",\"answer\":{\"value\":0.3}}\n" +
					"{\"id\":4,\"model\":\"jev-latest\",\"answer\":{\"value\":0.9}}\n",
			}},
		},
		{
			name: "should write every record once under --unordered and -j 4",
			steps: []pinnedStep{{
				args: byID("-o", "json", "-j", "4", "--unordered"), stdin: pinnedRecords, mock: pinnedByID,
				sorted: true,
				stdout: "{\"id\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.1}}\n" +
					"{\"id\":2,\"model\":\"jev-latest\",\"answer\":{\"value\":0.8}}\n" +
					"{\"id\":3,\"model\":\"jev-latest\",\"answer\":{\"value\":0.3}}\n" +
					"{\"id\":4,\"model\":\"jev-latest\",\"answer\":{\"value\":0.9}}\n",
			}},
		},
		{
			name: "should write an input error line and exit 6 for a blank line",
			steps: []pinnedStep{{
				args: lines("-o", "values", "--stats"), stdin: "a\n\nc\n", mock: `{"answer":0.4}`,
				stdout: "{\"line\":1,\"answer\":0.4}\n" +
					"{\"line\":2,\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 2: blank line, an empty state is a request the model cannot answer. Pass --skip-blank to drop blank lines\"}}\n" +
					"{\"line\":3,\"answer\":0.4}\n",
				stderr: "3 records, 1 failed, 2 answered from the mock, 2 questions answered, 0s\n",
				code:   ExitRecords,
			}},
		},
		{
			name: "should stop at a blank line under --stop-on-error",
			steps: []pinnedStep{{
				args: lines("-o", "json", "--stop-on-error", "-j", "4"), stdin: "a\n\nc\nd\n",
				mock: `{"answer":0.4}`,
				stdout: "{\"line\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}\n" +
					"{\"line\":2,\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 2: blank line, an empty state is a request the model cannot answer. Pass --skip-blank to drop blank lines\"}}\n",
				code: ExitRecords,
			}},
		},
		{
			name: "should drop blank lines under --skip-blank",
			steps: []pinnedStep{{
				args: lines("-o", "values", "--skip-blank"), stdin: "a\n\nc\n", mock: `{"answer":0.4}`,
				stdout: "{\"line\":1,\"answer\":0.4}\n{\"line\":3,\"answer\":0.4}\n",
			}},
		},
		{
			name: "should merge each record under --merge",
			steps: []pinnedStep{{
				args:  []string{"is it urgent", "-i", "jsonl", "--map", ".body", "--merge"},
				stdin: "{\"n\":1,\"body\":\"a\"}\n{\"n\":2,\"body\":\"b\"}\n", mock: `{"answer":0.4}`,
				stdout: "{\"n\":1,\"body\":\"a\",\"answers\":{\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}}\n" +
					"{\"n\":2,\"body\":\"b\",\"answers\":{\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}}\n",
			}},
		},
		{
			name: "should fail a record whose key --merge would overwrite",
			steps: []pinnedStep{{
				args:  []string{"is it urgent", "-i", "jsonl", "--merge"},
				stdin: "{\"answers\":1}\n{\"n\":2}\n", mock: `{"answer":0.4}`,
				stdout: "{\"state\":{\"answers\":1},\"answers\":{\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 1: --merge would overwrite the input's 'answers' key, pass --merge-key\"}}}\n" +
					"{\"n\":2,\"answers\":{\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}}\n",
				code: ExitRecords,
			}},
		},
		{
			name: "should merge into csv rows",
			steps: []pinnedStep{{
				args:  []string{"is it urgent", "-i", "csv", "--merge", "-o", "csv"},
				stdin: "n,body\n1,a\n2,b\n", mock: `{"answer":0.4}`,
				stdout: "n,body,answer,error\n1,a,0.4,\n2,b,0.4,\n",
			}},
		},
		{
			name: "should write csv with ids",
			steps: []pinnedStep{{
				args: byID("-o", "csv"), stdin: pinnedRecords, mock: pinnedByID,
				stdout: "id,answer,error\n1,0.1,\n2,0.8,\n3,0.3,\n4,0.9,\n",
			}},
		},
		{
			name: "should write tsv with ids",
			steps: []pinnedStep{{
				args: byID("-o", "tsv"), stdin: pinnedRecords, mock: pinnedByID,
				stdout: "id\tanswer\terror\n1\t0.1\t\n2\t0.8\t\n3\t0.3\t\n4\t0.9\t\n",
			}},
		},
		{
			name: "should write a markdown table and its summary",
			steps: []pinnedStep{{
				args: byID("-o", "markdown", "--assert", "answer.value < 0.5"), stdin: pinnedRecords, mock: pinnedByID,
				stdout: "| id | `answer` | gate | error |\n" +
					"|---|---|---|---|\n" +
					"| `1` | 0.1 | passed | |\n" +
					"| `2` | 0.8 | failed | |\n" +
					"| `3` | 0.3 | passed | |\n" +
					"| `4` | 0.9 | failed | |\n" +
					"\n" +
					"> [!CAUTION]\n" +
					"> 4 records: 2 passed, 2 failed.\n" +
					"\n" +
					"_`jev-latest`_\n",
				code: ExitRejected,
			}},
		},
		{
			name: "should write a markdown table cut short by --stop-on-assert",
			steps: []pinnedStep{{
				args:  byID("-o", "markdown", "--assert", "answer.value < 0.5", "--stop-on-assert"),
				stdin: pinnedRecords, mock: pinnedByID,
				stdout: "| id | `answer` | gate | error |\n" +
					"|---|---|---|---|\n" +
					"| `1` | 0.1 | passed | |\n" +
					"| `2` | 0.8 | failed | |\n" +
					"\n" +
					"> [!CAUTION]\n" +
					"> stopped after 2 records: 1 passed, 1 failed.\n" +
					"\n" +
					"_`jev-latest`_\n",
				code: ExitRejected,
			}},
		},
		{
			name: "should exit 1 for a false assertion",
			steps: []pinnedStep{{
				args: byID("-o", "json", "--assert", "answer.value < 0.5", "--stats"), stdin: pinnedRecords,
				mock: pinnedByID,
				stdout: "{\"id\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.1}}\n" +
					"{\"id\":2,\"assert\":false,\"model\":\"jev-latest\",\"answer\":{\"value\":0.8}}\n" +
					"{\"id\":3,\"model\":\"jev-latest\",\"answer\":{\"value\":0.3}}\n" +
					"{\"id\":4,\"assert\":false,\"model\":\"jev-latest\",\"answer\":{\"value\":0.9}}\n",
				stderr: "4 records answered from the mock, 2 false assertions, 4 questions answered, 0s\n",
				code:   ExitRejected,
			}},
		},
		{
			name: "should stop at the first false assertion under --stop-on-assert",
			steps: []pinnedStep{{
				args:  byID("-o", "json", "--assert", "answer.value < 0.5", "--stop-on-assert", "-j", "4"),
				stdin: pinnedRecords, mock: pinnedByID,
				stdout: "{\"id\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.1}}\n" +
					"{\"id\":2,\"assert\":false,\"model\":\"jev-latest\",\"answer\":{\"value\":0.8}}\n",
				code: ExitRejected,
			}},
		},
		{
			name: "should exit 7 when the abstain expression holds",
			steps: []pinnedStep{{
				args:  byID("-o", "values", "--assert", "answer.value < 0.5", "--abstain-if", "answer.value < 0.95"),
				stdin: pinnedRecords, mock: pinnedByID,
				stdout: "{\"id\":1,\"answer\":0.1}\n" +
					"{\"id\":2,\"abstain\":true,\"answer\":0.8}\n" +
					"{\"id\":3,\"answer\":0.3}\n" +
					"{\"id\":4,\"abstain\":true,\"answer\":0.9}\n",
				code: ExitAbstain,
			}},
		},
		{
			name: "should fail a repeated id",
			steps: []pinnedStep{{
				args:  byID("-o", "json"),
				stdin: "{\"id\":1,\"body\":\"a\"}\n{\"id\":1,\"body\":\"b\"}\n", mock: `{"answer":0.4}`,
				stdout: "{\"id\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}\n" +
					"{\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 2: --id: '1' is also the id of line 1\"}}\n",
				code: ExitRecords,
			}},
		},
		{
			name: "should fail a record --map cannot read",
			steps: []pinnedStep{{
				args:  []string{"is it urgent", "-i", "jsonl", "--map", ".body | ascii_downcase", "-o", "json"},
				stdin: "{\"body\":\"A\"}\n{\"body\":2}\n", mock: `{"answer":0.4}`,
				stdout: "{\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}\n" +
					"{\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 2: --map fails: ascii_downcase cannot be applied to: number (2)\"}}\n",
				code: ExitRecords,
			}},
		},
		{
			name: "should halt at the first record the mock does not answer",
			steps: []pinnedStep{{
				args:  byID("-o", "json", "--out", pinnedOut),
				stdin: pinnedRecords, mock: "{\"id\":1,\"answer\":0.1}\n{\"id\":3,\"answer\":0.3}\n",
				stderr: "onesie: --mock has no line with id '2', so input line 2 has no answer. Add one\n",
				code:   ExitUsage,
			}},
			file:     true,
			wantFile: "{\"id\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.1}}\n",
		},
		{
			name: "should resume by id and compact the file into input order",
			steps: []pinnedStep{
				{
					args:  byID("-o", "json", "--out", pinnedOut, "--resume"),
					stdin: "{\"id\":3,\"body\":\"c\"}\n{\"id\":1,\"body\":\"a\"}\n", mock: pinnedByID,
				},
				{
					args:  byID("-o", "json", "--out", pinnedOut, "--resume", "--stats"),
					stdin: pinnedRecords, mock: pinnedByID,
					stderr: "4 records, 2 skipped by --resume, 2 answered from the mock, 2 questions answered, 0s\n",
				},
			},
			file: true,
			wantFile: "{\"id\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.1}}\n" +
				"{\"id\":2,\"model\":\"jev-latest\",\"answer\":{\"value\":0.8}}\n" +
				"{\"id\":3,\"model\":\"jev-latest\",\"answer\":{\"value\":0.3}}\n" +
				"{\"id\":4,\"model\":\"jev-latest\",\"answer\":{\"value\":0.9}}\n",
		},
		{
			name: "should keep an answered id the input dropped until --prune",
			steps: []pinnedStep{
				{
					args: byID("-o", "json", "--out", pinnedOut, "--resume"), stdin: pinnedRecords, mock: pinnedByID,
				},
				{
					args:  byID("-o", "json", "--out", pinnedOut, "--resume", "--prune"),
					stdin: "{\"id\":2,\"body\":\"b\"}\n{\"id\":4,\"body\":\"d\"}\n", mock: pinnedByID,
				},
			},
			file: true,
			wantFile: "{\"id\":2,\"model\":\"jev-latest\",\"answer\":{\"value\":0.8}}\n" +
				"{\"id\":4,\"model\":\"jev-latest\",\"answer\":{\"value\":0.9}}\n",
		},
		{
			name: "should resume by position after a stored failure",
			steps: []pinnedStep{
				{
					args:  lines("-o", "json", "--out", pinnedOut, "--resume"),
					stdin: "a\n\nc\n", mock: `{"answer":0.4}`,
					code: ExitRecords,
				},
				{
					args:  lines("-o", "json", "--out", pinnedOut, "--resume", "--stats"),
					stdin: "a\n\nc\nd\n", mock: `{"answer":0.4}`,
					stderr: "4 records, 3 skipped by --resume, 1 failed, 1 answered from the mock, 1 question answered, 0s\n",
					code:   ExitRecords,
				},
			},
			file: true,
			wantFile: "{\"line\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}\n" +
				"{\"line\":2,\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 2: blank line, an empty state is a request the model cannot answer. Pass --skip-blank to drop blank lines\"}}\n" +
				"{\"line\":3,\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}\n" +
				"{\"line\":4,\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}\n",
		},
		{
			name: "should refuse to resume past a stored failure under --stop-on-error",
			steps: []pinnedStep{
				{
					args:  lines("-o", "json", "--out", pinnedOut, "--resume"),
					stdin: "a\n\nc\n", mock: `{"answer":0.4}`,
					code: ExitRecords,
				},
				{
					args:  lines("-o", "json", "--out", pinnedOut, "--resume", "--stop-on-error"),
					stdin: "a\n\nc\nd\n", mock: `{"answer":0.4}`,
					stderr: "onesie: line 2: blank line, an empty state is a request the model cannot answer. Pass --skip-blank to drop blank lines. The stored failure for record 2 is followed by more lines, so a run without --stop-on-error wrote it. Pass --id or drop --stop-on-error to carry on\n",
					code:   ExitRecords,
				},
			},
			file: true,
			wantFile: "{\"line\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}\n" +
				"{\"line\":2,\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 2: blank line, an empty state is a request the model cannot answer. Pass --skip-blank to drop blank lines\"}}\n" +
				"{\"line\":3,\"model\":\"jev-latest\",\"answer\":{\"value\":0.4}}\n",
		},
		{
			name: "should carry on after an interrupt",
			steps: []pinnedStep{
				{
					args:  byID("-o", "json", "--out", pinnedOut, "--resume"),
					stdin: pinnedRecords, mock: pinnedByID, interruptAfter: 1,
					code: ExitInterrupt,
				},
				{
					args:  byID("-o", "json", "--out", pinnedOut, "--resume", "--stats"),
					stdin: pinnedRecords, mock: pinnedByID,
					stderr: "4 records, 1 skipped by --resume, 3 answered from the mock, 3 questions answered, 0s\n",
				},
			},
			file: true,
			wantFile: "{\"id\":1,\"model\":\"jev-latest\",\"answer\":{\"value\":0.1}}\n" +
				"{\"id\":2,\"model\":\"jev-latest\",\"answer\":{\"value\":0.8}}\n" +
				"{\"id\":3,\"model\":\"jev-latest\",\"answer\":{\"value\":0.3}}\n" +
				"{\"id\":4,\"model\":\"jev-latest\",\"answer\":{\"value\":0.9}}\n",
		},
		{
			name: "should bill a deduplicated request once under --usage",
			steps: []pinnedStep{{
				args:  byID("-o", "json", "--usage", "--stats"),
				stdin: "{\"id\":1,\"body\":\"a\"}\n{\"id\":2,\"body\":\"a\"}\n{\"id\":3,\"body\":\"b\"}\n",
				stdout: "{\"id\":1,\"model\":\"m\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2},\"answer\":{\"value\":0.9}}\n" +
					"{\"id\":2,\"model\":\"m\",\"usage\":{\"input_tokens\":0,\"output_tokens\":0},\"answer\":{\"value\":0.9}}\n" +
					"{\"id\":3,\"model\":\"m\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2},\"answer\":{\"value\":0.9}}\n",
				stderr: "3 records, 1 deduplicated, 2 requests, 2 questions asked, 10 in / 4 out tokens, model m, 2 attempts, 10s/attempt, 0s\n",
			}},
		},
		{
			name: "should ask every record under --no-dedup",
			steps: []pinnedStep{{
				args:  byID("-o", "json", "--usage", "--no-dedup", "--stats"),
				stdin: "{\"id\":1,\"body\":\"a\"}\n{\"id\":2,\"body\":\"a\"}\n",
				stdout: "{\"id\":1,\"model\":\"m\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2},\"answer\":{\"value\":0.9}}\n" +
					"{\"id\":2,\"model\":\"m\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2},\"answer\":{\"value\":0.9}}\n",
				stderr: "2 records, 2 requests, 2 questions asked, 10 in / 4 out tokens, model m, 2 attempts, 10s/attempt, 0s\n",
			}},
		},
		{
			name: "should say once that the dedup is full",
			steps: []pinnedStep{{
				args:       byID("-o", "json", "--stats"),
				stdin:      "{\"id\":1,\"body\":\"a\"}\n{\"id\":2,\"body\":\"b\"}\n{\"id\":3,\"body\":\"a\"}\n",
				dedupLimit: 1,
				stdout: "{\"id\":1,\"model\":\"m\",\"answer\":{\"value\":0.9}}\n" +
					"{\"id\":2,\"model\":\"m\",\"answer\":{\"value\":0.9}}\n" +
					"{\"id\":3,\"model\":\"m\",\"answer\":{\"value\":0.9}}\n",
				stderr: "onesie: 1 distinct requests held, so later records are deduplicated only against those\n" +
					"3 records, 1 deduplicated, 2 requests, 2 questions asked, 10 in / 4 out tokens, model m, 2 attempts, 10s/attempt, 0s\n",
			}},
		},
		{
			name: "should write a failed request and exit 6",
			steps: []pinnedStep{{
				args: byID("-o", "json", "--stats"), stdin: pinnedRecords, status: http.StatusInternalServerError,
				stdout: "{\"id\":1,\"error\":{\"kind\":\"http\",\"status\":500,\"message\":\"500 the server failed\"}}\n" +
					"{\"id\":2,\"error\":{\"kind\":\"http\",\"status\":500,\"message\":\"500 the server failed\"}}\n" +
					"{\"id\":3,\"error\":{\"kind\":\"http\",\"status\":500,\"message\":\"500 the server failed\"}}\n" +
					"{\"id\":4,\"error\":{\"kind\":\"http\",\"status\":500,\"message\":\"500 the server failed\"}}\n",
				stderr: "4 records, 4 failed, 4 requests, 4 questions asked, 0 in / 0 out tokens, 4 attempts, 10s/attempt, 0s\n",
				code:   ExitRecords,
			}},
		},
		{
			name: "should stop at a failed request under --stop-on-error with its own exit code",
			steps: []pinnedStep{{
				args:   byID("-o", "json", "--stop-on-error"),
				stdin:  pinnedRecords,
				status: http.StatusInternalServerError,
				stdout: "{\"id\":1,\"error\":{\"kind\":\"http\",\"status\":500,\"message\":\"500 the server failed\"}}\n",
				stderr: "onesie: 500 the server failed\n",
				code:   ExitUnavailable,
			}},
		},
		{
			name: "should abort the run at a refused key",
			steps: []pinnedStep{{
				args: byID("-o", "json"), stdin: pinnedRecords, status: http.StatusUnauthorized,
				stdout: "{\"id\":1,\"error\":{\"kind\":\"http\",\"status\":401,\"message\":\"401 the key was refused\"}}\n",
				stderr: "onesie: 401 the key was refused. The key came from TYPESAFE_API_KEY\n",
				code:   ExitAuth,
			}},
		},
		{
			name: "should print each request body under --print-request",
			steps: []pinnedStep{{
				args: byID("--print-request"), stdin: pinnedRecords + "not json\n",
				stdout: "{\"state\":\"a\",\"model\":\"jev-latest\",\"questions\":{\"answer\":{\"type\":\"noul\",\"instructions\":\"is it urgent\"}}}\n" +
					"{\"state\":\"b\",\"model\":\"jev-latest\",\"questions\":{\"answer\":{\"type\":\"noul\",\"instructions\":\"is it urgent\"}}}\n" +
					"{\"state\":\"c\",\"model\":\"jev-latest\",\"questions\":{\"answer\":{\"type\":\"noul\",\"instructions\":\"is it urgent\"}}}\n" +
					"{\"state\":\"d\",\"model\":\"jev-latest\",\"questions\":{\"answer\":{\"type\":\"noul\",\"instructions\":\"is it urgent\"}}}\n" +
					"{\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 5: line is not one complete JSON value: invalid character 'o' in literal null (expecting 'u')\"}}\n",
				code: ExitRecords,
			}},
		},
	}
}

func pinnedRawRuns() []pinnedRun {
	request := `{"questions":{"answer":{"type":"noul","question":"is it urgent"}},"state":"a"}` + "\n" +
		"not json\n" +
		`{"questions":{"answer":{"type":"noul","question":"is it urgent"}},"state":"b"}` + "\n"

	return []pinnedRun{
		{
			name: "should send each raw request body under -i request",
			steps: []pinnedStep{{
				args: []string{"-i", "request", "--stats"}, stdin: request,
				stdout: "{\"model\":\"m\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2},\"answers\":{\"answer\":{\"type\":\"noul\",\"noul\":0.9}}}\n" +
					"{\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 2: line is not one complete JSON value: invalid character 'o' in literal null (expecting 'u')\"}}\n" +
					"{\"model\":\"m\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2},\"answers\":{\"answer\":{\"type\":\"noul\",\"noul\":0.9}}}\n",
				stderr: "3 records, 1 failed, 2 requests, 2 questions asked, 10 in / 4 out tokens, model m, 2 attempts, 10s/attempt, 0s\n",
				code:   ExitRecords,
			}},
		},
		{
			name: "should resume raw request bodies by position",
			steps: []pinnedStep{
				{
					args: []string{"-i", "request", "--out", pinnedOut, "--resume"}, stdin: request,
					status: http.StatusInternalServerError,
					code:   ExitRecords,
				},
				{
					args: []string{"-i", "request", "--out", pinnedOut, "--resume", "--stats"},
					stdin: request + `{"questions":{"answer":{"type":"noul","question":"is it urgent"}},"state":"c"}` +
						"\n",
					stderr: "4 records, 3 skipped by --resume, 3 failed, 1 request, 1 question asked, 5 in / 2 out tokens, model m, 1 attempt, 10s/attempt, 0s\n",
					code:   ExitRecords,
				},
			},
			file: true,
			wantFile: "{\"error\":{\"kind\":\"http\",\"status\":500,\"message\":\"500 the server failed\"}}\n" +
				"{\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 2: line is not one complete JSON value: invalid character 'o' in literal null (expecting 'u')\"}}\n" +
				"{\"error\":{\"kind\":\"http\",\"status\":500,\"message\":\"500 the server failed\"}}\n" +
				"{\"model\":\"m\",\"usage\":{\"input_tokens\":5,\"output_tokens\":2},\"answers\":{\"answer\":{\"type\":\"noul\",\"noul\":0.9}}}\n",
		},
		{
			name: "should print raw request bodies under -i request --print-request",
			steps: []pinnedStep{{
				args: []string{"-i", "request", "--print-request"}, stdin: request,
				stdout: "{\"model\":\"jev-latest\",\"questions\":{\"answer\":{\"type\":\"noul\",\"question\":\"is it urgent\"}},\"state\":\"a\"}\n" +
					"{\"error\":{\"kind\":\"input\",\"status\":null,\"message\":\"line 2: line is not one complete JSON value: invalid character 'o' in literal null (expecting 'u')\"}}\n" +
					"{\"model\":\"jev-latest\",\"questions\":{\"answer\":{\"type\":\"noul\",\"question\":\"is it urgent\"}},\"state\":\"b\"}\n",
				code: ExitRecords,
			}},
		},
	}
}

func checkPinnedRuns(t *testing.T, tests []pinnedRun) {
	t.Helper()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := filepath.Join(t.TempDir(), "answers")

			for i, step := range tc.steps {
				stdout, stderr, code := runPinned(t, step, out)
				if step.sorted {
					stdout = sortedLines(stdout)
				}

				if stdout != step.stdout || stderr != step.stderr || code != step.code {
					t.Errorf("step %d\nstdout = %q\n  want %q\nstderr = %q\n  want %q\ncode = %d, want %d",
						i+1, stdout, step.stdout, stderr, step.stderr, code, step.code)
				}
			}

			if !tc.file {
				return
			}

			if got := readText(t, out); got != tc.wantFile {
				t.Errorf("--out = %q\n  want %q", got, tc.wantFile)
			}
		})
	}
}

type pinnedRun struct {
	name     string
	steps    []pinnedStep
	file     bool
	wantFile string
}

type pinnedStep struct {
	args           []string
	stdin          string
	mock           string
	status         int
	dedupLimit     int
	interruptAfter int
	sorted         bool
	stdout         string
	stderr         string
	code           int
}

func runPinned(t *testing.T, step pinnedStep, out string) (string, string, int) {
	t.Helper()

	args := make([]string, 0, len(step.args)+2)
	for _, arg := range step.args {
		if arg == pinnedOut {
			arg = out
		}

		args = append(args, arg)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var stdin io.Reader = strings.NewReader(step.stdin)
	if step.interruptAfter > 0 {
		stdin = &cancellingReader{data: step.stdin, after: step.interruptAfter, cancel: cancel, ready: func() bool {
			data, err := os.ReadFile(out)

			return err == nil && bytes.Count(data, []byte("\n")) >= step.interruptAfter
		}}
	}

	opts := []RootOption{
		withKeychain(noKeychain()),
		WithStdin(stdin),
		WithStdinTTY(false),
		WithStdoutTTY(false),
		WithLookupEnv(lookupFrom(map[string]string{"ONESIE_CONFIG_DIR": t.TempDir()})),
		withNow(func() time.Time { return time.Unix(0, 0) }),
		WithClientFactory(pinnedFactory(t, step)),
	}

	if step.mock != "" {
		args = append(args, "--mock", writeMock(t, step.mock))
	}

	if step.dedupLimit > 0 {
		opts = append(opts, func(s *rootSettings) { s.dedupLimit = step.dedupLimit })
	}

	var stdout, stderr bytes.Buffer

	root := NewRootCmd(BuildInfo{Version: "1.2.3"}, opts...)
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)

	code := Execute(ctx, root)

	return stdout.String(), stderr.String(), code
}

func pinnedFactory(t *testing.T, step pinnedStep) func(context.Context, ...onesie.Option) (*onesie.Client, error) {
	t.Helper()

	if step.mock != "" {
		return func(context.Context, ...onesie.Option) (*onesie.Client, error) {
			t.Error("a mock run built an API client")

			return nil, os.ErrInvalid
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := pinnedAnswered
		if step.status != 0 {
			w.WriteHeader(step.status)

			body = pinnedServerFault
			if step.status == http.StatusUnauthorized {
				body = pinnedRefused
			}
		}

		if _, err := io.WriteString(w, body); err != nil {
			t.Errorf("writing the stub response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return stubFactory(srv.URL)
}

func sortedLines(text string) string {
	lines := strings.SplitAfter(text, "\n")
	slices.Sort(lines)

	return strings.Join(lines, "")
}
