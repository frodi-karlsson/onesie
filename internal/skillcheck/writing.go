package skillcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/skillgen"
)

var (
	linkTarget   = regexp.MustCompile(`\]\([^)\s]*\)`)
	bareURL      = regexp.MustCompile(`<?https?://[^\s>]*>?`)
	letterHyphen = regexp.MustCompile(`\pL-\pL`)
)

// CheckWriting checks the punctuation of the four root docs and of the skill prose CheckProse reads,
// outside code spans, fenced blocks, link targets, URLs and table rows.
func CheckWriting(root string) (WritingReport, error) {
	texts, err := writtenTexts(root)
	if err != nil {
		return WritingReport{}, err
	}

	report := WritingReport{Checked: len(texts)}

	for _, text := range texts {
		for _, problem := range writingProblems(text.body) {
			report.Failures = append(report.Failures,
				fmt.Sprintf("onesie: %s, line %d: %s", text.label, problem.line, problem.what))
		}
	}

	return report, nil
}

// WritingReport tallies one run of CheckWriting.
type WritingReport struct {
	Checked  int
	Failures []string
}

func writtenTexts(root string) ([]proseText, error) {
	var texts []proseText

	for _, name := range []string{"README.md", "REFERENCE.md", "CONTRIBUTING.md", "MAINTAINING.md"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return nil, fmt.Errorf("onesie: %w", err)
		}

		texts = append(texts, proseText{label: name, body: string(data)})
	}

	found, err := skillgen.Skills(root)
	if err != nil {
		return nil, err
	}

	for _, f := range found {
		skillTexts, err := proseTexts(filepath.Join(root, "skills", f.Dir), f.Skill)
		if err != nil {
			return nil, err
		}

		for _, text := range skillTexts {
			label := "skill '" + f.Skill.Name + "' " + text.label
			texts = append(texts, proseText{label: label, body: text.body})
		}
	}

	return texts, nil
}

func writingProblems(text string) []writingProblem {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	blankUncheckedLines(lines)

	var found []writingProblem

	for i, line := range strings.Split(blankCodeSpans(strings.Join(lines, "\n")), "\n") {
		for _, what := range sentenceProblems(line) {
			found = append(found, writingProblem{line: i + 1, what: what})
		}
	}

	return found
}

type writingProblem struct {
	line int
	what string
}

func blankUncheckedLines(lines []string) {
	fenced := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "```"):
			fenced = !fenced
			lines[i] = ""
		case fenced, strings.HasPrefix(trimmed, "|"):
			lines[i] = ""
		}
	}
}

func blankCodeSpans(text string) string {
	var out strings.Builder

	for {
		start := strings.IndexByte(text, '`')
		if start < 0 {
			break
		}

		fence := len(text[start:]) - len(strings.TrimLeft(text[start:], "`"))
		rest := text[start+fence:]

		end := strings.Index(rest, strings.Repeat("`", fence))
		if end < 0 {
			break
		}

		out.WriteString(text[:start])
		out.WriteString(blankKeepingLines(text[start : start+fence+end+fence]))
		text = rest[end+fence:]
	}

	out.WriteString(text)

	return out.String()
}

func blankKeepingLines(span string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}

		return ' '
	}, span)
}

func sentenceProblems(line string) []string {
	marks := []struct{ chars, name string }{
		{chars: ";", name: "a semicolon"},
		{chars: "?", name: "a question mark"},
		{chars: "()", name: "a parenthesis"},
		{chars: "\u2014", name: "an em dash"},
		{chars: "\u2013", name: "an en dash"},
	}

	var found []string

	for _, word := range strings.Fields(bareURL.ReplaceAllString(linkTarget.ReplaceAllString(line, "]"), " ")) {
		if strings.HasPrefix(word, "-") {
			continue
		}

		for _, mark := range marks {
			if strings.ContainsAny(word, mark.chars) {
				found = append(found, fmt.Sprintf("%s in '%s'", mark.name, word))
			}
		}

		if letterHyphen.MatchString(word) && !hyphenatedName(word) {
			found = append(found, fmt.Sprintf("a hyphen between two letters in '%s'", word))
		}
	}

	return found
}

func hyphenatedName(word string) bool {
	switch strings.TrimRight(strings.TrimLeft(word, `*"'[`), `*"'.,:!]`) {
	case "onesie-calibrate", "onesie-gate", "onesie-patterns", "onesie-setup", "onesie-stream",
		"personal-data", "prompt-injection", "shell-safety":
		return true
	default:
		return false
	}
}
