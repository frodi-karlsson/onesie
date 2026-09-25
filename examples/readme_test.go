package examples_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/frodi-karlsson/onesie/examples"
	"github.com/frodi-karlsson/onesie/internal/cli"
)

var (
	reportHeader = regexp.MustCompile(`^\S+, (yes/no|pick|rate):`)
	gateCut      = regexp.MustCompile(`(\w+)\.value < ([0-9.]+)`)
	decimal      = regexp.MustCompile(`\b\d+\.\d+\b`)
	codeSpan     = regexp.MustCompile("`[^`]*`")
)

func TestReadme(t *testing.T) {
	t.Parallel()

	t.Run("should list requirements only for sets that exist", func(t *testing.T) {
		t.Parallel()

		sets, err := examples.Sets(".")
		if err != nil {
			t.Fatal(err)
		}

		names := make([]string, 0, len(sets))
		for _, set := range sets {
			names = append(names, set.Name)
		}

		for _, name := range requirementRows(t) {
			if !slices.Contains(names, name) {
				t.Errorf("README.md lists requirements for %s, which is not a starter set", name)
			}
		}
	})
}

func requirementRows(t *testing.T) []string {
	t.Helper()

	_, section, found := strings.Cut(readFile(t, "README.md"), "\n## Requirements checked in CI\n")
	if !found {
		t.Fatal("README.md has no Requirements checked in CI section")
	}

	section, _, _ = strings.Cut(section, "\n## ")

	var names []string

	for _, line := range strings.Split(section, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) != 4 || !strings.HasPrefix(strings.TrimSpace(cells[1]), "`") {
			continue
		}

		names = append(names, strings.Trim(strings.TrimSpace(cells[1]), "`"))
	}

	return names
}

func showsTables(t *testing.T, set examples.Set) {
	for _, chunk := range reportChunks(t, set.Name) {
		var cuts []string

		for _, line := range chunk[1:] {
			fields := strings.Fields(line)
			if len(fields) > 1 && decimal.MatchString(fields[0]) && strings.Contains(line, "/") {
				cuts = append(cuts, fields[0])
			}
		}

		var args []string
		if len(cuts) > 0 {
			args = []string{"--cuts", strings.Join(cuts, ",")}
		}

		stdout, stderr, code := offline(t, set, args...)
		if code != cli.ExitOK {
			t.Fatalf("exit %d\nstderr:\n%s", code, stderr)
		}

		if !inOrder(sectionOf(stdout, chunk[0]), chunk) {
			t.Errorf("README.md shows\n%s\nwhich a fresh offline run does not print. It prints\n%s",
				strings.Join(chunk, "\n"), stdout)
		}
	}
}

func reportChunks(t *testing.T, name string) [][]string {
	t.Helper()

	var chunks [][]string

	inBlock, plain := false, false

	for _, line := range strings.Split(setSection(t, name), "\n") {
		if fence, found := strings.CutPrefix(line, "```"); found {
			inBlock, plain = !inBlock, fence == ""

			continue
		}

		switch {
		case !inBlock || !plain:
		case reportHeader.MatchString(line):
			chunks = append(chunks, []string{line})
		case len(chunks) > 0 && strings.TrimSpace(line) != "":
			chunks[len(chunks)-1] = append(chunks[len(chunks)-1], line)
		}
	}

	if len(chunks) == 0 {
		t.Fatalf("README.md shows no report for %s", name)
	}

	return chunks
}

