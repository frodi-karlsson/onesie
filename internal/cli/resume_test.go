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
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

func TestResumeLedger(t *testing.T) {
	t.Parallel()

	printed := func(input, idSource, output, mergeKey string) string {
		return fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
			provider: "typesafe", model: jev.DefaultModel, idSource: idSource, output: output,
			input: input, mergeKey: mergeKey,
		})
	}

	byID := printed("jsonl", ".id", "values", "")
	byPosition := printed("jsonl", "", "values", "")
	byItself := printed("jsonl", ".", "values", "answers")
	byItselfInLines := printed("lines", ".", "values", "answers")
	byStateKey := printed("jsonl", ".state", "values", "answers")
	byIDInTSV := printed("jsonl", ".id", "tsv", "")
	byIDInCSV := printed("jsonl", ".id", "csv", "")
	byIDMergedInCSV := printed("csv", ".id", "csv", "answers")
	abstaining := fingerprintInputs{
		provider: "typesafe", model: jev.DefaultModel, idSource: ".id", output: "values", input: "jsonl",
		assert: "answer.value > 0.9", abstainIf: "answer.value > 0.4",
	}
	byIDAbstaining := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, abstaining)
	gateArgs := []string{"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4"}
	abstaining.abstainIf = "answer.value > 0.6"
	byIDRejecting := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, abstaining)

	values := []string{"-i", "jsonl", "-o", "values", "--id", ".id", "--resume"}
	csvCR := "id,answer,error\n,,\"line 1: --id: \"\"a\\rb\"\" holds a carriage return, which -o csv cannot read back\"\n" +
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
						`{"id":3,"error":{"kind":"http","status":401,"message":"onesie: 401 bad key"}}` + "\n",
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
					`{"id":3,"error":{"kind":"http","status":401,"message":"onesie: 401 bad key"}}` + "\n",
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
				wantStderr: "is being resumed by another onesie run. Wait for it to finish",
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
				wantStderr: "is being resumed by another onesie run. Wait for it to finish",
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
				wantStderr: "is being resumed by another onesie run. Wait for it to finish",
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
						`{"id":3,"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n",
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
				slow:     true,
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
				wantStderr: "1 request, 2 skipped, ",
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
				slow:     true,
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
			existing: fileOf("{\"state\":\"a\",\"answers\":{\"answer\":0.5}}\n"),
			sidecar:  byItselfInLines,
			stdin:    "a\nb\n",
			runs: []resumeRun{{
				args: []string{"-i", "lines", "-o", "values", "--merge", "--id", ".", "--resume"},
				wantFile: "{\"state\":\"a\",\"answers\":{\"answer\":0.5}}\n" +
					"{\"state\":\"b\",\"answers\":{\"answer\":0.5}}\n",
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
					wantFile: "id\tanswer\terror\n\t\tline 1: --id: \"a\\tb\" holds a tab, carriage return or newline, which -o tsv cannot write\n",
				},
				{
					args:     []string{"-i", "jsonl", "-o", "tsv", "--id", ".id", "--resume"},
					input:    "{\"id\":\"a\\tb\"}\n{\"id\":\"a b\"}\n",
					wantCode: ExitRecords,
					wantFile: "id\tanswer\terror\n\t\tline 1: --id: \"a\\tb\" holds a tab, carriage return or newline, which -o tsv cannot write\n" + "a b\t0.5\t\n",
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
				wantFile: "id\tanswer\terror\n\t\tline 1: --id: \"a\\rb\" holds a tab, carriage return or newline, which -o tsv cannot write\n" +
					"\t\tline 2: --id: \"a\\nb\" holds a tab, carriage return or newline, which -o tsv cannot write\n",
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
					wantStderr: "0 requests, 2 skipped, 2 false assertions, ",
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
					wantStderr: "0 requests, 2 skipped, 2 abstains, ",
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
						`{"id":2,"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n",
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
					wantStderr: "1 skipped, 2 false assertions, ",
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
					wantStderr: "1 skipped, 2 false assertions, ",
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
				wantStderr: "1 skipped, 1 false assertion, 1 abstain, ",
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
						`{"id":2,"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args:       append([]string{"--retries", "0", "--stop-on-error"}, values...),
					failFrom:   1,
					failStatus: http.StatusBadRequest,
					wantCode:   ExitUsage,
					wantFile: "{\"id\":1,\"answer\":0.5}\n" +
						`{"id":2,"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n" +
						`{"id":2,"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n",
					wantSent:   []string{`{"id":2}`},
					wantStderr: "onesie: 400 bad key",
				},
			},
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
}

func TestResumedVerdicts(t *testing.T) {
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
					wantStderr: "1 skipped, 2 false assertions, ",
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
					wantStderr: "1 skipped, 2 false assertions, ",
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
						`{"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--assert", "answer.value > 0.9", "--stats",
					},
					wantCode: ExitRecords,
					wantFile: "{\"assert\":false,\"answer\":0.5}\n" +
						`{"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n",
					wantStderr: "2 skipped, 1 failed, 1 false assertion, ",
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
						`{"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stats"},
					wantCode: ExitRecords,
					wantFile: "{\"answer\":0.5}\n" +
						`{"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n",
					wantStderr: "2 skipped, 1 failed, ",
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
						`{"id":2,"verdict":{"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}}` + "\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--merge", "--merge-key", "verdict",
						"--assert", "answer.value > 0.9", "--stats",
					},
					wantCode: ExitRecords,
					wantFile: "{\"id\":1,\"verdict\":{\"assert\":false,\"answer\":0.5}}\n" +
						`{"id":2,"verdict":{"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}}` + "\n",
					wantStderr: "2 skipped, 1 failed, 1 false assertion, ",
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
					wantFile:   "answer,assert,error\n0.5,false,\n,,onesie: 400 bad key\n",
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "csv", "--resume", "--retries", "0", "--assert", "answer.value > 0.9", "--stats",
					},
					wantCode:   ExitRecords,
					wantFile:   "answer,assert,error\n0.5,false,\n,,onesie: 400 bad key\n",
					wantStderr: "2 skipped, 1 failed, 1 false assertion, ",
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
					wantFile:   "id\tbody\tanswer\terror\n1\ta\t0.5\t\n2\tb\t\tonesie: 400 bad key\n",
					wantSent:   []string{`{"id":"1","body":"a"}`, `{"id":"2","body":"b"}`},
				},
				{
					args:       []string{"-i", "tsv", "-o", "tsv", "--merge", "--resume", "--retries", "0", "--stats"},
					wantCode:   ExitRecords,
					wantFile:   "id\tbody\tanswer\terror\n1\ta\t0.5\t\n2\tb\t\tonesie: 400 bad key\n",
					wantStderr: "2 skipped, 1 failed, ",
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
}

func TestResumed(t *testing.T) {
	t.Parallel()

	answeredBody := `{"model":"m","answers":{"answer":{"type":"noul","noul":0.5}},"usage":{}}` + "\n"
	failedBody := `{"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}` + "\n"
	unavailableBody := `{"error":{"kind":"http","status":503,"message":"onesie: 503 bad key"}}` + "\n"
	forwarded, err := fingerprintOf(nil, fingerprintInputs{provider: "typesafe", input: "request"})
	if err != nil {
		t.Fatalf("fingerprinting -i request: %v", err)
	}

	byPositionAbstaining := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
		provider: "typesafe", model: jev.DefaultModel, output: "values", input: "jsonl",
		assert: "answer.value > 0.9", abstainIf: "answer.value > 0.4",
	})
	stored := "{\"abstain\":true,\"answer\":0.5}\n{\"assert\":false,\"answer\":0.5}\n"
	byPositionRejecting := fingerprintWith(t, plan.Source{Positional: "is this urgent"}, fingerprintInputs{
		provider: "typesafe", model: jev.DefaultModel, output: "values", input: "jsonl",
		assert: "answer.value > 0.9", abstainIf: "answer.value > 0.6",
	})

	runResumeCases(t, []resumeCase{
		{
			name:  "should ask a trailing stored error line again under --stop-on-error and carry on",
			stdin: idRecords(1, 4),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "values", "--retries", "0", "--stop-on-error"},
					failFrom:   2,
					failOnce:   true,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitUnavailable,
					wantFile:   "{\"answer\":0.5}\n" + unavailableBody,
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stop-on-error", "--stats",
					},
					wantFile:   strings.Repeat("{\"answer\":0.5}\n", 4),
					wantSent:   []string{`{"id":2}`, `{"id":3}`, `{"id":4}`},
					wantStderr: "3 requests, 1 skipped, ",
				},
			},
		},
		{
			name:  "should write the error line once again when the record asked again fails again under --stop-on-error",
			stdin: idRecords(1, 4),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "values", "--retries", "0", "--stop-on-error"},
					failFrom:   2,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitUnavailable,
					wantFile:   "{\"answer\":0.5}\n" + unavailableBody,
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args:       []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stop-on-error"},
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
			name:  "should ask a trailing stored json error line again under --stop-on-error",
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
					args:     []string{"-i", "jsonl", "-o", "json", "--resume", "--retries", "0", "--stop-on-error"},
					wantFile: strings.Repeat("{\"model\":\"m\",\"answer\":{\"value\":0.5}}\n", 3),
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				},
			},
		},
		{
			name:  "should stop at a stored error line followed by more lines under --stop-on-error with the code a fresh run gave",
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
					args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stop-on-error"},
					wantCode: ExitUnavailable,
					wantFile: "{\"answer\":0.5}\n" + unavailableBody + "{\"answer\":0.5}\n",
					wantStderr: "onesie: 503 bad key. The stored failure for record 2 is followed by more lines, " +
						"so a run without --stop-on-error wrote it. Pass --id or drop --stop-on-error to carry on",
				},
			},
		},
		{
			name:  "should stop at the first of two trailing stored error lines under --stop-on-error",
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
					args:       []string{"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--stop-on-error"},
					wantCode:   ExitUsage,
					wantFile:   "{\"answer\":0.5}\n" + failedBody + failedBody,
					wantStderr: "The stored failure for record 2 is followed by more lines",
				},
			},
		},
		{
			name:  "should ask a trailing stored error under the merge key again under --stop-on-error",
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
						`{"id":2,"verdict":{"error":{"kind":"http","status":400,"message":"onesie: 400 bad key"}}}` + "\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--retries", "0", "--merge", "--merge-key", "verdict",
						"--stop-on-error",
					},
					wantFile: "{\"id\":1,\"verdict\":{\"answer\":0.5}}\n{\"id\":2,\"verdict\":{\"answer\":0.5}}\n" +
						"{\"id\":3,\"verdict\":{\"answer\":0.5}}\n",
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				},
			},
		},
		{
			name:  "should ask a trailing stored error line again under --stop-on-error under -i request",
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
					args:     []string{"-i", "request", "--resume", "--retries", "0", "--stop-on-error"},
					bare:     true,
					wantFile: strings.Repeat(answeredBody, 3),
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				},
			},
		},
		{
			name:  "should stop at a stored error line followed by more lines under --stop-on-error under -i request",
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
					args:     []string{"-i", "request", "--resume", "--retries", "0", "--stop-on-error"},
					bare:     true,
					wantCode: ExitUsage,
					wantFile: answeredBody + failedBody + answeredBody,
					wantStderr: "onesie: 400 bad key. The stored failure for record 2 is followed by more lines, " +
						"so a run without --stop-on-error wrote it. Pass --id or drop --stop-on-error to carry on",
				},
			},
		},
		{
			name:  "should ask a trailing stored csv error row again under --stop-on-error",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "csv", "--retries", "0", "--stop-on-error"},
					failFrom:   2,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitUnavailable,
					wantFile:   "answer,error\n0.5,\n,onesie: 503 bad key\n",
					wantSent:   []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "csv", "--resume", "--retries", "0", "--stop-on-error"},
					wantFile: "answer,error\n0.5,\n0.5,\n0.5,\n",
					wantSent: []string{`{"id":2}`, `{"id":3}`},
				},
			},
		},
		{
			name:  "should keep the header when the only stored tsv row is an error asked again under --stop-on-error",
			stdin: idRecords(1, 2),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "tsv", "--retries", "0", "--stop-on-error"},
					failFrom:   1,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitUnavailable,
					wantFile:   "answer\terror\n\tonesie: 503 bad key\n",
					wantSent:   []string{`{"id":1}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "tsv", "--resume", "--retries", "0", "--stop-on-error"},
					wantFile: "answer\terror\n0.5\t\n0.5\t\n",
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
			},
		},
		{
			name:  "should resume a csv file holding no error row under --stop-on-error",
			stdin: idRecords(1, 2),
			runs: []resumeRun{
				{
					args:     []string{"-i", "jsonl", "-o", "csv"},
					input:    idRecords(1, 1),
					wantFile: "answer,error\n0.5,\n",
					wantSent: []string{`{"id":1}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "csv", "--resume", "--stop-on-error"},
					wantFile: "answer,error\n0.5,\n0.5,\n",
					wantSent: []string{`{"id":2}`},
				},
			},
		},
		{
			name:  "should refuse a stored csv error row followed by more rows under --stop-on-error",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args:       []string{"-i", "jsonl", "-o", "csv", "--retries", "0"},
					failFrom:   2,
					failOnce:   true,
					failStatus: http.StatusServiceUnavailable,
					wantCode:   ExitRecords,
					wantFile:   "answer,error\n0.5,\n,onesie: 503 bad key\n0.5,\n",
					wantSent:   []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
				},
				{
					args:     []string{"-i", "jsonl", "-o", "csv", "--resume", "--retries", "0", "--stop-on-error"},
					wantCode: ExitUsage,
					wantFile: "answer,error\n0.5,\n,onesie: 503 bad key\n0.5,\n",
					wantStderr: "onesie: the stored failure for record 2 is followed by more rows, so a run without " +
						"--stop-on-error wrote it, and a csv row keeps only its message, not the code to stop with. " +
						"Pass --id or drop --stop-on-error to carry on",
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
					wantStderr: "2 skipped, 1 failed, ",
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
				wantStderr: "1 skipped, ",
			}},
		},
		{
			name:     "should exit 1 over a skipped abstain when a record asked this run failed its assertion",
			existing: fileOf("{\"abstain\":true,\"answer\":0.5}\n"),
			sidecar:  byPositionRejecting,
			stdin:    idRecords(1, 2),
			runs: []resumeRun{{
				args: []string{
					"-i", "jsonl", "-o", "values", "--resume", "--stats",
					"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.6",
				},
				wantCode:   ExitRejected,
				wantFile:   "{\"abstain\":true,\"answer\":0.5}\n{\"assert\":false,\"answer\":0.5}\n",
				wantSent:   []string{`{"id":2}`},
				wantStderr: "1 skipped, 1 false assertion, 1 abstain, ",
			}},
		},
		{
			name:     "should stop at a skipped false assertion under --stop-on-assert and ask nothing after it",
			existing: fileOf(stored),
			sidecar:  byPositionAbstaining,
			stdin:    idRecords(1, 4),
			runs: []resumeRun{{
				args: []string{
					"-i", "jsonl", "-o", "values", "--resume", "--stop-on-assert",
					"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4",
				},
				wantCode: ExitRejected,
				wantFile: stored,
			}},
		},
		{
			name:  "should exit 1 on a resume by position whose skipped records failed their assertion",
			stdin: idRecords(1, 2),
			runs: []resumeRun{
				{
					args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--assert", "answer.value > 0.9"},
					wantCode: ExitRejected,
					wantFile: strings.Repeat("{\"assert\":false,\"answer\":0.5}\n", 2),
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--assert", "answer.value > 0.9", "--stats",
					},
					wantCode:   ExitRejected,
					wantFile:   strings.Repeat("{\"assert\":false,\"answer\":0.5}\n", 2),
					wantStderr: "0 requests, 2 skipped, 2 false assertions, ",
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
					wantFile: strings.Repeat("{\"abstain\":true,\"answer\":0.5}\n", 2),
					wantSent: []string{`{"id":1}`, `{"id":2}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume",
						"--assert", "answer.value > 0.9", "--abstain-if", "answer.value > 0.4", "--stats",
					},
					wantCode:   ExitAbstain,
					wantFile:   strings.Repeat("{\"abstain\":true,\"answer\":0.5}\n", 2),
					wantStderr: "0 requests, 2 skipped, 2 abstains, ",
				},
			},
		},
		{
			name:  "should count only the skipped records of a resume by position, asking the rest",
			stdin: idRecords(1, 3),
			runs: []resumeRun{
				{
					args:     []string{"-i", "jsonl", "-o", "values", "--resume", "--assert", "answer.value > 0.9"},
					input:    idRecords(1, 1),
					wantCode: ExitRejected,
					wantFile: "{\"assert\":false,\"answer\":0.5}\n",
					wantSent: []string{`{"id":1}`},
				},
				{
					args: []string{
						"-i", "jsonl", "-o", "values", "--resume", "--assert", "answer.value > 0.9", "--stats",
					},
					wantCode:   ExitRejected,
					wantFile:   strings.Repeat("{\"assert\":false,\"answer\":0.5}\n", 3),
					wantSent:   []string{`{"id":2}`, `{"id":3}`},
					wantStderr: "2 requests, 1 skipped, 3 false assertions, ",
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

			writeSidecar(t, through+".onesie", tc.sidecar, false, false)

			if tc.stalePart {
				if err := os.WriteFile(path+compactSuffix, []byte("stale\n"), 0o600); err != nil {
					t.Fatalf("writing the stale compaction file: %v", err)
				}
			}

			if tc.held {
				release, err := newLocker(runtime.GOOS).lockAnswers(path)
				if err != nil {
					t.Fatalf("holding the lock: %v", err)
				}

				t.Cleanup(func() {
					if err := release(); err != nil {
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

			if !tc.held {
				assertUnlocked(t, path)
			}

			if _, err := os.Stat(path + compactSuffix); !os.IsNotExist(err) {
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
	slow       bool
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
		WithKeychain(noKeychain()),
		WithLookupEnv(lookupFrom(nil)),
		WithClientFactory(stubFactory(srv.URL)),
	)

	root.SetOut(&out)
	root.SetErr(&errOut)
	question := []string{"is this urgent"}
	if run.bare {
		question = nil
	}

	root.SetArgs(append(append(question, "--out", path), run.args...))

	if code := Execute(ctx, root); code != run.wantCode {
		t.Fatalf("%s: exit code = %d, want %d\nstderr:\n%s", label, code, run.wantCode, errOut.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: reading the file: %v", label, err)
	}

	if string(data) != run.wantFile {
		t.Errorf("%s: file = %q, want %q", label, data, run.wantFile)
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		onCall(call)

		var body struct {
			State json.RawMessage `json:"state"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding the request: %v", err)
		}

		mu.Lock()
		sent = append(sent, string(body.State))
		mu.Unlock()

		if run.slow {
			var state struct {
				ID int `json:"id"`
			}

			if err := json.Unmarshal(body.State, &state); err == nil {
				// Later records answer first, so the order the lines land in is not input order.
				time.Sleep(time.Duration(10-state.ID) * 15 * time.Millisecond)
			}
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
		fmt.Fprintf(&lines, "{\"questions\":{\"answer\":{\"type\":\"noul\",\"question\":\"is this urgent\"}},\"state\":{\"id\":%d}}\n", id)
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

	release, err := newLocker(runtime.GOOS).lockAnswers(path)
	if err != nil {
		t.Fatalf("the run left %s locked: %v", filepath.Base(path), err)
	}

	if err := release(); err != nil {
		t.Fatalf("releasing the lock: %v", err)
	}

	if _, err := os.Stat(path + lockSuffix); !os.IsNotExist(err) {
		t.Errorf("lock file left behind: %v", err)
	}
}

func TestLedger_TakeOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		prune bool
	}{
		{name: "should hand over the order it built without copying it", prune: true},
		{name: "should hand over the order with the unasked answers after it", prune: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			book := &ledger{answered: map[ledgerKey]answeredLine{}}
			book.note("9", span{start: 0, end: 10}, verdict{})
			book.note("1", span{start: 10, end: 20}, verdict{})

			for _, id := range []string{"1", "2"} {
				rec := &namedRecord{id: id}
				book.admit(rec)
			}

			first := &book.lines[0]

			got := book.takeOrder(tc.prune)

			want := []span{{start: 10, end: 20}, {}}
			if !tc.prune {
				want = append(want, span{start: 0, end: 10})
			}

			if !slices.Equal(got, want) {
				t.Errorf("order = %v, want %v", got, want)
			}

			if tc.prune && &got[0] != first {
				t.Errorf("order was copied, want the ledger's own slice handed over")
			}

			if book.lines != nil {
				t.Errorf("ledger still holds %v, want it handed over", book.lines)
			}
		})
	}
}

