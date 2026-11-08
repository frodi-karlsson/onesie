package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/creds"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
)

func TestHelpExamples(t *testing.T) {
	const records = "{\"id\":\"a\",\"text\":\"The server is down\",\"urgent\":true}\n{\"id\":\"b\",\"text\":\"Thanks for the update\",\"urgent\":false}\n"
	for _, example := range helpExamples(settingrow.CommandRoot) {
		t.Run("should execute "+example.id, func(t *testing.T) {
			args := slices.Clone(example.args)
			env := map[string]string{}
			stdin := example.stdin
			var opts []RootOption
			switch example.id {
			case "ask":
				env["ONESIE_MOCK"] = writeMock(t, `{"answer":0.9}`)
			case "pick":
				env["ONESIE_MOCK"] = writeMock(t, `{"answer":"billing"}`)
			case "enrich":
				stdin = records
				env["ONESIE_MOCK"] = writeMock(t, "{\"answer\":0.9}\n{\"answer\":0.1}\n")
			case "question-export":
			case "connection":
				args[2] = "help-example"
				stdin = "SECRET-fake-example-key\n"
				chain := workingKeychain(nil)
				opts = append(opts, withKeychain(chain))
				t.Cleanup(func() {
					if chain.calls() == 0 {
						t.Error("profile creation did not use the fake keychain")
					}
				})
			default:
				t.Fatalf("missing fixture for %s", example.id)
			}
			dir := t.TempDir()
			opts = append(opts, WithHomeDir(func() (string, error) { return dir, nil }), WithWorkingDir(func() (string, error) { return dir, nil }))
			out, errOut, code := runMocked(t, t.Context(), args, stdin, env, nil, opts...)
			if code != ExitOK {
				t.Fatalf("exit %d, stdout %s, stderr %s", code, out, errOut)
			}
			switch example.id {
			case "ask":
				if !strings.Contains(out, `"answer":{"value":0.9}`) {
					t.Fatal(out)
				}
			case "pick":
				if !strings.Contains(out, `"value":"billing"`) {
					t.Fatal(out)
				}
			case "enrich":
				lines := strings.Split(strings.TrimSpace(out), "\n")
				if len(lines) != 2 {
					t.Fatal(out)
				}
				for _, line := range lines {
					var record map[string]json.RawMessage
					if err := json.Unmarshal([]byte(line), &record); err != nil {
						t.Fatal(err)
					}
					if record["text"] == nil || record["urgent"] == nil || record["answers"] == nil {
						t.Fatal(line)
					}
				}
			case "question-export":
				if !strings.Contains(out, "urgent:") || !strings.Contains(out, "threshold: 0.5") {
					t.Fatal(out)
				}
			case "connection":
				assertNoSecret(t, out, errOut)
			}
		})
	}
	t.Run("should generate then replay the documented calibration answers without a client", func(t *testing.T) {
		dir := t.TempDir()
		answers := filepath.Join(dir, "answers.jsonl")
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				State string `json:"state"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			value := 0.1
			if body.State == "The server is down" {
				value = 0.9
			} else if body.State != "Thanks for the update" {
				t.Errorf("unexpected mapped state %s", body.State)
			}
			if err := json.NewEncoder(w).Encode(map[string]any{"model": "jev-latest", "answers": map[string]any{"urgent": map[string]any{"type": "noul", "noul": value}}}); err != nil {
				t.Error(err)
			}
		}))
		defer stub.Close()
		var stored []byte
		for _, example := range helpExamples(settingrow.CommandCalibrate) {
			args := slices.Clone(example.args)
			for i, arg := range args {
				if arg == "answers.jsonl" {
					args[i] = answers
				}
			}
			env := map[string]string{"ONESIE_CONFIG_DIR": dir}
			opts := []RootOption{WithHomeDir(func() (string, error) { return dir, nil }), WithWorkingDir(func() (string, error) { return dir, nil })}
			if example.id == "calibrate" {
				opts = append(opts, WithClientFactory(stubFactory(stub.URL)))
			}
			out, errOut, code := runMocked(t, t.Context(), args, records, env, nil, opts...)
			if code != ExitOK {
				t.Fatalf("%s exit %d, stdout %s, stderr %s", example.id, code, out, errOut)
			}
			data, err := os.ReadFile(answers)
			if err != nil {
				t.Fatal(err)
			}
			if example.id == "calibrate" {
				stored = data
				if !bytes.Contains(data, []byte(`"urgent":{"value":0.9}`)) {
					t.Fatal(string(data))
				}
			} else if !bytes.Equal(stored, data) {
				t.Fatal("offline replay rewrote saved answers")
			}
		}
	})
	for _, command := range []settingrow.Command{settingrow.CommandRoot, settingrow.CommandCalibrate} {
		t.Run("should give every "+command.String()+" example a unique id, a known destination and one input source", func(t *testing.T) {
			topics := helpTopics(command)
			ids := map[string]bool{}
			for _, example := range helpExamples(command) {
				if example.id == "" || ids[example.id] {
					t.Errorf("example id %q is empty or repeated", example.id)
				}
				ids[example.id] = true
				if example.stdin != "" && example.inputFile != "" {
					t.Errorf("example %s reads both stdin and a file", example.id)
				}
				if len(example.places) == 0 {
					t.Errorf("example %s has no destination", example.id)
				}
				for _, place := range example.places {
					if place != "overview" && place != "footer" && !slices.ContainsFunc(topics, func(topic helpTopic) bool { return topic.name == place }) {
						t.Errorf("example %s goes to unknown place %s", example.id, place)
					}
				}
			}
		})
	}
}

func TestHelpDecisions(t *testing.T) {
	t.Run("should describe the quiet threshold from its behavior owner", func(t *testing.T) {
		if !strings.Contains(helpDecisions(), "defaulting to "+strconv.FormatFloat(quietDefaultThreshold, 'f', -1, 64)) {
			t.Fatal("missing owned quiet threshold")
		}
	})
	t.Run("should describe the assert grammar the gate skill teaches", func(t *testing.T) {
		for _, want := range []string{
			"path is a question id, a dot and a field",
			`["needs review"].value`,
			"min, max, sum and avg",
			"There are no arithmetic operators",
			"mug.confidence >= 0.6",
		} {
			if !strings.Contains(helpDecisions(), want) {
				t.Errorf("help decisions lacks %q", want)
			}
		}
	})
	for _, tc := range []struct {
		name        string
		args        []string
		mock, stdin string
		code        int
		contains    []string
		lines       int
	}{
		{name: "should add a false threshold decision without rejecting", args: []string{"is this urgent", "--threshold", "0.5"}, mock: `{"answer":0.1}`, code: ExitOK, contains: []string{`"decision":false`, `"value":0.1`}},
		{name: "should reject a false quiet answer", args: []string{"is this urgent", "--quiet"}, mock: `{"answer":0.1}`, code: ExitRejected},
		{name: "should abstain only after an assertion fails", args: []string{"is this urgent", "--assert", "answer.value >= 0.5", "--abstain-if", "answer.value >= 0.05"}, mock: `{"answer":0.1}`, code: ExitAbstain, contains: []string{`"abstain":true`}},
		{name: "should retain the model value beside a low confidence fallback", args: []string{"which team", "--pick", "billing,support", "--min-confidence", "0.9", "--fallback", "review"}, mock: `{"answer":{"value":"billing","confidence":0.5,"p":{"billing":0.5,"support":0.5}}}`, code: ExitOK, contains: []string{`"value":"billing"`, `"decision":"review"`, `"fallback":"low_confidence"`}},
		{name: "should retain an error and failure exit beside a fallback", args: []string{"is this urgent", "--fallback", "no", "--retries", "0"}, mock: `{"error":503}`, code: ExitUnavailable, contains: []string{`"status":503`, `"decision":false`, `"fallback":"error"`}},
		{name: "should emit both accepted and rejected stream records", args: []string{"is this urgent", "-i", "lines", "--assert", "answer.value >= 0.5"}, stdin: "a\nb\n", mock: "{\"answer\":0.1}\n{\"answer\":0.9}\n", code: ExitRejected, lines: 2, contains: []string{`"assert":false`, `"value":0.9`}},
		{name: "should emit the first rejection before stopping", args: []string{"is this urgent", "-i", "lines", "--assert", "answer.value >= 0.5", "--stop-on-assert"}, stdin: "a\nb\n", mock: "{\"answer\":0.1}\n{\"answer\":0.9}\n", code: ExitRejected, lines: 1, contains: []string{`"assert":false`}},
		{name: "should put merged metadata at the selected location", args: []string{"is this urgent", "-i", "json", "--merge-key", "judgment"}, stdin: `{"text":"down"}`, mock: `{"answer":0.9}`, code: ExitOK, contains: []string{`"judgment":{`, `"answer":{"value":0.9}`, `"text":"down"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append(slices.Clone(tc.args), "--mock", writeMock(t, tc.mock))
			if tc.stdin == "" {
				args = append(args, "--state", "text")
			}
			out, errOut, code := runMocked(t, t.Context(), args, tc.stdin, nil, nil)
			if code != tc.code {
				t.Fatalf("exit %d, want %d, stdout %s, stderr %s", code, tc.code, out, errOut)
			}
			for _, want := range tc.contains {
				if !strings.Contains(out, want) {
					t.Errorf("missing %s in %s", want, out)
				}
			}
			if slices.Contains(tc.args, "--quiet") && out != "" {
				t.Fatal(out)
			}
			if tc.lines > 0 && strings.Count(out, "\n") != tc.lines {
				t.Fatalf("wanted %d lines, got %s", tc.lines, out)
			}
		})
	}
}

