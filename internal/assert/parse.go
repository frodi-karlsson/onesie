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
	if p.peek().kind == kindEOF {
		return nil, parseError(p.peek().col, "the expression is empty")
	}

	root, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if rest := p.peek(); rest.kind != kindEOF {
		return nil, parseError(rest.col, "expected the end of the expression")
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
		root = &binaryNode{op: kindAnd, left: root, right: expr.root, column: expr.root.pos()}
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
	tokens []token
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

	for p.peek().kind == kindOr {
		column := p.next().col

		var right node
		right, err = p.parseAnd()
		if err != nil {
			return nil, err
		}

		left = &binaryNode{op: kindOr, left: left, right: right, column: column}
	}

	return left, nil
}

func (p *parser) parseAnd() (node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}

	for p.peek().kind == kindAnd {
		column := p.next().col

		var right node
		right, err = p.parseUnary()
		if err != nil {
			return nil, err
		}

		left = &binaryNode{op: kindAnd, left: left, right: right, column: column}
	}

	return left, nil
}

func (p *parser) parseUnary() (node, error) {
	switch tok := p.peek(); tok.kind {
	case kindNot:
		p.next()

		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}

		return &notNode{operand: operand, column: tok.col}, nil
	case kindLParen:
		p.next()

		inner, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if p.peek().kind != kindRParen {
			return nil, parseError(p.peek().col, "expected a closing parenthesis")
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
	if op.kind == kindIn {
		return p.parseIn(left, op.col)
	}
	if !comparisonOp(op.kind) {
		return nil, parseError(op.col, "expected a comparison")
	}
	p.next()

	var right node
	right, err = p.parseOperand()
	if err != nil {
		return nil, err
	}

	return &comparisonNode{op: op.kind, left: left, right: right, column: op.col}, nil
}

func (p *parser) parseIn(operand node, column int) (node, error) {
	p.next()

	if p.peek().kind != kindLBracket {
		return nil, parseError(p.peek().col, "expected a list after in")
	}
	p.next()

	if p.peek().kind == kindRBracket {
		return nil, parseError(p.peek().col, "a list needs at least one value")
	}

	var list []node
	for {
		member, err := p.parseOperand()
		if err != nil {
			return nil, err
		}

		list = append(list, member)
		if p.peek().kind != kindComma {
			break
		}
		p.next()
	}

	if p.peek().kind != kindRBracket {
		return nil, p.unclosedBracket()
	}
	p.next()

	return &inNode{operand: operand, list: list, column: column}, nil
}

func (p *parser) parseOperand() (node, error) {
	switch tok := p.peek(); tok.kind {
	case kindIdent, kindLBracket:
		return p.parsePath()
	case kindNumber:
		p.next()

		value, err := strconv.ParseFloat(tok.text, 64)
		if err != nil {
			return nil, parseError(tok.col, "expected a value")
		}

		return &numberNode{value: value, column: tok.col}, nil
	case kindString:
		p.next()

		return &stringNode{value: tok.text, column: tok.col}, nil
	case kindTrue, kindFalse:
		p.next()

		return &boolNode{value: tok.kind == kindTrue, column: tok.col}, nil
	default:
		return nil, parseError(tok.col, "expected a value")
	}
}

func (p *parser) parsePath() (node, error) {
	column := p.peek().col

	head, err := p.parseHead()
	if err != nil {
		return nil, err
	}

	segments := []string{head}
	for {
		switch p.peek().kind {
		case kindDot:
			p.next()

			if p.peek().kind != kindIdent {
				return nil, parseError(p.peek().col, "expected a field name after the dot")
			}

			segments = append(segments, p.next().text)
		case kindLBracket:
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
	if p.peek().kind == kindIdent {
		return p.next().text, nil
	}

	return p.parseBracketName()
}

func (p *parser) parseBracketName() (string, error) {
	p.next()

	if p.peek().kind != kindString {
		return "", parseError(p.peek().col, "expected a quoted name in the brackets")
	}

	name := p.next().text
	if p.peek().kind != kindRBracket {
		return "", p.unclosedBracket()
	}
	p.next()

	return name, nil
}

func (p *parser) peek() token {
	return p.tokens[p.at]
}

func (p *parser) next() token {
	tok := p.tokens[p.at]
	if p.at < len(p.tokens)-1 {
		p.at++
	}

	return tok
}

func (p *parser) unclosedBracket() error {
	// §17.8 publishes this message with a column one past the input, because the bracket was never
	// closed anywhere, not at the token that happened to follow it.
	return parseError(p.tokens[len(p.tokens)-1].col, "expected a closing bracket before the end")
}

func comparisonOp(k kind) bool {
	switch k {
	case kindEq, kindNe, kindLt, kindLe, kindGt, kindGe:
		return true
	default:
		return false
	}
}

func parseError(column int, want string) error {
	return fmt.Errorf("parse error at column %d, %s", column, want)
}
