// Package assert evaluates a boolean expression over a normalized record. It performs no IO and
// knows nothing about the command line.
package assert

import (
	"errors"
	"fmt"
	"strconv"
	"unicode"
)

func lex(expr string) ([]Token, error) {
	runes := []rune(expr)
	tokens := []Token{}

	for i := 0; i < len(runes); {
		var (
			tok  Token
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

	return append(tokens, Token{Kind: KindEOF, Col: len(runes) + 1}), nil
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

func lexString(runes []rune, i int) (Token, int, error) {
	start := i
	for i++; i < len(runes) && runes[i] != '"'; i++ {
		if runes[i] == '\\' {
			i++
		}
	}
	if i >= len(runes) {
		return Token{}, 0, errors.New("unterminated string")
	}

	raw := string(runes[start : i+1])
	text, err := strconv.Unquote(raw)
	if err != nil {
		return Token{}, 0, fmt.Errorf("invalid string %s", raw)
	}

	return Token{Kind: KindString, Text: text, Col: start + 1}, i + 1, nil
}

func startsIdent(runes []rune, i int) bool {
	return unicode.IsLetter(runes[i]) || runes[i] == '_'
}

func lexIdent(runes []rune, i int) (Token, int) {
	start := i
	for ; i < len(runes) && (startsIdent(runes, i) || unicode.IsDigit(runes[i])); i++ {
	}

	text := string(runes[start:i])

	return Token{Kind: keywordKind(text), Text: text, Col: start + 1}, i
}

func keywordKind(text string) Kind {
	switch text {
	case "and":
		return KindAnd
	case "or":
		return KindOr
	case "not":
		return KindNot
	case "in":
		return KindIn
	case "true":
		return KindTrue
	case "false":
		return KindFalse
	default:
		return KindIdent
	}
}

func startsNumber(runes []rune, i int) bool {
	if unicode.IsDigit(runes[i]) {
		return true
	}

	// There is no arithmetic, so a leading minus can only open a negative literal.
	return runes[i] == '-' && i+1 < len(runes) && unicode.IsDigit(runes[i+1])
}

func lexNumber(runes []rune, i int) (Token, int, error) {
	start := i
	for i++; i < len(runes) && (unicode.IsDigit(runes[i]) || runes[i] == '.'); i++ {
	}

	// A dot never follows a number in the grammar, so taking every dot here turns 1.2.3 into one
	// bad literal rather than a number and a stray path.
	text := string(runes[start:i])
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return Token{}, 0, fmt.Errorf("invalid number '%s'", text)
	}

	return Token{Kind: KindNumber, Text: text, Col: start + 1}, i, nil
}

func lexOperator(runes []rune, i int) (Token, int, error) {
	if i+1 < len(runes) {
		if kind, found := twoRuneKind(string(runes[i : i+2])); found {
			return Token{Kind: kind, Col: i + 1}, i + 2, nil
		}
	}

	var kind Kind
	switch runes[i] {
	case '<':
		kind = KindLt
	case '>':
		kind = KindGt
	case '.':
		kind = KindDot
	case ',':
		kind = KindComma
	case '[':
		kind = KindLBracket
	case ']':
		kind = KindRBracket
	case '(':
		kind = KindLParen
	case ')':
		kind = KindRParen
	case '=':
		return Token{}, 0, errors.New("equality is '==', got '='")
	default:
		return Token{}, 0, fmt.Errorf("unexpected character '%c'", runes[i])
	}

	return Token{Kind: kind, Col: i + 1}, i + 1, nil
}

func twoRuneKind(text string) (Kind, bool) {
	switch text {
	case "==":
		return KindEq, true
	case "!=":
		return KindNe, true
	case "<=":
		return KindLe, true
	case ">=":
		return KindGe, true
	default:
		return KindIdent, false
	}
}

// Token is one terminal of the §17.2 grammar. Col counts runes from 1 into the expression as it was
// typed, so a parse error can point at a character.
type Token struct {
	Kind Kind
	Text string
	Col  int
}

// Kind is what a Token is. Only an identifier, a number and a string carry Text.
type Kind int

const (
	// KindIdent is a question id or a field name.
	KindIdent Kind = iota
	// KindNumber is a numeric literal.
	KindNumber
	// KindString is a double quoted literal, with its escapes resolved.
	KindString
	// KindDot is the path separator.
	KindDot
	// KindComma is the list separator.
	KindComma
	// KindLBracket opens a bracket path or a list.
	KindLBracket
	// KindRBracket closes a bracket path or a list.
	KindRBracket
	// KindLParen opens a grouped expression.
	KindLParen
	// KindRParen closes a grouped expression.
	KindRParen
	// KindEq is the == operator.
	KindEq
	// KindNe is the != operator.
	KindNe
	// KindLt is the < operator.
	KindLt
	// KindLe is the <= operator.
	KindLe
	// KindGt is the > operator.
	KindGt
	// KindGe is the >= operator.
	KindGe
	// KindAnd is the and keyword.
	KindAnd
	// KindOr is the or keyword.
	KindOr
	// KindNot is the not keyword.
	KindNot
	// KindIn is the in keyword.
	KindIn
	// KindTrue is the true keyword.
	KindTrue
	// KindFalse is the false keyword.
	KindFalse
	// KindEOF closes every token list and carries the column one past the expression.
	KindEOF
)

// String names the kind as a parse error spells it.
func (k Kind) String() string {
	switch k {
	case KindIdent:
		return "ident"
	case KindNumber:
		return "number"
	case KindString:
		return "string"
	case KindDot:
		return "dot"
	case KindComma:
		return "comma"
	case KindLBracket:
		return "lbracket"
	case KindRBracket:
		return "rbracket"
	case KindLParen:
		return "lparen"
	case KindRParen:
		return "rparen"
	case KindEq:
		return "eq"
	case KindNe:
		return "ne"
	case KindLt:
		return "lt"
	case KindLe:
		return "le"
	case KindGt:
		return "gt"
	case KindGe:
		return "ge"
	case KindAnd:
		return "and"
	case KindOr:
		return "or"
	case KindNot:
		return "not"
	case KindIn:
		return "in"
	case KindTrue:
		return "true"
	case KindFalse:
		return "false"
	default:
		return "eof"
	}
}