func TestHelpOutput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tty   bool
		extra []string
		stdin string
		json  bool
	}{
		{name: "should use a table for one terminal result", tty: true},
		{name: "should use JSON when redirected", json: true},
		{name: "should use JSON for a terminal stream", tty: true, extra: []string{"-i", "lines"}, stdin: "text\n", json: true},
		{name: "should use JSON for a terminal merge", tty: true, extra: []string{"-i", "json", "--merge"}, stdin: `{"text":"down"}`, json: true},
		{name: "should use JSON in an answer file beside a terminal", tty: true, extra: []string{"--out", "FILE"}, json: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "answers.jsonl")
			args := []string{"is this urgent", "--mock", writeMock(t, `{"answer":0.9}`)}
			extra := slices.Clone(tc.extra)
			for i, arg := range extra {
				if arg == "FILE" {
					extra[i] = path
				}
			}
			args = append(args, extra...)
			if tc.stdin == "" {
				args = append(args, "--state", "text")
			}
			out, errOut, code := runMocked(t, t.Context(), args, tc.stdin, nil, nil, WithStdoutTTY(tc.tty))
			if code != ExitOK {
				t.Fatalf("exit %d, %s %s", code, out, errOut)
			}
			if slices.Contains(tc.extra, "--out") {
				if out != "" {
					t.Fatal(out)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				out = string(data)
			}
			if strings.HasPrefix(out, "{") != tc.json {
				t.Fatal(out)
			}
		})
	}
}

