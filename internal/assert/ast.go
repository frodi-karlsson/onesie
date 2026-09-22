package assert

import (
	"strconv"
	"strings"
	"unicode"
)

type node interface {
	pos() int
	render() string
}

type binaryNode struct {
	op          Kind
	left, right node
	column      int
}

func (n *binaryNode) pos() int {
	return n.column
}

func (n *binaryNode) render() string {
	return sexpr(opSymbol(n.op), n.left.render(), n.right.render())
}

type notNode struct {
	operand node
	column  int
}

func (n *notNode) pos() int {
	return n.column
}

func (n *notNode) render() string {
	return sexpr("not", n.operand.render())
}

type comparisonNode struct {
	op          Kind
	left, right node
	column      int
}

func (n *comparisonNode) pos() int {
	return n.column
}

func (n *comparisonNode) render() string {
	return sexpr(opSymbol(n.op), n.left.render(), n.right.render())
}

type inNode struct {
	operand node
	list    []node
	column  int
}

func (n *inNode) pos() int {
	return n.column
}

func (n *inNode) render() string {
	members := make([]string, 0, len(n.list))
	for _, member := range n.list {
		members = append(members, member.render())
	}

	return sexpr("in", n.operand.render(), "["+strings.Join(members, " ")+"]")
}

type pathNode struct {
	segments []string
	column   int
}

func (n *pathNode) pos() int {
	return n.column
}

func (n *pathNode) render() string {
	var out strings.Builder
	for i, segment := range n.segments {
		switch {
		case !bareSegment(segment):
			out.WriteString("[" + strconv.Quote(segment) + "]")
		case i > 0:
			out.WriteString("." + segment)
		default:
			out.WriteString(segment)
		}
	}

	return out.String()
}

type numberNode struct {
	value  float64
	column int
}

func (n *numberNode) pos() int {
	return n.column
}

func (n *numberNode) render() string {
	return strconv.FormatFloat(n.value, 'g', -1, 64)
}

type stringNode struct {
	value  string
	column int
}

func (n *stringNode) pos() int {
	return n.column
}

func (n *stringNode) render() string {
	return strconv.Quote(n.value)
}

type boolNode struct {
	value  bool
	column int
}

func (n *boolNode) pos() int {
	return n.column
}

func (n *boolNode) render() string {
	return strconv.FormatBool(n.value)
}

func sexpr(parts ...string) string {
	return "(" + strings.Join(parts, " ") + ")"
}

func opSymbol(kind Kind) string {
	switch kind {
	case KindEq:
		return "=="
	case KindNe:
		return "!="
	case KindLt:
		return "<"
	case KindLe:
		return "<="
	case KindGt:
		return ">"
	case KindGe:
		return ">="
	default:
		return kind.String()
	}
}

func bareSegment(segment string) bool {
	if segment == "" || keywordKind(segment) != KindIdent {
		return false
	}

	for i, r := range segment {
		if unicode.IsLetter(r) || r == '_' || (i > 0 && unicode.IsDigit(r)) {
			continue
		}

		return false
	}

	return true
}
