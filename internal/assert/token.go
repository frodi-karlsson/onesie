// Package assert evaluates a boolean expression over a normalized record. It performs no IO and
// knows nothing about the command line.
package assert

import (
	"errors"
	"fmt"
	"strconv"
	"unicode"
)

func lex(expr string) ([]token, error) {
	runes := []rune(expr)
	tokens := []token{}

	for i := 0; i < len(runes); {
		var (
			tok  token
			next int
			err  error
		)

		switch {
		case unicode.IsSpace(runes[i]):
			i++

			continue
		case runes[i] == '\'':
			return nil, singleQuoted(runes, i)
		case runes[i] == '"':
			tok, next, err = lexString(runes, i)
		case startsIdent(runes, i):
			tok, next = lexIdent(runes, i)
		case startsNumber(runes, i):
			tok, next, err = lexNumber(runes, i)
		default:
			tok, next, err = lexOperator(runes, i)
		}
		if err != nil {
			return nil, err
		}

		tokens = append(tokens, tok)
		i = next
	}

	return append(tokens, token{kind: kindEOF, col: len(runes) + 1}), nil
}

func singleQuoted(runes []rune, i int) error {
	end := i + 1
	for end < len(runes) && runes[end] != '\'' {
		end++
	}

	// The closing quote is reprinted even when the input ran out without one, so the message shows
	// the form the user meant to write rather than a dangling quote.
	return fmt.Errorf("strings use double quotes, got '%s'", string(runes[i+1:end]))
}

func lexString(runes []rune, i int) (token, int, error) {
	start := i
	for i++; i < len(runes) && runes[i] != '"'; i++ {
		if runes[i] == '\\' {
			i++
		}
	}
	if i >= len(runes) {
		return token{}, 0, errors.New("unterminated string")
	}

	raw := string(runes[start : i+1])
	text, err := strconv.Unquote(raw)
	if err != nil {
		return token{}, 0, fmt.Errorf("invalid string %s", raw)
	}

	return token{kind: kindString, text: text, col: start + 1}, i + 1, nil
}

func startsIdent(runes []rune, i int) bool {
	return unicode.IsLetter(runes[i]) || runes[i] == '_'
}

func lexIdent(runes []rune, i int) (token, int) {
	start := i
	for ; i < len(runes) && (startsIdent(runes, i) || unicode.IsDigit(runes[i])); i++ {
	}

	text := string(runes[start:i])

	return token{kind: keywordKind(text), text: text, col: start + 1}, i
}

func keywordKind(text string) kind {
	switch text {
	case "and":
		return kindAnd
	case "or":
		return kindOr
	case "not":
		return kindNot
	case "in":
		return kindIn
	case "true":
		return kindTrue
	case "false":
		return kindFalse
	default:
		return kindIdent
	}
}

func startsNumber(runes []rune, i int) bool {
	if unicode.IsDigit(runes[i]) {
		return true
	}

	// There is no arithmetic, so a leading minus can only open a negative literal.
	return runes[i] == '-' && i+1 < len(runes) && unicode.IsDigit(runes[i+1])
}

func lexNumber(runes []rune, i int) (token, int, error) {
	start := i
	for i++; i < len(runes) && (unicode.IsDigit(runes[i]) || runes[i] == '.'); i++ {
	}

	// A dot never follows a number in the grammar, so taking every dot here turns 1.2.3 into one
	// bad literal rather than a number and a stray path.
	text := string(runes[start:i])
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return token{}, 0, fmt.Errorf("invalid number '%s'", text)
	}

	return token{kind: kindNumber, text: text, col: start + 1}, i, nil
}

func lexOperator(runes []rune, i int) (token, int, error) {
	if i+1 < len(runes) {
		if found, ok := twoRuneKind(string(runes[i : i+2])); ok {
			return token{kind: found, col: i + 1}, i + 2, nil
		}
	}

	var found kind
	switch runes[i] {
	case '<':
		found = kindLt
	case '>':
		found = kindGt
	case '.':
		found = kindDot
	case ',':
		found = kindComma
	case '[':
		found = kindLBracket
	case ']':
		found = kindRBracket
	case '(':
		found = kindLParen
	case ')':
		found = kindRParen
	case '=':
		return token{}, 0, errors.New("equality is '==', got '='")
	default:
		return token{}, 0, fmt.Errorf("unexpected character '%c'", runes[i])
	}

	return token{kind: found, col: i + 1}, i + 1, nil
}

func twoRuneKind(text string) (kind, bool) {
	switch text {
	case "==":
		return kindEq, true
	case "!=":
		return kindNe, true
	case "<=":
		return kindLe, true
	case ">=":
		return kindGe, true
	default:
		return kindIdent, false
	}
}

type token struct {
	kind kind
	text string
	col  int
}

type kind int

const (
	kindIdent kind = iota
	kindNumber
	kindString
	kindDot
	kindComma
	kindLBracket
	kindRBracket
	kindLParen
	kindRParen
	kindEq
	kindNe
	kindLt
	kindLe
	kindGt
	kindGe
	kindAnd
	kindOr
	kindNot
	kindIn
	kindTrue
	kindFalse
	kindEOF
)

func (k kind) String() string {
	switch k {
	case kindIdent:
		return "ident"
	case kindNumber:
		return "number"
	case kindString:
		return "string"
	case kindDot:
		return "dot"
	case kindComma:
		return "comma"
	case kindLBracket:
		return "lbracket"
	case kindRBracket:
		return "rbracket"
	case kindLParen:
		return "lparen"
	case kindRParen:
		return "rparen"
	case kindEq:
		return "eq"
	case kindNe:
		return "ne"
	case kindLt:
		return "lt"
	case kindLe:
		return "le"
	case kindGt:
		return "gt"
	case kindGe:
		return "ge"
	case kindAnd:
		return "and"
	case kindOr:
		return "or"
	case kindNot:
		return "not"
	case kindIn:
		return "in"
	case kindTrue:
		return "true"
	case kindFalse:
		return "false"
	default:
		return "eof"
	}
}
