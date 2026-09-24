package assert

import (
	"slices"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/output"
)

// Eval reports whether the expression holds for a record, and a nil expression always holds. It
// returns no error, since Check has already proved the expression against the plan, per §17.3.
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
		leftNumber, leftKnown := left.(float64)
		rightNumber, rightKnown := right.(float64)

		// A missing answer has no order, so the comparison is false and a gate over it fails safe
		// rather than reading the gap as a zero.
		return leftKnown && rightKnown && compare(n.op, leftNumber, rightNumber)
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
	case *callNode:
		return e.call(typed)
	}

	// §17.2 puts only a path, a call or a literal in an operand position, and all five are above.
	return nil
}

func (e *evaluator) call(n *callNode) any {
	apply, ok := fold(n.name)
	if !ok || len(n.args) == 0 {
		return nil
	}

	numbers := make([]float64, 0, len(n.args))
	for _, arg := range n.args {
		number, isNumber := e.value(arg).(float64)
		if !isNumber {
			// A missing answer leaves the whole call missing rather than folding in as a zero, so
			// every ordered comparison over the call is false.
			return nil
		}

		numbers = append(numbers, number)
	}

	return apply(numbers)
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

	f, ok := lookup(segment(n, 1))
	if !ok {
		// Check rejects an unknown field before Eval runs, so this is unreachable by design and
		// exists only so the lookup is total.
		return nil
	}

	return f.read(a, segment(n, 2))
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
