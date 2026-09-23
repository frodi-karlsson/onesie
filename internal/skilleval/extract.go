package skilleval

import (
	"slices"
	"strings"
	"unicode"
)

// Stands in for every shell variable and command substitution in a command. A dry run needs a
// literal where the shell would have expanded one, and a state value is the usual place one sits.
const placeholder = "x"

// A word that leaves the next word in command position, so `if jev ...` is still a jev command.
var keywords = []string{"if", "then", "do", "else", "elif", "while", "until", "time", "!"}

func jevCommands(script string) []string {
	script = strings.ReplaceAll(script, "\\\n", " ")

	var commands []string

	for line := range strings.Lines(script) {
		commands = append(commands, lineCommands(trimYAMLRun(strings.TrimSpace(line)))...)
	}

	return commands
}

func trimYAMLRun(line string) string {
	// A CI answer writes its commands as YAML, where `- run: jev ...` holds a command after a key.
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

			if commandPosition && word == "jev" {
				commands = append(commands, readCommand(runes, i))
			}

			commandPosition = commandPosition && (slices.Contains(keywords, word) || strings.HasSuffix(word, "="))
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
	// Every variable and substitution becomes the placeholder and every redirection is dropped,
	// since the dry run runs argv directly and never hands any of it to a shell.
	var out strings.Builder

	double := false

	for i := start; i < len(runes); i++ {
		r := runes[i]

		switch {
		case r == '$':
			i = skipExpansion(runes, i)
			out.WriteString(placeholder)

		case r == '`':
			i = skipTo(runes, i+1, '`')
			out.WriteString(placeholder)

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
	}

	return i
}
