package examples_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/frodi-karlsson/onesie/internal/cli"
)

func TestStarterSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, set string)
	}{
		{name: "should load the question file and dry run it", check: dryRunQuestions},
		{name: "should print the question file back unchanged", check: reprintQuestions},
		{name: "should read every labelled record under calibrate", check: dryRunCalibrate},
		{name: "should carry the gate the README documents", check: carriesGate},
		{name: "should refuse a gate typed beside the file under --print-request", check: refuseTypedGate},
	}

	sets := exampleSets(t)

	for _, tc := range tests {
		for _, set := range sets {
			t.Run(tc.name+" for "+set, func(t *testing.T) {
				t.Parallel()

				tc.check(t, set)
			})
		}
	}
}

func exampleSets(t *testing.T) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("questions", "*.yaml"))
	if err != nil {
		t.Fatalf("listing question files: %v", err)
	}

	if len(paths) == 0 {
		t.Fatal("no question files in questions")
	}

	sets := make([]string, 0, len(paths))
	for _, path := range paths {
		sets = append(sets, strings.TrimSuffix(filepath.Base(path), ".yaml"))
	}

	return sets
}

func dryRunQuestions(t *testing.T, set string) {
	stdout, stderr, code := run(t, strings.NewReader(""),
		"-f", questionPath(set), "--state", "a sample record", "--print-request")
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}

	var request struct {
		Questions map[string]json.RawMessage `json:"questions"`
	}

	if err := json.Unmarshal([]byte(stdout), &request); err != nil {
		t.Fatalf("decoding the request: %v\n%s", err, stdout)
	}

	got := slices.Sorted(maps.Keys(request.Questions))
	if want := questionIDs(t, set); !slices.Equal(got, want) {
		t.Errorf("request asks %v, want %v", got, want)
	}
}

func reprintQuestions(t *testing.T, set string) {
	stdout, stderr, code := run(t, strings.NewReader(""), "-f", questionPath(set), "--print-questions")
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}

	if want := readFile(t, questionPath(set)); stdout != want {
		t.Errorf("--print-questions wrote\n%s\nwant the file as it is\n%s", stdout, want)
	}
}

func dryRunCalibrate(t *testing.T, set string) {
	ids := questionIDs(t, set)
	records := readRecords(t, set)
	field := textField(t, records[0], ids)

	for _, record := range records {
		for _, id := range ids {
			if record[id] == nil {
				t.Errorf("record %v has no %s label", record["id"], id)
			}
		}
	}

	args := []string{
		"calibrate", "-f", questionPath(set), "-i", "jsonl", "--map", "." + field, "--id", ".id",
		"--print-request",
	}
	for _, id := range ids {
		args = append(args, "--label", id+"=."+id)
	}

	stdout, stderr, code := run(t, strings.NewReader(readFile(t, dataPath(set))), args...)
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}

	if got := strings.Count(stdout, "\n"); got != len(records) {
		t.Errorf("calibrate wrote %d requests for %d records", got, len(records))
	}
}

func carriesGate(t *testing.T, set string) {
	want := documentedGate(t, set)

	var file gate
	if err := yaml.Unmarshal([]byte(readFile(t, questionPath(set))), &file); err != nil {
		t.Fatalf("parsing %s: %v", questionPath(set), err)
	}

	if file != want {
		t.Errorf("assert and abstain_if = %+v, want %+v as README.md documents", file, want)
	}
}

func documentedGate(t *testing.T, set string) gate {
	t.Helper()

	_, section, found := strings.Cut(readFile(t, "README.md"), "\n## "+set+"\n")
	if !found {
		t.Fatalf("README.md has no section for %s", set)
	}

	section, _, _ = strings.Cut(section, "\n## ")

	_, block, found := strings.Cut(section, "```yaml\n")
	if !found {
		t.Fatalf("README.md shows no gate for %s", set)
	}

	block, _, _ = strings.Cut(block, "```")

	var documented gate
	if err := yaml.Unmarshal([]byte(block), &documented); err != nil {
		t.Fatalf("parsing the gate README.md shows for %s: %v", set, err)
	}

	if documented.Assert == "" || documented.AbstainIf == "" {
		t.Fatalf("the gate README.md shows for %s lacks assert or abstain_if: %+v", set, documented)
	}

	return documented
}

type gate struct {
	Assert    string `yaml:"assert"`
	AbstainIf string `yaml:"abstain_if"`
}

func refuseTypedGate(t *testing.T, set string) {
	_, stderr, code := run(t, strings.NewReader(""),
		"-f", questionPath(set), "--state", "a sample record", "--print-request",
		"--assert", documentedGate(t, set).Assert)
	if code != cli.ExitUsage {
		t.Fatalf("exit %d, want %d, stderr: %s", code, cli.ExitUsage, stderr)
	}

	if want := "onesie: --assert judges an answer, which --print-request does not produce"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
}

func questionIDs(t *testing.T, set string) []string {
	t.Helper()

	var file map[string]any
	if err := yaml.Unmarshal([]byte(readFile(t, questionPath(set))), &file); err != nil {
		t.Fatalf("parsing %s: %v", questionPath(set), err)
	}

	var ids []string

	for key, value := range file {
		if question, ok := value.(map[string]any); ok && question["ask"] != nil {
			ids = append(ids, key)
		}
	}

	slices.Sort(ids)

	return ids
}

func readRecords(t *testing.T, set string) []map[string]any {
	t.Helper()

	var records []map[string]any

	lines := bufio.NewScanner(strings.NewReader(readFile(t, dataPath(set))))
	for lines.Scan() {
		var record map[string]any
		if err := json.Unmarshal(lines.Bytes(), &record); err != nil {
			t.Fatalf("%s line %d: %v", dataPath(set), len(records)+1, err)
		}

		records = append(records, record)
	}

	if len(records) == 0 {
		t.Fatalf("%s has no records", dataPath(set))
	}

	return records
}

func textField(t *testing.T, record map[string]any, ids []string) string {
	t.Helper()

	var fields []string

	for key := range record {
		if key != "id" && !slices.Contains(ids, key) {
			fields = append(fields, key)
		}
	}

	if len(fields) != 1 {
		t.Fatalf("want one text field beside id and the labels, got %v", fields)
	}

	return fields[0]
}

func questionPath(set string) string {
	return filepath.Join("questions", set+".yaml")
}

func dataPath(set string) string {
	return filepath.Join("data", set+".jsonl")
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	return string(data)
}

func run(t *testing.T, stdin io.Reader, args ...string) (string, string, int) {
	t.Helper()

	var stdout, stderr bytes.Buffer

	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "test"},
		cli.WithStdin(stdin),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		cli.WithLookupEnv(func(string) (string, bool) { return "", false }),
	)

	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)

	code := cli.Execute(t.Context(), root)

	return stdout.String(), stderr.String(), code
}
