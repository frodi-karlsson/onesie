package skillcheck

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/skillgen"
)

var (
	bareFlag      = regexp.MustCompile(`(?:^|[^\w-])--([A-Za-z][\w-]*)`)
	commandShape  = regexp.MustCompile(`^[a-z][a-z-]*$`)
	envAssignment = regexp.MustCompile(`^[A-Za-z_]\w*=`)
	spanFlag      = regexp.MustCompile(`^--([A-Za-z][\w-]*)`)
	helpFlag      = regexp.MustCompile(`^\s+(?:-(\w), )?--(\w[\w-]*)(?: (\S+))?(?:\s{2,}|$)`)
	helpCommand   = regexp.MustCompile(`^\s+(\S+)\s{2,}`)
)

// CheckProse checks every flag and subcommand the skills name outside their examples against the
// --help output of runner's binary. It reads the description, the compatibility, each rule's short
// and why, the intro, the sections and the references.
//
// It counts three kinds of mention, so the flags of other tools are never read as onesie's:
//
//   - A onesie command in a code span or on a line of a fenced block. It may follow a pipe, a chain
//     or env assignments, as in `ONESIE_MOCK=x onesie ...`, or sit inside `$(...)`. Its flags are
//     checked against the help of the command they follow, the root before any subcommand and,
//     after `onesie calibrate` or `onesie auth set`, that subcommand's own. A bare word where a
//     subcommand can stand must be one. The command ends at a pipe, a chain, a redirection, a
//     closing parenthesis or a lone --, so `onesie ... | jq -c --unbuffered` checks nothing of jq.
//   - A code span whose first word is a --flag, such as `--ask NAME=@FILE`. Only that flag counts.
//   - A --flag in plain prose, outside any code.
//
// A code span with no onesie command in it, such as `jq --unbuffered`, `git push --force` or
// `curl -fsSL`, is ignored. The last two kinds name no command, so they pass when the root help
// lists the flag, or the help of a subcommand the same skill runs in its prose or its examples.
// completion and a command that only asks for --help never count, and short flags are not checked.
func CheckProse(ctx context.Context, root string, runner *Runner) (ProseReport, error) {
	found, err := skillgen.Skills(root)
	if err != nil {
		return ProseReport{}, err
	}

	book := &helpBook{runner: runner, pages: map[string]helpPage{}}

	var report ProseReport

	for _, f := range found {
		texts, err := proseTexts(filepath.Join(root, "skills", f.Dir), f.Skill)
		if err != nil {
			return ProseReport{}, err
		}

		found := make([][]mention, len(texts))
		for i, text := range texts {
			found[i] = mentions(text.body)
		}

		flags, err := book.skillFlags(ctx, found, exampleCommands(f.Skill))
		if err != nil {
			return ProseReport{}, err
		}

		for i, text := range texts {
			for _, m := range found[i] {
				problems, err := book.check(ctx, m, flags)
				if err != nil {
					return ProseReport{}, err
				}

				report.Checked++

				for _, problem := range problems {
					report.Failures = append(report.Failures,
						fmt.Sprintf("onesie: skill '%s' %s: %s", f.Skill.Name, text.label, problem))
				}
			}
		}
	}

	return report, nil
}

// ProseReport tallies one run of CheckProse.
type ProseReport struct {
	Checked  int
	Failures []string
}

type proseText struct {
	label string
	body  string
}

func proseTexts(dir string, skill skillgen.Skill) ([]proseText, error) {
	texts := []proseText{
		{label: "description", body: skill.Description},
		{label: "compatibility", body: skill.Compatibility},
	}

	for _, rule := range skill.Rules {
		texts = append(texts, proseText{label: "rule '" + rule.ID + "'", body: rule.Short + "\n\n" + rule.Why})
	}

	var fragments []string
	if skill.Intro != "" {
		fragments = append(fragments, skill.Intro)
	}

	fragments = append(append(fragments, skill.Sections...), skill.References...)

	for _, name := range fragments {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("onesie: %w", err)
		}

		texts = append(texts, proseText{label: name, body: string(data)})
	}

	return texts, nil
}

func exampleCommands(skill skillgen.Skill) [][]word {
	var commands [][]word

	for _, rule := range skill.Rules {
		commands = append(append(commands, onesieCommands(rule.Bad)...), onesieCommands(rule.Good)...)
	}

	return commands
}

type mention struct {
	command []word
	flag    string
}

func mentions(text string) []mention {
	var found []mention

	var prose strings.Builder

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
			prose.WriteString(lines[i] + "\n")

			continue
		}

		end := i + 1
		for end < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[end]), "```") {
			end++
		}

		found = append(found, fenceMentions(lines[i+1:min(end, len(lines))])...)
		i = end
	}

	return append(inlineMentions(prose.String()), found...)
}

