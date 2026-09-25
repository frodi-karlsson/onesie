package examples_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"

	"github.com/frodi-karlsson/onesie/examples"
	"github.com/frodi-karlsson/onesie/internal/cli"
)

var errKeychain = errors.New("the starter set tests never use the keychain")

func TestStarterSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		check func(t *testing.T, set examples.Set)
	}{
		{name: "should load the question file and dry run it", check: dryRunQuestions},
		{name: "should print the question file back unchanged", check: reprintQuestions},
		{name: "should read every labelled record under calibrate", check: dryRunCalibrate},
		{name: "should carry the gate the README documents", check: carriesGate},
		{name: "should refuse a gate typed beside the file under --print-request", check: refuseTypedGate},
		{name: "should meet its requirements offline", check: meetsRequirements},
		{name: "should name the model its answers came from", check: namesModel},
		{name: "should show the tables a fresh offline run prints", check: showsTables},
		{name: "should quote only numbers its answers give", check: quotesNumbers},
	}

	sets, err := examples.Sets(".")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range tests {
		for _, set := range sets {
			t.Run(tc.name+" for "+set.Name, func(t *testing.T) {
				t.Parallel()

				tc.check(t, set)
			})
		}
	}
}

func dryRunQuestions(t *testing.T, set examples.Set) {
	stdout, stderr, code := run(t, strings.NewReader(""),
		"-f", set.Questions("."), "--state", "a sample record", "--print-request")
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
	if want := set.IDs; !slices.Equal(got, want) {
		t.Errorf("request asks %v, want %v", got, want)
	}
}

func reprintQuestions(t *testing.T, set examples.Set) {
	stdout, stderr, code := run(t, strings.NewReader(""), "-f", set.Questions("."), "--print-questions")
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}

	if want := readFile(t, set.Questions(".")); stdout != want {
		t.Errorf("--print-questions wrote\n%s\nwant the file as it is\n%s", stdout, want)
	}
}

func dryRunCalibrate(t *testing.T, set examples.Set) {
	records := readRecords(t, set)

	for _, record := range records {
		for _, id := range set.IDs {
			if record[id] == nil {
				t.Errorf("record %v has no %s label", record["id"], id)
			}
		}
	}

	args := append(set.CalibrateArgs("."), "--print-request")

	stdout, stderr, code := run(t, strings.NewReader(readFile(t, set.Data("."))), args...)
	if code != cli.ExitOK {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}

	if got := strings.Count(stdout, "\n"); got != len(records) {
		t.Errorf("calibrate wrote %d requests for %d records", got, len(records))
	}
}

func meetsRequirements(t *testing.T, set examples.Set) {
	var args []string
	for _, requirement := range requirementsOf(t, set.Name) {
		args = append(args, "--require", requirement)
	}

	stdout, stderr, code := offline(t, set, args...)
	if code != cli.ExitOK {
		t.Fatalf("exit %d, want 0\nstderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
}

func offline(t *testing.T, set examples.Set, extra ...string) (string, string, int) {
	t.Helper()

	// A copy keeps the lock file and any rewrite out of the tree, and lets the sets run in parallel.
	answers := filepath.Join(t.TempDir(), filepath.Base(set.Answers(".")))
	for _, suffix := range []string{"", ".onesie"} {
		if err := os.WriteFile(answers+suffix, []byte(readFile(t, set.Answers(".")+suffix)), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	args := append(set.CalibrateArgs("."), "--out", answers, "--resume", "--offline")

	return run(t, strings.NewReader(readFile(t, set.Data("."))), append(args, extra...)...)
}

func requirementsOf(t *testing.T, name string) []string {
	t.Helper()

	_, section, found := strings.Cut(readFile(t, "README.md"), "\n## Requirements checked in CI\n")
	if !found {
		t.Fatal("README.md has no Requirements checked in CI section")
	}

	section, _, _ = strings.Cut(section, "\n## ")

	for _, line := range strings.Split(section, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) != 4 || strings.TrimSpace(cells[1]) != "`"+name+"`" {
			continue
		}

		var requirements []string

		for _, quoted := range strings.Split(cells[2], "<br>") {
			requirement := strings.Trim(strings.TrimSpace(quoted), "`")
			if requirement != "" {
				requirements = append(requirements, requirement)
			}
		}

		if len(requirements) == 0 {
			t.Fatalf("README.md lists no requirement for %s", name)
		}

		return requirements
	}

	t.Fatalf("README.md has no requirements row for %s", name)

	return nil
}

func namesModel(t *testing.T, set examples.Set) {
	if want := "`" + examples.Model + "`"; !strings.Contains(readFile(t, "README.md"), want) {
		t.Errorf("README.md does not name the model %s", want)
	}

	lines := bufio.NewScanner(strings.NewReader(readFile(t, set.Answers("."))))
	for n := 1; lines.Scan(); n++ {
		var line struct {
			Model string `json:"model"`
		}

		if err := json.Unmarshal(lines.Bytes(), &line); err != nil || line.Model != examples.Model {
			t.Errorf("%s line %d names the model %q, %v, want %s", set.Answers("."), n, line.Model, err, examples.Model)
		}
	}
}

func carriesGate(t *testing.T, set examples.Set) {
	want := documentedGate(t, set.Name)

	var file gate
	if err := yaml.Unmarshal([]byte(readFile(t, set.Questions("."))), &file); err != nil {
		t.Fatalf("parsing %s: %v", set.Questions("."), err)
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

func refuseTypedGate(t *testing.T, set examples.Set) {
	_, stderr, code := run(t, strings.NewReader(""),
		"-f", set.Questions("."), "--state", "a sample record", "--print-request",
		"--assert", documentedGate(t, set.Name).Assert)
	if code != cli.ExitUsage {
		t.Fatalf("exit %d, want %d, stderr: %s", code, cli.ExitUsage, stderr)
	}

	if want := "onesie: --assert judges an answer, which --print-request does not produce"; !strings.Contains(stderr, want) {
		t.Errorf("stderr = %q, want it to contain %q", stderr, want)
	}
}

func readRecords(t *testing.T, set examples.Set) []map[string]any {
	t.Helper()

	var records []map[string]any

	lines := bufio.NewScanner(strings.NewReader(readFile(t, set.Data("."))))
	for lines.Scan() {
		var record map[string]any
		if err := json.Unmarshal(lines.Bytes(), &record); err != nil {
			t.Fatalf("%s line %d: %v", set.Data("."), len(records)+1, err)
		}

		records = append(records, record)
	}

	if len(records) == 0 {
		t.Fatalf("%s has no records", set.Data("."))
	}

	return records
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

	home := t.TempDir()

	root := cli.NewRootCmd(
		cli.BuildInfo{Version: "test"},
		cli.WithKeychain(failingKeychain{t: t}),
		cli.WithHomeDir(func() (string, error) { return home, nil }),
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

type failingKeychain struct {
	t *testing.T
}

func (k failingKeychain) Get(string) (string, error) {
	k.t.Error("a starter set test read the keychain")

	return "", errKeychain
}

func (k failingKeychain) Set(string, string) error {
	k.t.Error("a starter set test wrote the keychain")

	return errKeychain
}

func (k failingKeychain) Delete(string) error {
	k.t.Error("a starter set test deleted from the keychain")

	return errKeychain
}
