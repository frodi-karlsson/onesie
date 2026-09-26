package proseblocks

import (
	"regexp"
	"strings"
)

var (
	listItem       = regexp.MustCompile(`^\s*(?:[-*+]|\d{1,9}[.)])(?:\s+|$)`)
	heading        = regexp.MustCompile(`^#{1,6}(?:\s|$)`)
	delimiterRow   = regexp.MustCompile(`^\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?$`)
	inlineComment  = regexp.MustCompile(`<!--.*?-->`)
	setextLine     = regexp.MustCompile(`^(?:=+|-+)$`)
	thematicBreak  = regexp.MustCompile(`^(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	blockquoteMark = regexp.MustCompile(`^(?:>\s?)+`)
)

func markdownBlocks(path string, src []byte) []span {
	p := &markdownParser{path: path}

	for i, line := range strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n") {
		p.line(i+1, line)
	}

	p.flush()

	return p.blocks
}

type markdownParser struct {
	path   string
	blocks []span

	start  int
	last   int
	text   []string
	inItem bool
	// listOpen is true from a list item until a paragraph outside the list, so an indented
	// paragraph under an item reads as prose and not as an indented code block.
	listOpen bool

	fence       string
	inComment   bool
	inTable     bool
	frontMatter bool
}

func (p *markdownParser) line(n int, raw string) {
	if p.skipped(n, raw) {
		return
	}

	trimmed := strings.TrimSpace(blockquoteMark.ReplaceAllString(strings.TrimSpace(raw), ""))

	switch {
	case trimmed == "":
		p.flush()
		p.inTable = false
	case p.inTable:
	case heading.MatchString(trimmed), isHTMLLine(trimmed):
		p.flush()
	case strings.HasPrefix(trimmed, "|"):
		p.flush()
		p.inTable = true
	case strings.Contains(trimmed, "|") && delimiterRow.MatchString(trimmed):
		p.drop()
		p.inTable = true
	case setextLine.MatchString(trimmed) && len(p.text) > 0 && !p.inItem:
		p.drop()
	case thematicBreak.MatchString(trimmed):
		p.flush()
	case listItem.MatchString(raw):
		p.flush()
		p.begin(n, listItem.ReplaceAllString(raw, ""), true)
		p.listOpen = true
	case len(p.text) > 0:
		p.text = append(p.text, trimmed)
		p.last = n
	case indent(raw) >= 4 && !p.listOpen:
	default:
		p.listOpen = p.listOpen && indent(raw) > 0
		p.begin(n, trimmed, false)
	}
}

func (p *markdownParser) skipped(n int, raw string) bool {
	trimmed := strings.TrimSpace(raw)

	switch {
	case n == 1 && trimmed == "---":
		p.frontMatter = true
	case p.frontMatter:
		p.frontMatter = trimmed != "---" && trimmed != "..."
	case p.fence != "":
		if closesFence(trimmed, p.fence) {
			p.fence = ""
		}
	case p.inComment:
		p.inComment = !strings.Contains(trimmed, "-->")
	case opensFence(trimmed) != "":
		p.flush()
		p.fence = opensFence(trimmed)
	case strings.HasPrefix(trimmed, "<!--"):
		p.flush()
		p.inComment = !strings.Contains(trimmed[len("<!--"):], "-->")
	default:
		return false
	}

	return true
}

func (p *markdownParser) begin(n int, text string, item bool) {
	p.start = n
	p.last = n
	p.text = []string{strings.TrimSpace(text)}
	p.inItem = item
}

func (p *markdownParser) flush() {
	if len(p.text) > 0 {
		text := inlineComment.ReplaceAllString(strings.Join(p.text, " "), "")
		if block, ok := newBlock(p.path, p.start, p.last, text); ok {
			p.blocks = append(p.blocks, block)
		}
	}

	p.drop()
}

func (p *markdownParser) drop() {
	p.text = nil
	p.inItem = false
}

func opensFence(trimmed string) string {
	for _, marker := range []byte{'`', '~'} {
		run := len(trimmed) - len(strings.TrimLeft(trimmed, string(marker)))
		if run >= 3 {
			return trimmed[:run]
		}
	}

	return ""
}

func closesFence(trimmed, fence string) bool {
	return strings.HasPrefix(trimmed, fence) && strings.Trim(trimmed, fence[:1]) == ""
}

func isHTMLLine(trimmed string) bool {
	return strings.HasPrefix(trimmed, "<") && strings.HasSuffix(trimmed, ">")
}

func indent(raw string) int {
	width := 0

	for _, r := range raw {
		switch r {
		case ' ':
			width++
		case '\t':
			width += 4
		default:
			return width
		}
	}

	return width
}