func TestHelpExecution(t *testing.T) {
	t.Run("should describe the mock file format on both commands", func(t *testing.T) {
		for _, command := range []settingrow.Command{settingrow.CommandRoot, settingrow.CommandCalibrate} {
			for _, want := range []string{
				"A mock file is one JSON object keyed by question id",
				"Lines match records by id under --id and by position otherwise",
				`{"error":503}`, `{"error":"timeout"}`, `{"error":"connection"}`,
			} {
				if !strings.Contains(helpExecution(command), want) {
					t.Errorf("%s help execution lacks %q", command, want)
				}
			}
		}
	})
	for _, tc := range []struct {
		entry string
		code  int
	}{
		{`{"error":503}`, ExitUnavailable},
		{`{"error":"timeout"}`, ExitTransport},
		{`{"error":"connection"}`, ExitTransport},
	} {
		t.Run("should exit "+strconv.Itoa(tc.code)+" on the documented entry "+tc.entry, func(t *testing.T) {
			args := []string{"is this urgent", "--state", "text", "--retries", "0", "--mock", writeMock(t, tc.entry)}
			if _, errOut, code := runMocked(t, t.Context(), args, "", nil, nil); code != tc.code {
				t.Fatalf("exit %d, want %d, stderr %s", code, tc.code, errOut)
			}
		})
	}
}

