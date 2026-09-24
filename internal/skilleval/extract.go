package skilleval

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const placeholder = "{}"

var (
	keywords = []string{
		"if", "then", "do", "else", "elif", "while", "until", "time", "!", "{", "exec", "command", "env",
	}

	assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
)

func jevCommands(script string) []string {
	script = strings.ReplaceAll(script, "\\\n", " ")

	var commands []string

	for line := range strings.Lines(script) {
		commands = append(commands, lineCommands(trimYAMLRun(strings.TrimSpace(line)))...)
	}

	return commands
}

func trimYAMLRun(line string) string {
	line = strings.TrimPrefix(line, "- ")

	if rest, found := strings.CutPrefix(line, "run:"); found {
		return strings.TrimSpace(rest)
	}

	return line
}

func lineCommands(line string) []string {
	runes := []rune(line)

	var commands []string

	quotes := []rune{0}
	commandPosition := true

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		switch quotes[len(quotes)-1] {
		case '\'':
			if r == '\'' {
				quotes = quotes[:len(quotes)-1]
			}

			continue

		case '"':
			switch {
			case r == '"':
				quotes = quotes[:len(quotes)-1]
			case r == '\\':
				i++
			case r == '$' && i+1 < len(runes) && runes[i+1] == '(':
				quotes = append(quotes, 0)
				commandPosition = true
				i++
			}

			continue
		}

		switch {
		case r == ' ' || r == '\t' || r == '\n':
		case r == '\'' || r == '"':
			quotes = append(quotes, r)
			commandPosition = false
		case r == '\\':
			i++
			commandPosition = false
		case r == '$' && i+1 < len(runes) && runes[i+1] == '(':
			quotes = append(quotes, 0)
			commandPosition = true
			i++
		case r == ')':
			if len(quotes) > 1 {
				quotes = quotes[:len(quotes)-1]
			}

			commandPosition = true
		case r == '(' || r == '`' || r == '|' || r == '&' || r == ';':
			commandPosition = true
		case r == '#' && (i == 0 || unicode.IsSpace(runes[i-1])):
			return commands
		case r == '<' || r == '>':
			commandPosition = false
		default:
			end := max(wordEnd(runes, i), i+1)
			word := string(runes[i:end])

			if commandPosition && (word == "jev" || strings.HasSuffix(word, "/jev")) {
				commands = append(commands, readCommand(runes, end-len("jev")))
			}

			commandPosition = commandPosition && (slices.Contains(keywords, word) || assignment.MatchString(word))
			i = end - 1
		}
	}

	return commands
}

func wordEnd(runes []rune, start int) int {
	for i := start; i < len(runes); i++ {
		if unicode.IsSpace(runes[i]) || strings.ContainsRune("|&;()<>'\"$`\\", runes[i]) {
			return i
		}
	}

	return len(runes)
}

func readCommand(runes []rune, start int) string {
	var out strings.Builder

	double := false

	for i := start; i < len(runes); i++ {
		r := runes[i]

		switch {
		case r == '$':
			i = skipExpansion(runes, i)
			out.WriteString(quotedPlaceholder(double))

		case r == '`':
			i = skipTo(runes, i+1, '`')
			out.WriteString(quotedPlaceholder(double))

		case r == '\\' && i+1 < len(runes):
			out.WriteRune(r)
			out.WriteRune(runes[i+1])
			i++

		case double:
			if r == '"' {
				double = false
			}

			out.WriteRune(r)

		case r == '"':
			double = true
			out.WriteRune(r)

		case r == '\'':
			end := skipTo(runes, i+1, '\'')
			out.WriteString(string(runes[i:min(end+1, len(runes))]))
			i = end

		case r == '|' || r == '&' || r == ';' || r == ')':
			return strings.TrimSpace(out.String())

		case r == '#' && unicode.IsSpace(runes[i-1]):
			return strings.TrimSpace(out.String())

		case r == '<' || r == '>':
			trimmed := dropFileDescriptor(out.String())
			out.Reset()
			out.WriteString(trimmed)
			i = skipRedirection(runes, i)

		default:
			out.WriteRune(r)
		}
	}

	return strings.TrimSpace(out.String())
}

func quotedPlaceholder(insideDoubleQuotes bool) string {
	if insideDoubleQuotes {
		return placeholder
	}

	return "'" + placeholder + "'"
}

func skipExpansion(runes []rune, start int) int {
	if start+1 >= len(runes) {
		return start
	}

	switch next := runes[start+1]; {
	case next == '(':
		depth := 0

		for i := start + 1; i < len(runes); i++ {
			switch runes[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return i
				}
			}
		}

		return len(runes) - 1

	case next == '{' && start+2 < len(runes) && runes[start+2] == '{':
		return skipPast(runes, start+3, "}}")

	case next == '{':
		return skipTo(runes, start+2, '}')

	case unicode.IsLetter(next) || next == '_':
		i := start + 1
		for i+1 < len(runes) && (unicode.IsLetter(runes[i+1]) || unicode.IsDigit(runes[i+1]) || runes[i+1] == '_') {
			i++
		}

		return i

	default:
		return start + 1
	}
}

func skipTo(runes []rune, start int, closing rune) int {
	for i := start; i < len(runes); i++ {
		if runes[i] == closing {
			return i
		}
	}

	return len(runes) - 1
}

func skipPast(runes []rune, start int, closing string) int {
	target := []rune(closing)

	for i := start; i+len(target) <= len(runes); i++ {
		if slices.Equal(runes[i:i+len(target)], target) {
			return i + len(target) - 1
		}
	}

	return len(runes) - 1
}

func skipDoubleQuote(runes []rune, start int) int {
	for i := start; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}

	return len(runes) - 1
}

func dropFileDescriptor(s string) string {
	if n := len(s); n > 0 && s[n-1] >= '0' && s[n-1] <= '9' && (n == 1 || s[n-2] == ' ') {
		return s[:n-1]
	}

	return s
}

func skipRedirection(runes []rune, start int) int {
	i := start
	for i+1 < len(runes) && (runes[i+1] == '>' || runes[i+1] == '<') {
		i++
	}

	if i+1 < len(runes) && runes[i+1] == '&' {
		i++
		for i+1 < len(runes) && (unicode.IsDigit(runes[i+1]) || runes[i+1] == '-') {
			i++
		}

		return i
	}

	for i+1 < len(runes) && unicode.IsSpace(runes[i+1]) {
		i++
	}

	for i+1 < len(runes) && !unicode.IsSpace(runes[i+1]) {
		i++

		switch runes[i] {
		case '\'':
			i = skipTo(runes, i+1, '\'')
		case '"':
			i = skipDoubleQuote(runes, i+1)
		case '$':
			i = skipExpansion(runes, i)
		}
	}

	return i
}