func TestCopyLines(t *testing.T) {
	t.Parallel()

	var file strings.Builder

	var forward []span

	for i := range 2000 {
		start := int64(file.Len())
		fmt.Fprintf(&file, "{\"id\":%d,\"answer\":0.5}\n", i)
		forward = append(forward, span{start: start, end: int64(file.Len())})
	}

	backward := slices.Clone(forward)
	slices.Reverse(backward)

	source := file.String()

	tests := []struct {
		name      string
		header    int64
		lines     []span
		want      string
		wantReads int
		wantErr   bool
	}{
		{
			name:      "should read spans that run forward in a few large reads",
			lines:     forward,
			want:      source,
			wantReads: 10,
		},
		{
			name:      "should copy spans that run backward",
			lines:     backward,
			want:      reversedLines(source),
			wantReads: 2 * len(backward),
		},
		{
			name:      "should write the header once and skip it inside a span",
			header:    forward[0].end,
			lines:     []span{{start: 0, end: forward[1].end}, forward[2]},
			want:      source[:forward[2].end],
			wantReads: 10,
		},
		{
			name:      "should fail when a span read on its own runs past a short source",
			lines:     []span{backward[0], {start: backward[1].start, end: backward[0].end + 1}},
			wantReads: 4,
			wantErr:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := &countingReaderAt{source: strings.NewReader(source)}

			var out bytes.Buffer

			err := copyLines(&out, reader, int64(len(source)), tc.header, tc.lines)
			if (err != nil) != tc.wantErr {
				t.Fatalf("copyLines error = %v, want an error %v", err, tc.wantErr)
			}

			if tc.wantErr {
				return
			}

			if out.String() != tc.want {
				t.Errorf("copied %d bytes, want %d matching the source", out.Len(), len(tc.want))
			}

			if got := int(reader.reads.Load()); got > tc.wantReads {
				t.Errorf("reads = %d, want at most %d", got, tc.wantReads)
			}
		})
	}
}

type countingReaderAt struct {
	source io.ReaderAt
	reads  atomic.Int32
}

func (r *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	r.reads.Add(1)

	return r.source.ReadAt(p, off)
}

func reversedLines(text string) string {
	lines := strings.SplitAfter(text, "\n")
	slices.Reverse(lines)

	return strings.Join(lines, "")
}

func rejectedLines(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"id\":%d,\"assert\":false,\"answer\":0.5}\n", id)
	}

	return lines.String()
}

func abstainedLines(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"id\":%d,\"abstain\":true,\"answer\":0.5}\n", id)
	}

	return lines.String()
}