func TestInstallHelp(t *testing.T) {
	t.Run("should render every direct help route without runtime dependencies", func(t *testing.T) {
		paths := [][]string{nil, {"--help"}, {"help"}, {"calibrate"}, {"calibrate", "--help"}, {"help", "calibrate"}, {"help", "questions"}, {"help", "profile", "create"}}
		for _, command := range []settingrow.Command{settingrow.CommandRoot, settingrow.CommandCalibrate} {
			prefix := []string{"help"}
			if command == settingrow.CommandCalibrate {
				prefix = append(prefix, "calibrate")
			}
			for _, topic := range append(helpTopics(command), helpTopic{name: "all"}) {
				paths = append(paths, append(slices.Clone(prefix), topic.name))
			}
		}
		for _, args := range paths {
			var out bytes.Buffer
			active := false
			dir := t.TempDir()
			chain := workingKeychain(nil)
			root := NewRootCmd(BuildInfo{}, withKeychain(chain), withCredentialStore(creds.NewStore(creds.WithOpen(func(string) (*os.File, error) {
				t.Error("help opened a profile or credential file")
				return nil, os.ErrNotExist
			}))), withTerminalWidth(func() (int, bool) { t.Error("help queried terminal width"); return 0, false }), WithStdin(unreadable{t: t}), WithLookupEnv(func(string) (string, bool) {
				if active {
					t.Error("help read the runtime environment")
				}
				return "", false
			}), WithHomeDir(func() (string, error) { return dir, nil }), WithWorkingDir(func() (string, error) { return dir, nil }), WithReadFile(func(string) ([]byte, error) { t.Error("help read a file"); return nil, os.ErrPermission }), WithClientFactory(func(context.Context, ...onesie.Option) (*onesie.Client, error) {
				t.Error("help built a client")
				return nil, os.ErrPermission
			}))
			root.Command.SetOut(&out)
			root.Command.SetErr(&out)
			root.Command.SetArgs(args)
			active = true
			if code := root.Execute(t.Context()); code != ExitOK {
				t.Fatalf("%v exit %d, %s", args, code, out.String())
			}
			if chain.calls() != 0 {
				t.Fatal("help read the keychain")
			}
		}
	})
	t.Run("should report an unclassified flag on stderr instead of printing empty help", func(t *testing.T) {
		var out, errOut bytes.Buffer
		root := NewRootCmd(BuildInfo{})
		version, _, err := root.Command.Find([]string{"version"})
		if err != nil {
			t.Fatal(err)
		}
		version.Flags().String("unclassified", "", "extra")
		root.Command.SetArgs([]string{"version", "--help"})
		root.Command.SetOut(&out)
		root.Command.SetErr(&errOut)
		root.Execute(t.Context())
		if out.Len() != 0 || errOut.String() != "onesie: flag --unclassified has no help metadata\n" {
			t.Fatalf("stdout %q, stderr %q", out.String(), errOut.String())
		}
	})
	t.Run("should preserve established writer failure and broken pipe handling", func(t *testing.T) {
		for _, tc := range []struct {
			args []string
			err  error
			code int
		}{{[]string{"help", "all"}, os.ErrPermission, ExitUsage}, {[]string{"help", "all"}, syscall.EPIPE, ExitOK}, {[]string{"--help"}, os.ErrPermission, ExitOK}} {
			root := NewRootCmd(BuildInfo{})
			root.Command.SetArgs(tc.args)
			root.Command.SetOut(helpErrorWriter{tc.err})
			root.Command.SetErr(io.Discard)
			if code := root.Execute(t.Context()); code != tc.code {
				t.Fatalf("%v exit %d, want %d", tc.args, code, tc.code)
			}
		}
	})
}

type helpErrorWriter struct{ err error }

func (w helpErrorWriter) Write([]byte) (int, error) { return 0, w.err }
