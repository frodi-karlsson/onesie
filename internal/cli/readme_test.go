package cli_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/cli"
	"github.com/frodi-karlsson/onesie/internal/skillcheck"
)

var (
	reportHeader = regexp.MustCompile(`^\S+, (yes/no|pick|rate):`)
	shellVar     = regexp.MustCompile(`"\$\w+"`)
	mockEcho     = regexp.MustCompile(`echo '(\{[^']*\})' > danger\.json`)
	destroysGate = regexp.MustCompile(`\bdestroys\.value < ([0-9.]+)`)

	destroysClaim = regexp.MustCompile(`For destroys, shell-safety blocks at ([0-9.]+) and passes below ([0-9.]+)\.`)
)

func TestReadme(t *testing.T) {
	t.Parallel()

	t.Run("should print the destroys excerpt the thresholds section shows", func(t *testing.T) {
		t.Parallel()

		blocks := codeBlocks(readmeSection(t, "Thresholds from evidence"))
		command := append(readmeCommand(t, blocks[0].lines), "--offline")

		out, errOut, code := runCalibrateOnCommitted(t, command)
		if code != cli.ExitOK {
			t.Fatalf("exit code = %d, want %d\nstderr:\n%s", code, cli.ExitOK, errOut)
		}

		var want []string

		for _, line := range blocks[1].lines {
			if strings.TrimSpace(line) != "" {
				want = append(want, line)
			}
		}

		if got := destroysSection(out, want[0]); !inOrder(got, want) {
			t.Errorf("README.md shows\n%s\nwhich the offline run does not print. It prints\n%s",
				strings.Join(want, "\n"), strings.Join(got, "\n"))
		}
	})

	t.Run("should state the destroys cuts the shell-safety gate holds", func(t *testing.T) {
		t.Parallel()

		var gate struct {
			Assert    string `yaml:"assert"`
			AbstainIf string `yaml:"abstain_if"`
		}

		if err := yaml.Unmarshal([]byte(readRepoFile(t, "examples", "questions", "shell-safety.yaml")), &gate); err != nil {
			t.Fatal(err)
		}

		pass, block := destroysCut(gate.Assert), destroysCut(gate.AbstainIf)

		section := strings.Join(strings.Fields(readmeSection(t, "Thresholds from evidence")), " ")

		claim := destroysClaim.FindStringSubmatch(section)
		if claim == nil {
			t.Fatal("README.md states no destroys cuts for shell-safety")
		}

		if claim[1] != block || claim[2] != pass {
			t.Errorf("README.md says blocks at %s and passes below %s, and the gate cuts destroys at %s in abstain_if and %s in assert",
				claim[1], claim[2], block, pass)
		}
	})

	t.Run("should pass the offline CI check REFERENCE.md shows", func(t *testing.T) {
		t.Parallel()

		_, offline, found := strings.Cut(readRepoFile(t, "REFERENCE.md"), "\n### Offline\n")
		if !found {
			t.Fatal("REFERENCE.md has no Offline section")
		}

		_, errOut, code := runCalibrateOnCommitted(t, readmeCommand(t, codeBlocks(offline)[0].lines))
		if code != cli.ExitOK {
			t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, cli.ExitOK, errOut)
		}
	})

	t.Run("should block with the mock answers the thresholds section shows", func(t *testing.T) {
		t.Parallel()

		found := mockEcho.FindStringSubmatch(readmeSection(t, "Thresholds from evidence"))
		if found == nil {
			t.Fatal("README.md writes no danger.json")
		}

		mock := filepath.Join(t.TempDir(), "danger.json")
		if err := os.WriteFile(mock, []byte(found[1]), 0o600); err != nil {
			t.Fatal(err)
		}

		_, errOut, code := runReadme(t, "", "-f", "shell-safety", "-q", "--state", "rm -rf /", "--mock", mock)
		if code != cli.ExitRejected {
			t.Errorf("exit code = %d, want %d for the blocked branch\nstderr:\n%s", code, cli.ExitRejected, errOut)
		}
	})

	t.Run("should answer each record of the triage recipe before stdin closes", func(t *testing.T) {
		t.Parallel()

		mock := filepath.Join(t.TempDir(), "urgent.json")
		if err := os.WriteFile(mock, []byte(`{"answer": 0.9}`), 0o600); err != nil {
			t.Fatal(err)
		}

		command := recipeCommand(t, "**Triage tickets as they come in.**")

		stdin, feed := io.Pipe()
		t.Cleanup(func() { feed.Close() })

		out := &lineCounter{lines: make(chan struct{}, 8)}

		var errOut bytes.Buffer

		root := readmeRoot(t, stdin)
		root.SetOut(out)
		root.SetErr(&errOut)
		root.SetArgs(append(command[1:], "--mock", mock))

		exited := make(chan int, 1)

		go func() { exited <- cli.Execute(t.Context(), root) }()

		for _, record := range []string{`{"id":"t1","body":"the site is down"}`, `{"id":"t2","body":"thanks"}`} {
			if _, err := io.WriteString(feed, record+"\n"); err != nil {
				t.Fatalf("feeding stdin: %v", err)
			}

			select {
			case <-out.lines:
			case <-time.After(5 * time.Second):
				t.Fatalf("no answer for %s while stdin stayed open\nstderr:\n%s", record, errOut.String())
			}
		}

		feed.Close()

		if code := <-exited; code != cli.ExitOK {
			t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, cli.ExitOK, errOut.String())
		}
	})

	t.Run("should comment the gate result first in the pull request recipe", func(t *testing.T) {
		t.Parallel()

		mock := filepath.Join(t.TempDir(), "explained.json")
		if err := os.WriteFile(mock, []byte(`{"answer": 0.9}`), 0o600); err != nil {
			t.Fatal(err)
		}

		_, after, found := strings.Cut(readRepoFile(t, "README.md"), "**Comment on a pull request.**")
		if !found {
			t.Fatal("README.md has no pull request recipe")
		}

		command := commandsIn(codeBlocks(after)[0].lines)
		if len(command) != 1 {
			t.Fatalf("want one onesie command in the pull request recipe, got %d", len(command))
		}

		tokens, reason := skillcheck.Tokenize(shellVar.ReplaceAllString(command[0].text, "'x'"))
		if reason != "" {
			t.Fatalf("%s does not tokenize: %s", command[0].text, reason)
		}

		out, errOut, code := runReadme(t, "", append(tokens[1:], "--mock", mock)...)
		if code != cli.ExitOK || !strings.HasPrefix(out, "> [!") {
			t.Errorf("exit code = %d, want %d and a GitHub alert first\nstdout:\n%s\nstderr:\n%s", code, cli.ExitOK, out, errOut)
		}
	})

	t.Run("should dry run every onesie command the README shows", func(t *testing.T) {
		t.Parallel()

		// Checked by the tests above against the committed answers, since --print-request would
		// only check the flags.
		checkedElsewhere := []string{"calibrate"}
		// The README shortens the offline check to calibrate ... and REFERENCE.md holds it whole.
		abbreviated := " ... "

		commands := readmeCommands(t)
		if len(commands) < 10 {
			t.Fatalf("found %d onesie commands in README.md, want the whole README", len(commands))
		}

		for _, command := range commands {
			if strings.Contains(command.text, abbreviated) {
				continue
			}

			tokens, reason := skillcheck.Tokenize(shellVar.ReplaceAllString(command.text, "'x'"))
			if reason != "" {
				t.Errorf("%s does not tokenize: %s", command.text, reason)

				continue
			}

			if slices.Contains(checkedElsewhere, tokens[1]) {
				continue
			}

			// A dry run writes no answers, so --resume has nothing to pick up and is refused.
			tokens = slices.DeleteFunc(tokens, func(token string) bool { return token == "--resume" })
			// --print-request refuses a typed gate, and --print-questions a positional question, so
			// the pull request recipe dry runs without its gate. The test below runs it whole.
			tokens = withoutFlag(tokens, "--assert")
			tokens = intoTempDir(t, tokens)

			args, reason, err := skillcheck.DryRunArgs(tokens)
			if err != nil || reason != "" {
				t.Errorf("%s has no dry run: %v %s", command.text, err, reason)

				continue
			}

			stdin := ""
			if command.readsStdin {
				stdin = sampleRecord(args)
			}

			_, errOut, code := runReadme(t, stdin, args...)
			if code != cli.ExitOK {
				t.Errorf("%s dry run as %v exit code = %d\nstderr:\n%s", command.text, args, code, errOut)
			}
		}
	})
}