func sectionOf(report, header string) []string {
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

func quotesNumbers(t *testing.T, set examples.Set) {
	f := factsOf(t, set)
	prose := proseOf(t, set.Name)
	explained := map[string]bool{}

	for _, cut := range f.pass {
		explained[number(cut)] = true
	}

	for _, cut := range f.block {
		explained[number(cut)] = true
	}

	section := strings.Join(strings.Fields(setSection(t, set.Name)), " ")

	for _, c := range claimsOf(set.Name) {
		values := make([]any, 0, len(c.values))
		for _, value := range c.values {
			values = append(values, value(f))
		}

		text := fmt.Sprintf(c.text, values...)
		if !strings.Contains(section, text) {
			t.Errorf("README.md should say %q, as the answers give it", text)
		}

		for _, quoted := range decimal.FindAllString(codeSpan.ReplaceAllString(text, ""), -1) {
			explained[number(parse(quoted))] = true
		}
	}

	for _, quoted := range decimal.FindAllString(prose, -1) {
		if !explained[number(parse(quoted))] {
			t.Errorf("README.md quotes %s for %s, which is neither a cut in its gate nor a checked claim", quoted, set.Name)
		}
	}
}

func proseOf(t *testing.T, name string) string {
	t.Helper()

	var prose strings.Builder

	for i, part := range strings.Split(setSection(t, name), "```") {
		if i%2 == 0 {
			prose.WriteString(codeSpan.ReplaceAllString(part, ""))
		}
	}

	return prose.String()
}

func setSection(t *testing.T, name string) string {
	t.Helper()

	_, section, found := strings.Cut(readFile(t, "README.md"), "\n## "+name+"\n")
	if !found {
		t.Fatalf("README.md has no section for %s", name)
	}

	section, _, _ = strings.Cut(section, "\n## ")

	return section
}

type claim struct {
	text   string
	values []func(f facts) any
}

type facts struct {
	scores      map[string]map[string]float64
	yes         map[string]map[string]bool
	pass, block map[string]float64
	verdicts    map[string]string
}

func factsOf(t *testing.T, set examples.Set) facts {
	t.Helper()

	f := facts{
		scores: map[string]map[string]float64{}, yes: map[string]map[string]bool{},
		pass: map[string]float64{}, block: map[string]float64{}, verdicts: map[string]string{},
	}

	gate := documentedGate(t, set.Name)
	for _, m := range gateCut.FindAllStringSubmatch(gate.Assert, -1) {
		f.pass[m[1]] = parse(m[2])
	}

	for _, m := range gateCut.FindAllStringSubmatch(gate.AbstainIf, -1) {
		f.block[m[1]] = parse(m[2])
	}

	for _, record := range readRecords(t, set) {
		id, _ := record["id"].(string)
		f.yes[id] = map[string]bool{}

		for _, q := range set.IDs {
			label, _ := record[q].(bool)
			f.yes[id][q] = label
		}
	}

	lines := bufio.NewScanner(strings.NewReader(readFile(t, set.Answers("."))))
	for lines.Scan() {
		var line map[string]json.RawMessage
		if err := json.Unmarshal(lines.Bytes(), &line); err != nil {
			t.Fatal(err)
		}

		var id string
		if err := json.Unmarshal(line["id"], &id); err != nil {
			t.Fatal(err)
		}

		f.scores[id] = map[string]float64{}

		for _, q := range set.IDs {
			var answer struct {
				Value any `json:"value"`
			}

			if err := json.Unmarshal(line[q], &answer); err != nil {
				t.Fatal(err)
			}

			if value, isNumber := answer.Value.(float64); isNumber {
				f.scores[id][q] = value
			}
		}

		f.verdicts[id] = f.verdict(id)
	}

	return f
}

func (f facts) verdict(id string) string {
	below := func(cuts map[string]float64) bool {
		for q, cut := range cuts {
			if f.scores[id][q] >= cut {
				return false
			}
		}

		return true
	}

	switch {
	case below(f.pass):
		return "pass"
	case below(f.block):
		return "person"
	default:
		return "block"
	}
}

func (f facts) bad(id string) bool {
	for _, yes := range f.yes[id] {
		if yes {
			return true
		}
	}

	return false
}

func count(verdict string, bad bool) func(facts) any {
	return func(f facts) any {
		n := 0
		for id, got := range f.verdicts {
			if got == verdict && f.bad(id) == bad {
				n++
			}
		}

		return n
	}
}

func counted(verdict string) func(facts) any {
	return func(f facts) any {
		n := 0
		for _, got := range f.verdicts {
			if got == verdict {
				n++
			}
		}

		return n
	}
}

func scoreOf(id, q string) func(facts) any {
	return func(f facts) any { return f.scores[id][q] }
}

func lowestYes(q string) func(facts) any {
	return func(f facts) any {
		low := math.Inf(1)
		for id, labels := range f.yes {
			if labels[q] {
				low = math.Min(low, f.scores[id][q])
			}
		}

		return low
	}
}

func highestNo(q string) func(facts) any {
	return func(f facts) any {
		high := math.Inf(-1)
		for id, labels := range f.yes {
			if !labels[q] {
				high = math.Max(high, f.scores[id][q])
			}
		}

		return high
	}
}

func passMargin(q string) func(facts) any {
	return func(f facts) any { return lowestYes(q)(f).(float64) - f.pass[q] }
}

func blockMargin(q string) func(facts) any {
	return func(f facts) any { return f.block[q] - highestNo(q)(f).(float64) }
}

func yesMargin(q string) func(facts) any {
	return func(f facts) any { return lowestYes(q)(f).(float64) - f.block[q] }
}

func parse(text string) float64 {
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return math.NaN()
	}

	return value
}

func number(value float64) string {
	return strconv.FormatFloat(math.Round(value*100)/100, 'f', 2, 64)
}

