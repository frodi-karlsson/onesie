package cli

import (
	"bytes"
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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/output"
)

func TestResumeLedger(t *testing.T) {
	t.Parallel()

	byID := fingerprintFor(t, "is this urgent", "typesafe", jev.DefaultModel, "", ".id")
	byPosition := fingerprintFor(t, "is this urgent", "typesafe", jev.DefaultModel, "", "")
	byItself := fingerprintFor(t, "is this urgent", "typesafe", jev.DefaultModel, "", ".")
	byStateKey := fingerprintFor(t, "is this urgent", "typesafe", jev.DefaultModel, "", ".state")

	values := []string{"-i", "jsonl", "-o", "values", "--id", ".id", "--resume"}
	csvCR := "id,answer,error\n,,\"line 1: --id: \"\"a\\rb\"\" holds a carriage return, which -o csv cannot read back\"\n" +
		"\"a\nb\",0.5,\n"
	dupError := `{"error":{"kind":"input","status":null,"message":"line 2: --id: '1' is also the id of line 1"}}`

	tests := []struct {
		name      string
		existing  *string
		sidecar   string
		stdin     string
		stalePart bool
		held      bool
		runs      []resumeRun
	}{
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
			sidecar:  byID,
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
			sidecar:  byID,
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
			sidecar:  byItself,
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

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "answers.jsonl")
			if tc.existing != nil {
				if err := os.WriteFile(path, []byte(*tc.existing), 0o600); err != nil {
					t.Fatalf("writing the existing file: %v", err)
				}
			}

			writeSidecar(t, path+".onesie", tc.sidecar, false, false)

			if tc.stalePart {
				if err := os.WriteFile(path+compactSuffix, []byte("stale\n"), 0o600); err != nil {
					t.Fatalf("writing the stale compaction file: %v", err)
				}
			}

			if tc.held {
				release, err := lockAnswers(path)
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
				runResume(t, fmt.Sprintf("run %d", i+1), path, tc.stdin, run)
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
	input      string
	failFrom   int32
	slow       bool
	wantCode   int
	wantFile   string
	wantSent   []string
	anyOrder   bool
	wantStderr string
}

func runResume(t *testing.T, label, path, stdin string, run resumeRun) {
	t.Helper()

	srv, sent := countingServer(t, run.failFrom, run.slow)

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
	root.SetArgs(append([]string{"is this urgent", "--out", path}, run.args...))

	if code := Execute(t.Context(), root); code != run.wantCode {
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

func countingServer(t *testing.T, failFrom int32, slow bool) (*httptest.Server, func() []string) {
	t.Helper()

	var (
		calls atomic.Int32
		mu    sync.Mutex
		sent  []string
	)

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

		if slow {
			var state struct {
				ID int `json:"id"`
			}

			if err := json.Unmarshal(body.State, &state); err == nil {
				// Later records answer first, so the order the lines land in is not input order.
				time.Sleep(time.Duration(10-state.ID) * 15 * time.Millisecond)
			}
		}

		w.Header().Set("Content-Type", "application/json")

		if failFrom > 0 && call >= failFrom {
			w.WriteHeader(http.StatusUnauthorized)

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

func TestOutFile_Finish(t *testing.T) {
	t.Parallel()

	const appended = "{\"id\":2,\"answer\":0.5}\n{\"id\":1,\"answer\":0.5}\n"

	const compacted = "{\"id\":1,\"answer\":0.5}\n{\"id\":2,\"answer\":0.5}\n"

	failed := errors.New("disk full")

	tests := []struct {
		name        string
		rename      func(string, string) error
		open        func(string, int, os.FileMode) (*os.File, error)
		link        bool
		goos        string
		wantErr     string
		wantFile    string
		wantDirSync bool
	}{
		{
			name:        "should rewrite the file in input order through a rename",
			wantFile:    compacted,
			wantDirSync: true,
		},
		{
			name: "should leave the uncompacted file when the rename fails",
			rename: func(from, to string) error {
				if strings.HasSuffix(from, compactSuffix) {
					return failed
				}

				return os.Rename(from, to)
			},
			wantErr:  "disk full",
			wantFile: appended,
		},
		{
			name: "should leave the uncompacted file when the temporary file cannot be opened",
			open: func(name string, flag int, perm os.FileMode) (*os.File, error) {
				if strings.HasSuffix(name, compactSuffix) {
					return nil, failed
				}

				return os.OpenFile(name, flag, perm)
			},
			wantErr:  "disk full",
			wantFile: appended,
		},
		{
			name:        "should write through a symlink at the path and leave the link in place",
			link:        true,
			wantFile:    compacted,
			wantDirSync: true,
		},
		{
			name:     "should not flush the directory on windows, which cannot",
			goos:     "windows",
			wantFile: compacted,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.link && runtime.GOOS == "windows" {
				t.Skip("creating a symlink on windows needs a privilege the test may not have")
			}

			dir := t.TempDir()
			path := filepath.Join(dir, "answers.jsonl")
			target := path

			if tc.link {
				target = filepath.Join(dir, "target.jsonl")
				if err := os.Symlink(target, path); err != nil {
					t.Fatalf("linking: %v", err)
				}
			}

			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatalf("writing the existing file: %v", err)
			}

			rename := tc.rename
			if rename == nil {
				rename = os.Rename
			}

			open := tc.open
			if open == nil {
				open = os.OpenFile
			}

			goos := tc.goos
			if goos == "" {
				goos = "linux"
			}

			resolvedDir, resolveErr := filepath.EvalSymlinks(dir)
			if resolveErr != nil {
				t.Fatalf("resolving the directory: %v", resolveErr)
			}

			var dirSynced atomic.Bool

			out := &outFile{
				path: path,
				open: func(name string, flag int, perm os.FileMode) (*os.File, error) {
					if name == resolvedDir {
						dirSynced.Store(true)
					}

					return open(name, flag, perm)
				},
				rename:  rename,
				remove:  os.Remove,
				resolve: filepath.EvalSymlinks,
				goos:    goos,
				resume:  true,
			}
			if err := out.bind("v1:" + strings.Repeat("a", 64)); err != nil {
				t.Fatalf("bind: %v", err)
			}

			if _, err := io.WriteString(out, appended); err != nil {
				t.Fatalf("write: %v", err)
			}

			half := int64(len(appended) / 2)
			out.compactInto([]span{{start: half, end: 2 * half}, {start: 0, end: half}}, output.Values)

			err := out.finish(nil)
			if !strings.Contains(fmt.Sprint(err), tc.wantErr) || (tc.wantErr == "") != (err == nil) {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}

			assertFileHolds(t, target, tc.wantFile)

			if runtime.GOOS != "windows" {
				assertMode(t, target, 0o600)
			}

			if info, statErr := os.Lstat(path); statErr != nil || (info.Mode()&os.ModeSymlink != 0) != tc.link {
				t.Errorf("path = %v, %v, want a symlink %v", info, statErr, tc.link)
			}

			for _, beside := range []string{path, target} {
				if _, statErr := os.Stat(beside + compactSuffix); !os.IsNotExist(statErr) {
					t.Errorf("temporary compaction file left behind: %v", statErr)
				}
			}

			if dirSynced.Load() != tc.wantDirSync {
				t.Errorf("directory flushed = %v, want %v", dirSynced.Load(), tc.wantDirSync)
			}
		})
	}
}

func idRecords(from, to int) string {
	var lines strings.Builder
	for id := from; id <= to; id++ {
		fmt.Fprintf(&lines, "{\"id\":%d}\n", id)
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

	release, err := lockAnswers(path)
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

			book := &ledger{answered: map[ledgerKey]span{}}
			book.note("9", span{start: 0, end: 10})
			book.note("1", span{start: 10, end: 20})

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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reader := &countingReaderAt{source: strings.NewReader(source)}

			var out bytes.Buffer
			if err := copyLines(&out, reader, int64(len(source)), tc.header, tc.lines); err != nil {
				t.Fatalf("copyLines: %v", err)
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