func destroysCut(expression string) string {
	found := destroysGate.FindStringSubmatch(expression)
	if found == nil {
		return ""
	}

	return found[1]
}

func withoutFlag(tokens []string, flag string) []string {
	var kept []string

	for i := 0; i < len(tokens); i++ {
		if tokens[i] == flag {
			i++

			continue
		}

		kept = append(kept, tokens[i])
	}

	return kept
}

func intoTempDir(t *testing.T, tokens []string) []string {
	t.Helper()

	dir := t.TempDir()

	for i, token := range tokens {
		if i > 0 && tokens[i-1] == "--out" {
			tokens[i] = filepath.Join(dir, token)
		}
	}

	return tokens
}

func readmeSection(t *testing.T, title string) string {
	t.Helper()

	_, section, found := strings.Cut(readRepoFile(t, "README.md"), "\n## "+title+"\n")
	if !found {
		t.Fatalf("README.md has no %s section", title)
	}

	section, _, _ = strings.Cut(section, "\n## ")

	return section
}

type codeBlock struct {
	lang  string
	lines []string
}

func codeBlocks(text string) []codeBlock {
	var (
		blocks  []codeBlock
		current *codeBlock
	)

	for _, line := range strings.Split(text, "\n") {
		fence, isFence := strings.CutPrefix(line, "```")

		switch {
		case isFence && current == nil:
			current = &codeBlock{lang: fence}
		case isFence:
			blocks = append(blocks, *current)
			current = nil
		case current != nil:
			current.lines = append(current.lines, line)
		}
	}

	return blocks
}

