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

	"github.com/goccy/go-yaml"

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

	t.Run("should route the ticket and retain its CSV columns", func(t *testing.T) {
		t.Parallel()
		blocks := codeBlocks(readRepoFile(t, "README.md"))
		if len(blocks) < 4 || blocks[0].lang != "sh" || blocks[1].lang != "csv" || blocks[2].lang != "sh" || blocks[3].lang != "csv" {
			t.Fatal("README.md needs a routing command, CSV input, a batch command and CSV output")
		}

		for _, tc := range []struct {
			name, fixture, stdin, want string
			block                      int
		}{
			{"should print the chosen team", `{"answer":"billing"}`, "I was charged twice for my order\n", "billing\n", 0},
			{"should append team to the original columns", `{"team":"billing"}`, strings.Join(blocks[1].lines, "\n") + "\n", strings.Join(blocks[3].lines, "\n") + "\n", 2},
		} {
			t.Run(tc.name, func(t *testing.T) {
				mock := filepath.Join(t.TempDir(), "team.json")
				if err := os.WriteFile(mock, []byte(tc.fixture), 0o600); err != nil {
					t.Fatal(err)
				}

				command := readmeCommand(t, blocks[tc.block].lines)
				out, errOut, code := runReadme(t, tc.stdin, append(command[1:], "--mock", mock)...)
				if code != cli.ExitOK || out != tc.want {
					t.Errorf("exit code = %d, stdout = %q, want %q, stderr = %s", code, out, tc.want, errOut)
				}
			})
		}
	})

	t.Run("should print the destroys excerpt the thresholds section shows", func(t *testing.T) {
		t.Parallel()

		blocks := codeBlocks(readmeSection(t, "Thresholds from evidence", "examples", "README.md"))
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

		section := strings.Join(strings.Fields(readmeSection(t, "Thresholds from evidence", "README.md")), " ")

		claim := destroysClaim.FindStringSubmatch(section)
		if claim == nil {
			t.Fatal("README.md states no destroys cuts for shell-safety")
		}

		if claim[1] != block || claim[2] != pass {
			t.Errorf("README.md says blocks at %s and passes below %s, and the gate cuts destroys at %s in abstain_if and %s in assert",
				claim[1], claim[2], block, pass)
		}
	})

	t.Run("should pass the offline CI check examples README shows", func(t *testing.T) {
		t.Parallel()

		_, offline, found := strings.Cut(readRepoFile(t, "examples", "README.md"), "\n## Offline\n")
		if !found {
			t.Fatal("examples README has no Offline section")
		}

		_, errOut, code := runCalibrateOnCommitted(t, readmeCommand(t, codeBlocks(offline)[0].lines))
		if code != cli.ExitOK {
			t.Errorf("exit code = %d, want %d\nstderr:\n%s", code, cli.ExitOK, errOut)
		}
	})

	t.Run("should print the secrets excerpt the examples README compare section shows", func(t *testing.T) {
		t.Parallel()

		blocks := codeBlocks(readmeSection(t, "Compare two runs", "examples", "README.md"))

		out, errOut, code := runCompareOnCommitted(t, readmeCommand(t, blocks[0].lines))
		if code != cli.ExitOK || !strings.Contains(errOut, "differs from examples/data/shell-safety.answers.jsonl in the model") {
			t.Fatalf("exit code = %d, want %d naming the model\nstderr:\n%s", code, cli.ExitOK, errOut)
		}

		// The temp copies make the table wider than a run from the repository root, so the lines
		// are compared with their spacing collapsed.
		var want []string

		for _, line := range blocks[1].lines {
			if strings.TrimSpace(line) != "" {
				want = append(want, strings.Join(strings.Fields(line), " "))
			}
		}

		got := strings.Split(out, "\n")
		for i, line := range got {
			got[i] = strings.Join(strings.Fields(line), " ")
		}

		if !inOrder(got, want) {
			t.Errorf("examples README shows\n%s\nwhich the comparison does not print. It prints\n%s",
				strings.Join(want, "\n"), out)
		}
	})

	t.Run("should hold and fail the change requirements the examples README compare section names", func(t *testing.T) {
		t.Parallel()

		command := readmeCommand(t, codeBlocks(readmeSection(t, "Compare two runs", "examples", "README.md"))[0].lines)

		for _, tc := range []struct {
			requirement string
			code        int
			stderr      string
		}{
			{requirement: "lower(destroys.catches_change) >= -0.25", code: cli.ExitOK},
			{requirement: "lower(network.catches_change) >= -0.25", code: cli.ExitOK},
			{requirement: "lower(secrets.catches_change) >= -0.25", code: cli.ExitFail, stderr: "interval -0.4345 to 0.4345"},
		} {
			_, errOut, code := runCompareOnCommitted(t, append(slices.Clone(command), "--require", tc.requirement))
			if code != tc.code || !strings.Contains(errOut, tc.stderr) {
				t.Errorf("%s exit code = %d, want %d with %q\nstderr:\n%s", tc.requirement, code, tc.code, tc.stderr, errOut)
			}
		}
	})

	t.Run("should block with the mock answers the examples README testing a gate section shows", func(t *testing.T) {
		t.Parallel()

		found := mockEcho.FindStringSubmatch(readmeSection(t, "Testing a gate", "examples", "README.md"))
		if found == nil {
			t.Fatal("examples README writes no danger.json")
		}

		mock := filepath.Join(t.TempDir(), "danger.json")
		if err := os.WriteFile(mock, []byte(found[1]), 0o600); err != nil {
			t.Fatal(err)
		}

		_, errOut, code := runReadme(t, "", "-f", "shell-safety", "-q", "--state", "rm -rf /", "--mock", mock)
		if code != cli.ExitFail {
			t.Errorf("exit code = %d, want %d for the blocked branch\nstderr:\n%s", code, cli.ExitFail, errOut)
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
		root.Command.SetOut(out)
		root.Command.SetErr(&errOut)
		root.Command.SetArgs(append(command[1:], "--mock", mock))

		exited := make(chan int, 1)

		go func() { exited <- root.Execute(t.Context()) }()

		for _, record := range []string{`{"id":"t1","body":"the site is down"}`, `{"id":"t2","body":"thanks"}`} {
			if _, err := io.WriteString(feed, record+"\n"); err != nil {
				t.Fatalf("feeding stdin: %v", err)
			}

			<-out.lines
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

		_, after, found := strings.Cut(readRepoFile(t, "examples", "README.md"), "**Comment on a pull request.**")
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

		catalog, err := skillcheck.NewCatalog(cli.DryRunRules())
		if err != nil {
			t.Fatal(err)
		}

		commands := readmeCommands(t)
		if len(commands) < 10 {
			t.Fatalf("found %d onesie commands in README.md, want the whole README", len(commands))
		}

		for _, command := range commands {
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
			// --print-request refuses a typed gate or policy, and --print-questions a positional
			// question, so a command dry runs without them. The test below runs the gated recipe whole.
			for _, long := range skillcheck.JudgingFlags() {
				tokens = withoutFlag(tokens, "--"+long)
			}
			tokens = intoTempDir(t, tokens)

			args, reason, err := catalog.DryRunArgs(tokens)
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

		if strings.HasPrefix(tokens[i], flag+"=") {
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

func readmeSection(t *testing.T, title string, path ...string) string {
	t.Helper()

	_, section, found := strings.Cut(readRepoFile(t, path...), "\n## "+title+"\n")
	if !found {
		t.Fatalf("%s has no %s section", filepath.Join(path...), title)
	}

	section, _, _ = strings.Cut(section, "\n## ")

	return section
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

type codeBlock struct {
	lang  string
	lines []string
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

	_, after, found := strings.Cut(readRepoFile(t, "examples", "README.md"), title)
	if !found {
		t.Fatalf("README.md has no %s recipe", title)
	}

	return readmeCommand(t, codeBlocks(after)[0].lines)
}

func readmeCommands(t *testing.T) []readmeLine {
	t.Helper()

	var commands []readmeLine

	for _, block := range codeBlocks(readRepoFile(t, "README.md") + "\n" + readRepoFile(t, "examples", "README.md")) {
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

func runCompareOnCommitted(t *testing.T, command []string) (string, string, int) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	args := slices.Clone(command[1:])

	for i, arg := range args {
		name, committed := strings.CutPrefix(arg, "examples/data/")
		if !committed || !strings.HasSuffix(name, ".answers.jsonl") {
			continue
		}

		for _, suffix := range []string{"", ".onesie"} {
			data := readRepoFile(t, "examples", "data", name+suffix)
			if err := os.WriteFile(filepath.Join(dir, name+suffix), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		args[i] = filepath.Join(dir, name)
	}

	out, errOut, code := runReadme(t, readRepoFile(t, "examples", "data", "shell-safety.jsonl"), args...)

	// The copies stand in for the committed files, so their paths read as the README writes them.
	committed := func(text string) string {
		return strings.ReplaceAll(text, dir+string(filepath.Separator), "examples/data/")
	}

	return committed(out), committed(errOut), code
}

func runReadme(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()

	var out, errOut bytes.Buffer

	root := readmeRoot(t, strings.NewReader(stdin))
	root.Command.SetOut(&out)
	root.Command.SetErr(&errOut)
	root.Command.SetArgs(args)

	code := root.Execute(t.Context())

	return out.String(), errOut.String(), code
}

func readmeRoot(t *testing.T, stdin io.Reader) *cli.Root {
	t.Helper()

	dir := t.TempDir()

	return cli.NewRootCmd(
		cli.BuildInfo{Version: "1.2.3"},
		cli.WithKeychain(offKeychain()),
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
