package skillcheck

import "regexp"

// trailingRedirect matches the redirections an example keeps a dry run's output out of the
// reader's way with. A dry run never runs through a shell, so these are noise rather than argv.
var trailingRedirect = regexp.MustCompile(
	`(?:\s+(?:>\s*/dev/null|[12]?>&[12]|[12]>\s*/dev/null))+\s*$`)

func tokenize(command string) (tokens []string, ok bool) {
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

		switch {
		case r == '\'':
			end, closed := scanSingleQuote(runes, i+1)
			if !closed {
				return nil, false
			}

			current = append(current, runes[i+1:end]...)
			word = true
			i = end

		case r == '"':
			end, body, safe := scanDoubleQuote(runes, i+1)
			if !safe {
				return nil, false
			}

			current = append(current, body...)
			word = true
			i = end

		case r == ' ' || r == '\t' || r == '\n':
			flush()

		// A pipe, a chain, a redirection or an expansion needs a shell to resolve, and this
		// package never hands a parsed command to one. The whole example is skipped rather than
		// guessed at.
		case isShellMeta(r):
			return nil, false

		case r == '\\':
			if i+1 >= len(runes) {
				return nil, false
			}

			current = append(current, runes[i+1])
			word = true
			i++

		default:
			current = append(current, r)
			word = true
		}
	}

	flush()

	if len(tokens) == 0 {
		return nil, false
	}

	return tokens, true
}

func isShellMeta(r rune) bool {
	switch r {
	case '|', '&', ';', '<', '>', '(', ')', '`', '$':
		return true
	default:
		return false
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

func scanDoubleQuote(runes []rune, start int) (end int, body []rune, safe bool) {
	for i := start; i < len(runes); i++ {
		switch r := runes[i]; r {
		case '"':
			return i, body, true

		case '$', '`':
			return 0, nil, false

		case '\\':
			if i+1 >= len(runes) {
				return 0, nil, false
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

	return 0, nil, false
}