func fenceMentions(lines []string) []mention {
	var found []mention

	for _, line := range strings.Split(strings.ReplaceAll(strings.Join(lines, "\n"), "\\\n", " "), "\n") {
		for _, command := range onesieCommands(line) {
			found = append(found, mention{command: command})
		}
	}

	return found
}

func inlineMentions(text string) []mention {
	var found []mention

	for text != "" {
		start := strings.IndexByte(text, '`')
		if start < 0 {
			return append(found, proseFlags(text)...)
		}

		found = append(found, proseFlags(text[:start])...)

		fence := len(text[start:]) - len(strings.TrimLeft(text[start:], "`"))
		rest := text[start+fence:]

		end := strings.Index(rest, strings.Repeat("`", fence))
		if end < 0 {
			return found
		}

		found = append(found, spanMentions(strings.TrimSpace(rest[:end]))...)
		text = rest[end+fence:]
	}

	return found
}

func proseFlags(text string) []mention {
	var found []mention

	for _, match := range bareFlag.FindAllStringSubmatch(text, -1) {
		found = append(found, mention{flag: match[1]})
	}

	return found
}

func spanMentions(span string) []mention {
	if match := spanFlag.FindStringSubmatch(span); match != nil {
		return []mention{{flag: match[1]}}
	}

	var found []mention

	for _, command := range onesieCommands(span) {
		found = append(found, mention{command: command})
	}

	return found
}

func onesieCommands(s string) [][]word {
	var commands [][]word

	for _, segment := range shellSegments(s) {
		for len(segment) > 0 && envAssignment.MatchString(segment[0].raw) {
			segment = segment[1:]
		}

		if len(segment) > 0 && segment[0].raw == "onesie" {
			commands = append(commands, segment)
		}
	}

	return commands
}

type word struct {
	raw  string
	text string
}

func shellSegments(s string) [][]word {
	sc := &segmentScanner{}

	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		i = sc.step(runes, i)
	}

	sc.endSegment()

	for len(sc.outer) > 0 {
		sc.closeSubstitution()
		sc.endSegment()
	}

	return sc.segments
}

type segmentScanner struct {
	segments   [][]word
	words      []word
	raw, text  strings.Builder
	inWord     bool
	afterSlash bool
	skipNext   bool
	quote      rune
	outer      []scannerFrame
}

type scannerFrame struct {
	words []word
	quote rune
}

func (sc *segmentScanner) step(runes []rune, i int) int {
	r := runes[i]

	switch {
	case sc.afterSlash:
		sc.afterSlash = false
		sc.add(r)
	case r == '$' && i+1 < len(runes) && runes[i+1] == '(' && sc.quote != '\'':
		sc.flush()
		sc.outer = append(sc.outer, scannerFrame{words: sc.words, quote: sc.quote})
		sc.words, sc.quote = nil, 0

		return i + 1
	case sc.quote != 0:
		sc.raw.WriteRune(r)

		if r == sc.quote {
			sc.quote = 0
		} else {
			sc.text.WriteRune(r)
		}
	case r == ')' && len(sc.outer) > 0:
		sc.endSegment()
		sc.closeSubstitution()
	case r == '\\':
		sc.afterSlash, sc.inWord = true, true
		sc.raw.WriteRune(r)
	case r == '\'' || r == '"':
		sc.quote, sc.inWord = r, true
		sc.raw.WriteRune(r)
	case r == ' ' || r == '\t' || r == '\n':
		sc.flush()
	case r == '|' || r == '&' || r == ';':
		sc.endSegment()
	case r == '<' || r == '>':
		if strings.Trim(sc.raw.String(), "0123456789") == "" {
			// The 2 of 2>/dev/null names a file descriptor, not a word of the command.
			sc.inWord = false
		}

		sc.flush()

		for i+1 < len(runes) && (runes[i+1] == '&' || runes[i+1] == '>') {
			i++
		}

		sc.skipNext = true
	default:
		sc.add(r)
	}

	return i
}

func (sc *segmentScanner) add(r rune) {
	sc.raw.WriteRune(r)
	sc.text.WriteRune(r)
	sc.inWord = true
}

func (sc *segmentScanner) flush() {
	if sc.inWord {
		if sc.skipNext {
			sc.skipNext = false
		} else {
			sc.words = append(sc.words, word{raw: sc.raw.String(), text: sc.text.String()})
		}
	}

	sc.raw.Reset()
	sc.text.Reset()

	sc.inWord = false
}

func (sc *segmentScanner) endSegment() {
	sc.flush()
	sc.skipNext = false

	if len(sc.words) > 0 {
		sc.segments = append(sc.segments, sc.words)
	}

	sc.words = nil
}

