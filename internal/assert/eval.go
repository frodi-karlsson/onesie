package assert

import (
	"slices"

	"github.com/frodi-karlsson/jev-cli/internal/answer"
	"github.com/frodi-karlsson/jev-cli/internal/output"
)

// Eval reports whether the expression holds for a record. It returns no error, because Check has
// already proved every path and every comparison against the plan, which is the promise §17.3
// makes. A nil expression is no assertion at all, so it holds.
func Eval(expr *Expr, record output.Record) bool {
	if expr == nil {
		return true
	}

	return (&evaluator{record: record}).boolean(expr.root)
}

type evaluator struct {
	record output.Record
}

func (e *evaluator) boolean(n node) bool {
	switch typed := n.(type) {
	case *binaryNode:
		return e.binary(typed)
	case *notNode:
		return !e.boolean(typed.operand)
	case *comparisonNode:
		return e.comparison(typed)
	case *inNode:
		return e.membership(typed)
	}

	// parseComparison ends every leaf in a comparison or an in, so nothing else reaches a boolean
	// position and there is no answer to give for one.
	return false
}

func (e *evaluator) binary(n *binaryNode) bool {
	if n.op == kindAnd {
		return e.boolean(n.left) && e.boolean(n.right)
	}

	return e.boolean(n.left) || e.boolean(n.right)
}

func (e *evaluator) comparison(n *comparisonNode) bool {
	left := e.value(n.left)
	right := e.value(n.right)

	if ordered(n.op) {
		return compare(n.op, numberOf(left), numberOf(right))
	}

	// Every resolved value is a float64, a string, a bool or nil, so comparing the two interfaces
	// cannot panic on an uncomparable type.
	return (left == right) == (n.op == kindEq)
}

func (e *evaluator) membership(n *inNode) bool {
	operand := e.value(n.operand)

	return slices.ContainsFunc(n.list, func(member node) bool {
		return e.value(member) == operand
	})
}

func (e *evaluator) value(n node) any {
	switch typed := n.(type) {
	case *pathNode:
		return e.resolve(typed)
	case *numberNode:
		return typed.value
	case *stringNode:
		return typed.value
	case *boolNode:
		return typed.value
	}

	// §17.2 puts only a path or a literal in an operand position, and all four are above.
	return nil
}

func (e *evaluator) resolve(n *pathNode) any {
	head := segment(n, 0)
	if head == "model" {
		return e.record.Model
	}

	a := e.answer(head)
	if a == nil {
		return nil
	}

	switch segment(n, 1) {
	case "value":
		return a.Value
	case "confidence":
		return unwrap(a.Confidence)
	case "score":
		return unwrap(a.Score)
	case "norm":
		return unwrap(a.Norm)
	case "p":
		return probability(a, segment(n, 2))
	case "decision":
		// Apply sets Decision only alongside Decided, so an undecided answer already reads as nil
		// and Decided adds nothing here.
		return a.Decision
	case "fallback":
		// §17.3 reads an absent fallback as the empty string, which is what the field already
		// holds when no policy replaced the answer.
		return a.Fallback
	default:
		return nil
	}
}

func (e *evaluator) answer(id string) *answer.Answer {
	at := slices.IndexFunc(e.record.Answers, func(named output.Named) bool {
		return named.ID == id
	})
	if at < 0 {
		return nil
	}

	return e.record.Answers[at].Answer
}

func segment(n *pathNode, at int) string {
	if at >= len(n.segments) {
		return ""
	}

	return n.segments[at]
}

func probability(a *answer.Answer, key string) any {
	if a.P == nil {
		return nil
	}

	value, ok := a.P.Values[key]
	if !ok {
		return nil
	}

	return value
}

func unwrap(value *float64) any {
	if value == nil {
		return nil
	}

	return *value
}

func compare(op kind, left, right float64) bool {
	switch op {
	case kindLt:
		return left < right
	case kindLe:
		return left <= right
	case kindGt:
		return left > right
	default:
		return left >= right
	}
}

func numberOf(value any) float64 {
	number, ok := value.(float64)
	if !ok {
		return 0
	}

	return number
}
