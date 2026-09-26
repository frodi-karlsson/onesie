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
	bareFlag     = regexp.MustCompile(`(?:^|[^\w-])--([A-Za-z][\w-]*)`)
	commandShape = regexp.MustCompile(`^[a-z][a-z-]*$`)
	spanFlag     = regexp.MustCompile(`^--([A-Za-z][\w-]*)`)
	helpFlag     = regexp.MustCompile(`^\s+(?:-(\w), )?--(\w[\w-]*)(?: (\S+))?(?:\s{2,}|$)`)
	helpCommand  = regexp.MustCompile(`^\s+(\S+)\s{2,}`)
)

// CheckProse checks every flag and subcommand the skills name outside their examples against the
// --help output of runner's binary. It reads the description, the compatibility, each rule's short
// and why, the intro, the sections and the references.
//
// It counts three kinds of mention, so the flags of other tools are never read as onesie's:
//
//   - A code span, or a line of a fenced block, whose first word is onesie. Its flags are checked
//     against the help of the command they follow, the root before any subcommand and, after
//     `onesie calibrate` or `onesie auth set`, that subcommand's own. A bare word where a
//     subcommand can stand must be one. The mention ends at a pipe, a chain, a redirection or a
//     lone --, so `onesie ... | jq -c --unbuffered` checks nothing after the pipe.
//   - A code span whose first word is a --flag, such as `--ask NAME=@FILE`. Only that flag counts.
//   - A --flag in plain prose, outside any code.
//
// A code span that starts with anything else, such as `jq --unbuffered`, `git push --force` or
// `curl -fsSL`, is ignored. The last two kinds name no command, so they pass when any onesie
// command's help lists the flag. Short flags are not checked.
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

		for _, text := range texts {
			for _, m := range mentions(text.body) {
				problems, err := book.check(ctx, m)
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
		if m, ok := spanMention(strings.TrimSpace(line)); ok && m.command != nil {
			found = append(found, m)
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

		if m, ok := spanMention(strings.TrimSpace(rest[:end])); ok {
			found = append(found, m)
		}

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

func spanMention(span string) (mention, bool) {
	words := shellWords(span)
	if len(words) == 0 {
		return mention{}, false
	}

	switch first := words[0]; {
	case first.raw == "onesie":
		return mention{command: words}, true
	case spanFlag.MatchString(first.raw):
		return mention{flag: spanFlag.FindStringSubmatch(first.raw)[1]}, true
	default:
		return mention{}, false
	}
}

type word struct {
	raw  string
	text string
}

func shellWords(s string) []word {
	var (
		words      []word
		raw, text  strings.Builder
		inWord     bool
		quote      rune
		afterSlash bool
	)

	flush := func() {
		if inWord {
			words = append(words, word{raw: raw.String(), text: text.String()})
		}

		raw.Reset()
		text.Reset()

		inWord = false
	}

	for _, r := range s {
		switch {
		case afterSlash:
			afterSlash = false

			raw.WriteRune(r)
			text.WriteRune(r)
		case quote != 0:
			raw.WriteRune(r)

			if r == quote {
				quote = 0
			} else {
				text.WriteRune(r)
			}
		case r == '\\':
			afterSlash, inWord = true, true

			raw.WriteRune(r)
		case r == '\'' || r == '"':
			quote, inWord = r, true

			raw.WriteRune(r)
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		case strings.ContainsRune("|&;<>", r):
			// The rest belongs to another command or to the shell, and a word cut short by it,
			// such as the 2 of 2>&1, is not part of the onesie command either.
			inWord = false

			flush()

			return words
		default:
			inWord = true

			raw.WriteRune(r)
			text.WriteRune(r)
		}
	}

	flush()

	return words
}

type helpBook struct {
	runner *Runner
	pages  map[string]helpPage
	every  map[string]bool
}

func (b *helpBook) check(ctx context.Context, m mention) ([]string, error) {
	if m.command == nil {
		return b.checkFlag(ctx, m.flag)
	}

	return b.checkCommand(ctx, m.command)
}

func (b *helpBook) checkFlag(ctx context.Context, name string) ([]string, error) {
	if b.every == nil {
		every := map[string]bool{}
		if err := b.collect(ctx, nil, every); err != nil {
			return nil, err
		}

		b.every = every
	}

	if b.every[name] {
		return nil, nil
	}

	return []string{fmt.Sprintf("--%s is in the --help of no onesie command", name)}, nil
}

func (b *helpBook) collect(ctx context.Context, path []string, every map[string]bool) error {
	page, err := b.page(ctx, path)
	if err != nil {
		return err
	}

	for name := range page.longTakesValue {
		every[name] = true
	}

	for name := range page.commands {
		if err := b.collect(ctx, append(append([]string{}, path...), name), every); err != nil {
			return err
		}
	}

	return nil
}

func (b *helpBook) checkCommand(ctx context.Context, words []word) ([]string, error) {
	var (
		path     []string
		problems []string
	)

	page, err := b.page(ctx, nil)
	if err != nil {
		return nil, err
	}

	subcommandNext := true

	for i := 1; i < len(words); i++ {
		w := words[i]

		switch {
		case w.raw == "--":
			return problems, nil
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
				return nil, err
			}
		default:
			subcommandNext = false
		}
	}

	return problems, nil
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