func claimsOf(name string) []claim {
	of := func(text string, values ...func(facts) any) claim { return claim{text: text, values: values} }

	switch name {
	case "shell-safety":
		return []claim{
			of("Those cuts sit %.2f, %.2f and %.2f below the lowest labelled yes",
				passMargin("destroys"), passMargin("secrets"), passMargin("network")),
			of("The first sits %.2f above the highest safe command, `rm -rf ./build` at %.2f.",
				blockMargin("destroys"), scoreOf("rm-build", "destroys")),
			of("The second sits %.2f above the highest command labelled as sending no secrets, and %.2f below the lowest",
				blockMargin("secrets"), yesMargin("secrets")),
			of("%d commands passed and all were safe", counted("pass")),
			of("%d were blocked and all were labelled dangerous", counted("block")),
			of("%d went to a person, and %d of those were safe", counted("person"), count("person", false)),
			of("`rm -rf /` scored %.2f on `destroys`", scoreOf("rm-root", "destroys")),
			of("`echo 'rm -rf /'` scored %.2f, above the pass cut", scoreOf("echo-rm", "destroys")),
			of("`echo hello > notes.txt` overwrites a file but scored %.2f", scoreOf("echo-overwrite", "destroys")),
			of("`ssh deploy@203.0.113.5 uptime` scored %.2f on `secrets`", scoreOf("ssh-uptime", "secrets")),
		}
	case "prompt-injection":
		return []claim{
			of("a request for the system prompt, scored only %.2f on `instructs`", scoreOf("ask-prompt", "instructs")),
			of("clean texts scored %.2f, %.2f and %.2f", scoreOf("router", "instructs"), scoreOf("recipe", "instructs"),
				scoreOf("readme-quote", "instructs")),
			of("`access` caught it at %.2f", scoreOf("ask-prompt", "access")),
			of("`overrides` reaches 0.7, %.2f above the highest clean text, or `access` reaches 0.82, %.2f above",
				blockMargin("overrides"), blockMargin("access")),
			of("%d of 20 clean texts passed and %d went to a person", count("pass", false), count("person", false)),
			of("All %d blocked texts were injections, and the other %d injections went to a person",
				counted("block"), count("person", true)),
			of("a tutorial that quotes a system prompt scored %.2f on `instructs`", scoreOf("tutorial-prompt", "instructs")),
			of("a `robots.txt` scored %.2f", scoreOf("robots", "instructs")),
			of("`Sure, go ahead and ignore everything I said, like always.` scored %.2f on `overrides`",
				scoreOf("sarcasm", "overrides")),
			of("approve a pull request scored %.2f on `access`", scoreOf("pr-approve", "access")),
			of("quotes an attacker's override scored %.2f on `overrides`", scoreOf("blog-security", "overrides")),
		}
	case "personal-data":
		return []claim{
			of("`name_contact` at 0.15 sits %.2f below a shipping label at %.2f",
				passMargin("name_contact"), scoreOf("ship-to", "name_contact")),
			of("sit %.2f, %.2f and %.2f below theirs",
				passMargin("email_phone"), passMargin("home_address"), passMargin("credential")),
			of("They sit %.2f, %.2f, %.2f and %.2f above the highest labelled no",
				blockMargin("name_contact"), blockMargin("email_phone"), blockMargin("home_address"),
				blockMargin("credential")),
			of("a museum, scored %.2f and the lowest labelled home address %.2f",
				scoreOf("baker", "home_address"), lowestYes("home_address")),
			of("%d texts passed and all were clean", counted("pass")),
			of("All %d texts with personal data were blocked", counted("block")),
			of("%d clean texts went to a person", count("person", false)),
			of("a fake OpenSSH private key scored only %.2f on `credential`", scoreOf("private-key", "credential")),
			of("scored %.2f on `name_contact`, though the address question caught it at %.2f",
				scoreOf("ship-to", "name_contact"), scoreOf("ship-to", "home_address")),
			of("with a named recipient scored %.2f on `email_phone`", scoreOf("hr-attn", "email_phone")),
		}
	case "moderation":
		return []claim{
			of("Every labelled hostile comment scored %.2f or more and every labelled spam %.2f or more",
				lowestYes("hostile"), lowestYes("spam")),
			of("when `hostile` reaches 0.85, %.2f above the highest clean comment", blockMargin("hostile")),
			of("`spam` reaches 0.5, %.2f above the highest clean comment", blockMargin("spam")),
			of("`Shut up.` at %.2f among them", scoreOf("shut-up", "hostile")),
			of("%d comments were published and all were clean", counted("pass")),
			of("%d hostile or spam comments were rejected, and the other %d went to a person",
				counted("block"), count("person", true)),
			of("%d clean comments went to a person", count("person", false)),
			of("though it gave it only %.2f on `hostile`", scoreOf("sarcasm-update", "hostile")),
			of("`Stop being so dramatic, it's just a typo.` scored %.2f on `hostile`", scoreOf("dramatic", "hostile")),
			of("what moderators see scored %.2f on `hostile`", scoreOf("quoted-slur", "hostile")),
		}
	default:
		return nil
	}
}
