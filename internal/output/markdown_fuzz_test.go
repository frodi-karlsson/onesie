package output

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/frodi-karlsson/onesie/internal/answer"
)

var (
	afterSpan    = regexp.MustCompile(`^(, [0-9.<>]+)?$`)
	entityDigits = regexp.MustCompile(`^[0-9]{1,7}$`)
)

func FuzzCodeSpan(f *testing.F) {
	for _, seed := range []string{
		"T-1", "@someone", "#12", "<b>bold</b>", "a ``b`` c", "`x", "x`", "a|b", `a\|b`,
		"<b>@x</b> & *y* | `z`", "a&b", "line one\nline two", "a\r\nb\tc", "\x1b[31mred",
		" a ", "  ", " a", "", "`", "``", " ` ", "_empty_", "a b", "\xff", "\x85",
	} {
		f.Add(seed, false)
		f.Add(seed, true)
	}

	f.Fuzz(func(t *testing.T, text string, inTable bool) {
		rendered := codeSpan(text, inTable)

		content, rest, err := readSpan(rendered, inTable)
		if err != nil {
			t.Fatalf("codeSpan(%q, %v) = %q, which %v", text, inTable, rendered, err)
		}

		if rest != "" {
			t.Fatalf("codeSpan(%q, %v) = %q, which leaves %q after the span", text, inTable, rendered, rest)
		}

		if want := spanText(text); content != want {
			t.Fatalf("codeSpan(%q, %v) = %q, which reads back as %q, want %q",
				text, inTable, rendered, content, want)
		}
	})
}

func FuzzMarkdownTable(f *testing.F) {
	f.Add("@team|#12", "<b>`x`</b>", "", "a\nb", "line 2: <b>bad</b>\n`x` \\| y", "jev-1.13.0")
	f.Add("a|b", "2", "furious", "", "", "")
	f.Add("", "", "", "", "", "")
	f.Add("`", "|", "\\", "\r", "\\|", "_")
	f.Add(" a ", "``", "`", " ", "\x00", " ")

	f.Fuzz(func(t *testing.T, question, level, legend, gate, message, model string) {
		confidence := 0.5
		named := []Named{
			{ID: question, Answer: &answer.Answer{
				Value: level, Legend: map[string]string{level: legend}, Confidence: &confidence,
			}},
			{ID: "decided", Answer: &answer.Answer{Value: 0.5, Decided: true, Decision: level}},
		}

		var single bytes.Buffer

		rec := Record{ID: message, Model: model, Answers: named, AssertFailed: true}
		if err := WriteMarkdown(&single, rec, MarkdownOptions{Assert: gate}); err != nil {
			t.Fatalf("WriteMarkdown() error = %v", err)
		}

		rows := checkMarkdown(t, single.String(), 3)
		if len(rows) != 4 {
			t.Fatalf("wrote %d table rows, want 4\n%s", len(rows), single.String())
		}

		for i, want := range []string{spanText(question), spanText(level + " " + legend)} {
			if got, _, _ := readSpan(rows[2][i], true); got != want {
				t.Fatalf("cell %q reads back as %q, want %q", rows[2][i], got, want)
			}
		}

		var stream bytes.Buffer

		table := NewMarkdownTable(&stream, MarkdownTableOptions{IDs: []string{question, "decided"}, ID: true, Gate: true})
		for _, rec := range []Record{
			rec,
			{ID: level, Model: legend, Failure: &Failure{Kind: "input", Message: message}},
			{ID: 1.5, Answers: named, Abstained: true},
		} {
			if err := table.Write(rec); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
		}

		if err := table.Finish(false); err != nil {
			t.Fatalf("Finish() error = %v", err)
		}

		if rows := checkMarkdown(t, stream.String(), 5); len(rows) != 5 {
			t.Fatalf("wrote %d table rows, want 5\n%s", len(rows), stream.String())
		}
	})
}

func checkMarkdown(t *testing.T, written string, columns int) [][]string {
	t.Helper()

	if !strings.HasSuffix(written, "\n") || strings.Contains(written, "\r") {
		t.Fatalf("wrote %q, want lines ending in a newline and no carriage return", written)
	}

	var rows [][]string

	for line := range strings.Lines(written) {
		line = strings.TrimSuffix(line, "\n")

		switch {
		case line == "", strings.HasPrefix(line, "> "):
		case strings.HasPrefix(line, "_") && strings.HasSuffix(line, "_"):
		case strings.HasPrefix(line, "|"):
			rows = append(rows, checkRow(t, line, columns))
		default:
			t.Fatalf("wrote a line %q that is no table row, alert, footer or blank\nin\n%s", line, written)
		}
	}

	return rows
}

