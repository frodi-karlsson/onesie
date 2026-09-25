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

var calibratedGates = map[string][2]string{
	"shell-safety": {
		"destroys.value < 0.3 and secrets.value < 0.45 and network.value < 0.35",
		"destroys.value < 0.85 and secrets.value < 0.6",
	},
	"prompt-injection": {
		"instructs.value < 0.25 and overrides.value < 0.5 and access.value < 0.4",
		"overrides.value < 0.9 and access.value < 0.65",
	},
	"personal-data": {
		"name_contact.value < 0.15 and email_phone.value < 0.2 and home_address.value < 0.3 and " +
			"credential.value < 0.3",
		"name_contact.value < 0.3 and email_phone.value < 0.7 and home_address.value < 0.95 and " +
			"credential.value < 0.35",
	},
	"moderation": {
		"hostile.value < 0.2 and spam.value < 0.1",
		"hostile.value < 0.8 and spam.value < 0.5",
	},
}

func TestStarterSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, set string)
	}{
		{name: "should load the question file and dry run it", check: dryRunQuestions},
		{name: "should print the question file back unchanged", check: reprintQuestions},
		{name: "should read every labelled record under calibrate", check: dryRunCalibrate},
		{name: "should carry the gate calibrated on the sample", check: carriesGate},
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
	want, found := calibratedGates[set]
	if !found {
		t.Fatalf("no calibrated gate listed for %s", set)
	}

	var file struct {
		Assert    string `yaml:"assert"`
		AbstainIf string `yaml:"abstain_if"`
	}

	if err := yaml.Unmarshal([]byte(readFile(t, questionPath(set))), &file); err != nil {
		t.Fatalf("parsing %s: %v", questionPath(set), err)
	}

	if got := [2]string{file.Assert, file.AbstainIf}; got != want {
		t.Errorf("assert and abstain_if = %q, want %q", got, want)
	}
}

func refuseTypedGate(t *testing.T, set string) {
	_, stderr, code := run(t, strings.NewReader(""),
		"-f", questionPath(set), "--state", "a sample record", "--print-request",
		"--assert", calibratedGates[set][0])
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
