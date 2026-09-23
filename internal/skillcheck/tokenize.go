package skillcheck

import "regexp"

// The forms an example keeps a dry run's output out of the reader's way with. A dry run never
// runs through a shell, so these are noise rather than argv.
var trailingRedirect = regexp.MustCompile(
	`(?:\s+(?:>\s*/dev/null|[12]?>&[12]|[12]>\s*/dev/null))+\s*$`)

// reason is empty on success. On failure it names, in words a skill author can act on, the one
// thing about the command this package refuses to guess at: a pipe, a chain, a redirection, an
// expansion, a glob, or malformed quoting.
func tokenize(command string) (tokens []string, reason string) {
	command = trailingRedirect.ReplaceAllString(command, "")

	var current []rune

	word := false

	flush := func() {
		if word {
			tokens = append(tokens, string(current))
			current = nil
			word = false
		}
	}

	runes := []rune(command)

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		switch r {
		case '\'':
			end, closed := scanSingleQuote(runes, i+1)
			if !closed {
				return nil, "an unterminated single quote"
			}

			current = append(current, runes[i+1:end]...)
			word = true
			i = end

		case '"':
			end, body, quoteReason := scanDoubleQuote(runes, i+1)
			if quoteReason != "" {
				return nil, quoteReason
			}

			current = append(current, body...)
			word = true
			i = end

		case ' ', '\t', '\n':
			flush()

		case '\\':
			if i+1 >= len(runes) {
				return nil, "a trailing backslash"
			}

			current = append(current, runes[i+1])
			word = true
			i++

		default:
			// A pipe, a chain, a redirection, an expansion or a glob needs a shell, a filesystem
			// or an environment to resolve, and this package hands a parsed command to none of
			// them. The whole example is skipped rather than guessed at.
			if hazard := shellHazard(r); hazard != "" {
				return nil, "contains " + hazard
			}

			current = append(current, r)
			word = true
		}
	}

	flush()

	if len(tokens) == 0 {
		return nil, "an empty command"
	}

	return tokens, ""
}

func shellHazard(r rune) string {
	switch r {
	case '|':
		return "a pipe"
	case '&':
		return "a background operator"
	case ';':
		return "a command separator"
	case '<', '>':
		return "a redirection"
	case '(', ')':
		return "a subshell"
	case '`':
		return "a command substitution"
	case '$':
		return "a variable or a command substitution"
	case '*', '?', '[':
		return "a glob"
	case '{':
		return "a brace expansion"
	case '~':
		return "a home directory expansion"
	case '#':
		return "a comment marker"
	default:
		return ""
	}
}

func scanSingleQuote(runes []rune, start int) (end int, closed bool) {
	for i := start; i < len(runes); i++ {
		if runes[i] == '\'' {
			return i, true
		}
	}

	return 0, false
}

func scanDoubleQuote(runes []rune, start int) (end int, body []rune, reason string) {
	for i := start; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '"':
			return i, body, ""

		case '$', '`':
			return 0, nil, "a variable or a command substitution"

		case '\\':
			if i+1 >= len(runes) {
				return 0, nil, "an unterminated double quote"
			}

			switch next := runes[i+1]; next {
			case '\\', '"', '$', '`':
				body = append(body, next)
				i++
			default:
				body = append(body, r)
			}

		default:
			body = append(body, r)
		}
	}

	return 0, nil, "an unterminated double quote"
}
