package assert

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse reads one §17.2 expression. Its errors carry the column they were found at and no jev
// prefix, which the caller owns.
func Parse(source string) (*Expr, error) {
	tokens, err := lex(source)
	if err != nil {
		return nil, err
	}

	p := &parser{tokens: tokens}
	if p.peek().Kind == KindEOF {
		return nil, parseError(p.peek().Col, "the expression is empty")
	}

	root, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if rest := p.peek(); rest.Kind != KindEOF {
		return nil, parseError(rest.Col, "expected the end of the expression")
	}

	return &Expr{root: root, source: source}, nil
}

// Combine folds several expressions into one, joined by and, in the order given. It is what makes
// several --assert flags a single gate. Combining none returns nil, which a caller reads as no
// assertion to evaluate.
func Combine(exprs ...*Expr) *Expr {
	switch len(exprs) {
	case 0:
		return nil
	case 1:
		return exprs[0]
	}

	root := exprs[0].root
	sources := []string{"(" + exprs[0].source + ")"}

	for _, expr := range exprs[1:] {
		root = &binaryNode{op: KindAnd, left: root, right: expr.root, column: expr.root.pos()}
		sources = append(sources, "("+expr.source+")")
	}

	return &Expr{root: root, source: strings.Join(sources, " and ")}
}

// Expr is a parsed, not yet checked assertion. Its tree stays unexported, so every consumer goes
// through this package, and it keeps its source so an error can quote what the user typed.
type Expr struct {
	root   node
	source string
}

type parser struct {
	tokens []Token
	at     int
}

func (p *parser) parseExpr() (node, error) {
	return p.parseOr()
}

func (p *parser) parseOr() (node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}

	for p.peek().Kind == KindOr {
		column := p.next().Col

		var right node
		right, err = p.parseAnd()
		if err != nil {
			return nil, err
		}

		left = &binaryNode{op: KindOr, left: left, right: right, column: column}
	}

	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for p.peek().Kind == KindAnd {
		column := p.next().Col

		var right node
		right, err = p.parseUnary()
		if err != nil {
			return nil, err
		}

		left = &binaryNode{op: KindAnd, left: left, right: right, column: column}
	}

	return left, nil
}

func (p *parser) parseUnary() (node, error) {
	switch tok := p.peek(); tok.Kind {
	case KindNot:
		p.next()

		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}

		return &notNode{operand: operand, column: tok.Col}, nil
	case KindLParen:
		p.next()

		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().Kind != KindRParen {
			return nil, parseError(p.peek().Col, "expected a closing parenthesis")
		}
		p.next()

		return inner, nil
	default:
		return p.parseComparison()
	}
}

func (p *parser) parseComparison() (node, error) {
	left, err := p.parseOperand()
	if err != nil {
		return nil, err
	}

	op := p.peek()
	if op.Kind == KindIn {
		return p.parseIn(left, op.Col)
	}
	if !comparisonOp(op.Kind) {
		return nil, parseError(op.Col, "expected a comparison")
	}
	p.next()

	var right node
	right, err = p.parseOperand()
	if err != nil {
		return nil, err
	}

	return &comparisonNode{op: op.Kind, left: left, right: right, column: op.Col}, nil
}

func (p *parser) parseIn(operand node, column int) (node, error) {
	p.next()

	if p.peek().Kind != KindLBracket {
		return nil, parseError(p.peek().Col, "expected a list after in")
	}
	p.next()

	if p.peek().Kind == KindRBracket {
		return nil, parseError(p.peek().Col, "a list needs at least one value")
	}

	var list []node
	for {
		member, err := p.parseOperand()
		if err != nil {
			return nil, err
		}

		list = append(list, member)
		if p.peek().Kind != KindComma {
			break
		}
		p.next()
	}

	if p.peek().Kind != KindRBracket {
		return nil, p.unclosedBracket()
	}
	p.next()

	return &inNode{operand: operand, list: list, column: column}, nil
}

func (p *parser) parseOperand() (node, error) {
	switch tok := p.peek(); tok.Kind {
	case KindIdent, KindLBracket:
		return p.parsePath()
	case KindNumber:
		p.next()

		value, err := strconv.ParseFloat(tok.Text, 64)
		if err != nil {
			return nil, parseError(tok.Col, "expected a value")
		}

		return &numberNode{value: value, column: tok.Col}, nil
	case KindString:
		p.next()

		return &stringNode{value: tok.Text, column: tok.Col}, nil
	case KindTrue, KindFalse:
		p.next()

		return &boolNode{value: tok.Kind == KindTrue, column: tok.Col}, nil
	default:
		return nil, parseError(tok.Col, "expected a value")
	}
}

func (p *parser) parsePath() (node, error) {
	column := p.peek().Col

	head, err := p.parseHead()
	if err != nil {
		return nil, err
	}

	segments := []string{head}
	for {
		switch p.peek().Kind {
		case KindDot:
			p.next()

			if p.peek().Kind != KindIdent {
				return nil, parseError(p.peek().Col, "expected a field name after the dot")
			}

			segments = append(segments, p.next().Text)
		case KindLBracket:
			var segment string

			segment, err = p.parseBracketName()
			if err != nil {
				return nil, err
			}

			segments = append(segments, segment)
		default:
			return &pathNode{segments: segments, column: column}, nil
		}
	}
}

func (p *parser) parseHead() (string, error) {
	if p.peek().Kind == KindIdent {
		return p.next().Text, nil
	}

	return p.parseBracketName()
}

func (p *parser) parseBracketName() (string, error) {
	p.next()

	if p.peek().Kind != KindString {
		return "", parseError(p.peek().Col, "expected a quoted name in the brackets")
	}

	name := p.next().Text
	if p.peek().Kind != KindRBracket {
		return "", p.unclosedBracket()
	}
	p.next()

	return name, nil
}

func (p *parser) peek() Token {
	return p.tokens[p.at]
}

func (p *parser) next() Token {
	tok := p.tokens[p.at]
	if p.at < len(p.tokens)-1 {
		p.at++
	}

	return tok
}

func (p *parser) unclosedBracket() error {
	// §17.8 publishes this message with a column one past the input, because the bracket was never
	// closed anywhere, not at the token that happened to follow it.
	return parseError(p.tokens[len(p.tokens)-1].Col, "expected a closing bracket before the end")
}

func comparisonOp(kind Kind) bool {
	switch kind {
	case KindEq, KindNe, KindLt, KindLe, KindGt, KindGe:
		return true
	default:
		return false
	}
}

func parseError(column int, want string) error {
	return fmt.Errorf("parse error at column %d, %s", column, want)
}