func readmeCommand(t *testing.T, lines []string) []string {
	t.Helper()

	commands := commandsIn(lines)
	if len(commands) != 1 {
		t.Fatalf("want one onesie command in\n%s", strings.Join(lines, "\n"))
	}

	tokens, reason := skillcheck.Tokenize(commands[0].text)
	if reason != "" {
		t.Fatalf("%s does not tokenize: %s", commands[0].text, reason)
	}

	return tokens
}

func recipeCommand(t *testing.T, title string) []string {
	t.Helper()

	_, after, found := strings.Cut(readRepoFile(t, "README.md"), title)
	if !found {
		t.Fatalf("README.md has no %s recipe", title)
	}

	return readmeCommand(t, codeBlocks(after)[0].lines)
}

func readmeCommands(t *testing.T) []readmeLine {
	t.Helper()

	var commands []readmeLine

	for _, block := range codeBlocks(readRepoFile(t, "README.md")) {
		if block.lang == "sh" {
			commands = append(commands, commandsIn(block.lines)...)
		}
	}

	return commands
}

type readmeLine struct {
	text       string
	readsStdin bool
}

func commandsIn(lines []string) []readmeLine {
	joined := strings.ReplaceAll(strings.Join(lines, "\n"), "\\\n", " ")

	var commands []readmeLine

	for _, line := range strings.Split(joined, "\n") {
		segments := unquotedSplit(line)

		for i, segment := range segments {
			text := strings.TrimSpace(segment.text)
			if !strings.HasPrefix(text, "onesie ") {
				continue
			}

			piped := i > 0 && segments[i-1].end == '|'
			commands = append(commands, readmeLine{text: text, readsStdin: piped || redirectsIn(segments[i:])})
		}
	}

	return commands
}

type segment struct {
	text string
	end  rune
}

func unquotedSplit(line string) []segment {
	var (
		segments []segment
		current  strings.Builder
		quote    rune
	)

	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '|' || r == '<' || r == '>' || r == '#':
			segments = append(segments, segment{text: current.String(), end: r})
			current.Reset()

			continue
		}

		current.WriteRune(r)
	}

	return append(segments, segment{text: current.String()})
}

func redirectsIn(segments []segment) bool {
	return segments[0].end == '<'
}

func sampleRecord(args []string) string {
	switch {
	case slices.Contains(args, "jsonl"):
		return `{"id":"1","body":"the site is down"}` + "\n"
	case slices.Contains(args, "csv"):
		return "id,body\n1,the site is down\n"
	default:
		return "the site is down\n"
	}
}

func runCalibrateOnCommitted(t *testing.T, command []string) (string, string, int) {
	t.Helper()

	dir := t.TempDir()
	answers := filepath.Join(dir, "answers.jsonl")

	for _, suffix := range []string{"", ".onesie"} {
		data := readRepoFile(t, "examples", "data", "shell-safety.answers.jsonl"+suffix)
		if err := os.WriteFile(answers+suffix, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	args := slices.Clone(command[1:])
	for i, arg := range args {
		if arg == "answers.jsonl" || arg == "examples/data/shell-safety.answers.jsonl" {
			args[i] = answers
		}
	}

	return runReadme(t, readRepoFile(t, "examples", "data", "shell-safety.jsonl"), args...)
}

func runReadme(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := readmeRoot(t, strings.NewReader(stdin))
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)

	code := cli.Execute(t.Context(), root)

	return out.String(), errOut.String(), code
}

func readmeRoot(t *testing.T, stdin io.Reader) *cobra.Command {
	t.Helper()

	dir := t.TempDir()

	return cli.NewRootCmd(
		cli.BuildInfo{Version: "1.2.3"},
		cli.WithKeychain(offKeychain{}),
		cli.WithStdin(stdin),
		cli.WithStdinTTY(false),
		cli.WithStdoutTTY(false),
		cli.WithWorkingDir(func() (string, error) { return dir, nil }),
		cli.WithHomeDir(func() (string, error) { return dir, nil }),
		cli.WithLookupEnv(lookupOnly(map[string]string{
			"ONESIE_CONFIG_DIR": filepath.Join(dir, "config"),
			"ONESIE_CACHE_DIR":  filepath.Join(dir, "cache"),
		})),
	)
}

func lookupOnly(env map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := env[name]

		return value, ok
	}
}

func destroysSection(report, header string) []string {
	var section []string

	for _, line := range strings.Split(report, "\n") {
		switch {
		case line == header:
			section = []string{line}
		case len(section) > 0 && reportHeader.MatchString(line):
			return section
		case len(section) > 0:
			section = append(section, line)
		}
	}

	return section
}

func inOrder(lines, want []string) bool {
	next := 0
	for _, line := range lines {
		if next < len(want) && line == want[next] {
			next++
		}
	}

	return next == len(want)
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}