func checkRow(t *testing.T, line string, columns int) []string {
	t.Helper()

	if !strings.HasSuffix(line, "|") {
		t.Fatalf("row %q does not end in a pipe", line)
	}

	// Every pipe splits a GFM cell, escaped or not, since a code span does not protect one either.
	cells := strings.Split(line[1:len(line)-1], "|")
	if len(cells) != columns {
		t.Fatalf("row %q splits into %d cells, want %d", line, len(cells), columns)
	}

	for i, cell := range cells {
		// GFM trims only spaces and tabs around a cell, so any other white space stays in it.
		cells[i] = strings.Trim(cell, " \t")
		if !strings.HasPrefix(cells[i], "`") && !strings.HasPrefix(cells[i], "<code>") {
			continue
		}

		_, rest, err := readSpan(cells[i], true)
		if err != nil {
			t.Fatalf("cell %q of row %q %v", cells[i], line, err)
		}

		// A decision is followed by its probability in the same cell.
		if !afterSpan.MatchString(rest) {
			t.Fatalf("cell %q of row %q leaves %q after its span", cells[i], line, rest)
		}
	}

	return cells
}

func spanText(text string) string {
	var b strings.Builder

	for _, r := range text {
		if unicode.IsControl(r) {
			r = ' '
		}

		b.WriteRune(r)
	}

	return b.String()
}

func readSpan(rendered string, inTable bool) (text, rest string, err error) {
	if strings.ContainsAny(rendered, "\n\r") {
		return "", "", errors.New("holds a line ending")
	}

	if inTable && strings.Contains(rendered, "|") {
		return "", "", errors.New("holds a pipe, which splits a table cell even inside a code span")
	}

	if rest, empty := strings.CutPrefix(rendered, "_empty_"); empty {
		return "", rest, nil
	}

	if strings.HasPrefix(rendered, "<code>") {
		if !inTable {
			return "", "", errors.New("is html code outside a table")
		}

		return readHTMLCode(rendered)
	}

	return readBacktickSpan(rendered)
}

func readHTMLCode(rendered string) (string, string, error) {
	inner, rest, closed := strings.Cut(strings.TrimPrefix(rendered, "<code>"), "</code>")
	if !closed {
		return "", "", errors.New("opens a code element it does not close")
	}

	var b strings.Builder

	for inner != "" {
		if inner[0] == '&' {
			entity, after, ended := strings.Cut(inner[1:], ";")
			digits, numeric := strings.CutPrefix(entity, "#")

			// CommonMark reads 1 to 7 decimal digits and nothing else, and 0 as U+FFFD.
			code, err := strconv.Atoi(digits)
			if !ended || !numeric || err != nil || !entityDigits.MatchString(digits) {
				return "", "", errors.New("has an ampersand that starts no numeric entity")
			}

			if code == 0 || !utf8.ValidRune(rune(code)) {
				code = unicode.ReplacementChar
			}

			b.WriteRune(rune(code))
			inner = after

			continue
		}

		r, size := utf8.DecodeRuneInString(inner)
		if r <= unicode.MaxASCII && (unicode.IsPunct(r) || unicode.IsSymbol(r)) {
			return "", "", fmt.Errorf("leaves %q unencoded, which markdown may read as markup", r)
		}

		b.WriteRune(r)
		inner = inner[size:]
	}

	return b.String(), rest, nil
}

func readBacktickSpan(rendered string) (string, string, error) {
	fence := len(rendered) - len(strings.TrimLeft(rendered, "`"))
	if fence == 0 {
		return "", "", errors.New("opens no code span")
	}

	// The span closes at the first backtick run as long as the one that opened it.
	at := fence

	for {
		start := strings.IndexByte(rendered[at:], '`')
		if start < 0 {
			return "", "", errors.New("opens a code span it does not close")
		}

		start += at
		end := start + len(rendered[start:]) - len(strings.TrimLeft(rendered[start:], "`"))

		if end-start != fence {
			at = end

			continue
		}

		return stripSpace(rendered[fence:start]), rendered[end:], nil
	}
}

func stripSpace(inner string) string {
	// CommonMark strips one space from each end when both ends are spaces, unless every character
	// is one, and by a space it means U+0020 alone.
	if len(inner) >= 2 && inner[0] == ' ' && inner[len(inner)-1] == ' ' && strings.Trim(inner, " ") != "" {
		return inner[1 : len(inner)-1]
	}

	return inner
}