func (sc *segmentScanner) closeSubstitution() {
	top := sc.outer[len(sc.outer)-1]
	sc.outer = sc.outer[:len(sc.outer)-1]
	sc.words, sc.quote = top.words, top.quote
	sc.inWord = sc.quote != 0
}

type helpBook struct {
	runner *Runner
	pages  map[string]helpPage
}

func (b *helpBook) skillFlags(ctx context.Context, prose [][]mention, examples [][]word) (map[string]bool, error) {
	commands := examples

	for _, ms := range prose {
		for _, m := range ms {
			if m.command != nil {
				commands = append(commands, m.command)
			}
		}
	}

	flags := map[string]bool{}

	if err := b.addFlags(ctx, nil, flags); err != nil {
		return nil, err
	}

	for _, command := range commands {
		_, path, err := b.checkCommand(ctx, command)
		if err != nil {
			return nil, err
		}

		if len(path) > 0 && path[0] == "completion" || asksForHelp(command) {
			continue
		}

		for n := 1; n <= len(path); n++ {
			if err := b.addFlags(ctx, path[:n], flags); err != nil {
				return nil, err
			}
		}
	}

	return flags, nil
}

func asksForHelp(command []word) bool {
	for _, w := range command {
		if w.raw == "--help" || w.raw == "-h" {
			return true
		}
	}

	return false
}

func (b *helpBook) addFlags(ctx context.Context, path []string, flags map[string]bool) error {
	page, err := b.page(ctx, path)
	if err != nil {
		return err
	}

	for name := range page.longTakesValue {
		flags[name] = true
	}

	return nil
}

func (b *helpBook) check(ctx context.Context, m mention, flags map[string]bool) ([]string, error) {
	if m.command != nil {
		problems, _, err := b.checkCommand(ctx, m.command)

		return problems, err
	}

	if flags[m.flag] {
		return nil, nil
	}

	return []string{fmt.Sprintf(
		"--%s is not in onesie --help or the --help of a command this skill runs", m.flag)}, nil
}

func (b *helpBook) checkCommand(ctx context.Context, words []word) ([]string, []string, error) {
	var (
		path     []string
		problems []string
	)

	page, err := b.page(ctx, nil)
	if err != nil {
		return nil, nil, err
	}

	subcommandNext := true

	for i := 1; i < len(words); i++ {
		w := words[i]

		switch {
		case w.raw == "--":
			return problems, path, nil
		case strings.HasPrefix(w.raw, "--"):
			name, _, joined := strings.Cut(w.raw[2:], "=")

			takesValue, known := page.longTakesValue[name]
			if !known {
				problems = append(problems, fmt.Sprintf("--%s is not a flag in %s --help", name, commandName(path)))
			} else if takesValue && !joined {
				i++
			}
		case strings.HasPrefix(w.raw, "-"):
			if len(w.raw) == 2 && page.shortTakesValue[w.raw[1:]] {
				i++
			}
		case subcommandNext && len(page.commands) > 0 && w.raw == w.text && commandShape.MatchString(w.text):
			if !page.commands[w.text] {
				problems = append(problems, fmt.Sprintf("%s is not a command in %s --help", w.text, commandName(path)))
				subcommandNext = false

				continue
			}

			path = append(path, w.text)

			if page, err = b.page(ctx, path); err != nil {
				return nil, nil, err
			}
		default:
			subcommandNext = false
		}
	}

	return problems, path, nil
}

func commandName(path []string) string {
	return strings.Join(append([]string{"onesie"}, path...), " ")
}

func (b *helpBook) page(ctx context.Context, path []string) (helpPage, error) {
	key := strings.Join(path, " ")
	if page, ok := b.pages[key]; ok {
		return page, nil
	}

	text, err := b.runner.Help(ctx, path)
	if err != nil {
		return helpPage{}, err
	}

	page := parseHelp(text)
	b.pages[key] = page

	return page, nil
}

type helpPage struct {
	longTakesValue  map[string]bool
	shortTakesValue map[string]bool
	commands        map[string]bool
}

func parseHelp(text string) helpPage {
	page := helpPage{
		longTakesValue: map[string]bool{}, shortTakesValue: map[string]bool{}, commands: map[string]bool{},
	}

	section := ""

	for _, line := range strings.Split(text, "\n") {
		if line != "" && !strings.HasPrefix(line, " ") {
			section = strings.TrimSpace(line)

			continue
		}

		switch section {
		case "Flags:", "Global Flags:":
			if match := helpFlag.FindStringSubmatch(line); match != nil {
				page.longTakesValue[match[2]] = match[3] != ""

				if match[1] != "" {
					page.shortTakesValue[match[1]] = match[3] != ""
				}
			}
		case "Available Commands:":
			if match := helpCommand.FindStringSubmatch(line); match != nil {
				page.commands[match[1]] = true
			}
		}
	}

	return page
}
