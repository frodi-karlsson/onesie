package assert

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Check validates an expression against the plan it will run over, so every path and every
// comparison is known good before any network call. An expression that passes cannot fail to
// evaluate on a successful record.
func Check(expr *Expr, built *plan.Plan) error {
	if expr == nil {
		return nil
	}

	// The walk visits nodes in source order, so the error it keeps is the leftmost. Keeping the
	// lowest column would misorder a combined expression, whose columns are relative to each part's
	// own source.
	c := &checker{built: built}
	c.walk(expr.root)

	return c.err
}

type checker struct {
	built *plan.Plan
	err   error
}

func (c *checker) walk(n node) {
	if c.err != nil {
		return
	}

	switch typed := n.(type) {
	case *binaryNode:
		c.walk(typed.left)
		c.walk(typed.right)
	case *notNode:
		c.walk(typed.operand)
	case *comparisonNode:
		c.comparison(typed)
	case *inNode:
		c.membership(typed)
	}
}

func (c *checker) comparison(n *comparisonNode) {
	left, ok := c.typeOf(n.left)
	if !ok {
		return
	}

	right, ok := c.typeOf(n.right)
	if !ok {
		return
	}

	if left != right {
		c.fail("cannot compare %s, with %s", describe(n.left, left), describe(n.right, right))

		return
	}

	if ordered(n.op) && left != typeNumber {
		c.fail("cannot order %s. '%s' takes two numbers", describe(n.left, left), opSymbol(n.op))
	}
}

func (c *checker) membership(n *inNode) {
	want, ok := c.typeOf(n.operand)
	if !ok {
		return
	}

	for _, member := range n.list {
		got, memberOK := c.typeOf(member)
		if !memberOK {
			return
		}

		if got != want {
			c.fail("a list holds one type. Cannot compare %s, with %s",
				describe(n.operand, want), describe(member, got))

			return
		}
	}
}

func (c *checker) typeOf(n node) (valueType, bool) {
	switch typed := n.(type) {
	case *pathNode:
		return c.pathType(typed)
	case *stringNode:
		return typeString, true
	case *boolNode:
		return typeBoolean, true
	}

	// §17.2 puts only a path, a string, a boolean or a number in an operand position, and the
	// first three are above.
	return typeNumber, true
}

func (c *checker) pathType(n *pathNode) (valueType, bool) {
	head := n.segments[0]
	if head == "model" {
		if len(n.segments) > 1 {
			c.fail("'model' has no field '%s'", n.segments[1])

			return typeString, false
		}

		return typeString, true
	}

	q := c.question(head)
	if q == nil {
		return typeString, false
	}

	if len(n.segments) == 1 {
		c.fail("'%s' needs a field. '%s' has: %s", head, head, strings.Join(fieldsOf(*q), ", "))

		return typeString, false
	}

	got, used, ok := c.fieldType(*q, n)
	if !ok {
		return got, false
	}

	if len(n.segments) > used {
		c.fail("'%s' has no field '%s'", prefix(n, used), n.segments[used])

		return got, false
	}

	return got, true
}

func (c *checker) question(id string) *plan.Question {
	if at := slices.IndexFunc(c.built.Questions, func(q plan.Question) bool {
		return q.ID == id
	}); at >= 0 {
		return &c.built.Questions[at]
	}

	ids := make([]string, 0, len(c.built.Questions))
	for _, q := range c.built.Questions {
		ids = append(ids, q.ID)
	}

	// Naming a plan with no question at all would print the separator and nothing after it, which
	// is the reason tooFew in plan splits the same message in two.
	if len(ids) == 0 {
		c.fail("unknown question '%s'", id)

		return nil
	}

	c.fail("unknown question '%s'. Questions: %s", id, strings.Join(ids, ", "))

	return nil
}

func (c *checker) fieldType(q plan.Question, n *pathNode) (valueType, int, bool) {
	name := n.segments[1]

	f, ok := lookup(name)
	if !ok {
		c.fail("'%s' has no field '%s'", q.ID, name)

		return typeString, 0, false
	}

	if !f.available(q) {
		return c.unavailable(q, f)
	}

	if f.takesKey {
		return c.probability(q, n, f)
	}

	return f.typeOf(q), 2, true
}

func (c *checker) unavailable(q plan.Question, f field) (valueType, int, bool) {
	switch f.reason {
	case reasonDecision:
		c.fail("'%s.decision' needs %s on '%s'", q.ID, decisionNeeds(q), q.ID)

		return typeString, 0, false
	case reasonFallback:
		c.fail("'%s.fallback' needs %s, %s or %s on '%s'", q.ID,
			plan.Spelling(q.Origin, "--threshold"),
			plan.Spelling(q.Origin, "--min-confidence"),
			plan.Spelling(q.Origin, "--fallback"), q.ID)

		return typeString, 0, false
	default:
		return c.absent(q, f.name)
	}
}

func (c *checker) absent(q plan.Question, name string) (valueType, int, bool) {
	c.fail("'%s' is a %s question and has no '%s'", q.ID, shapeWord(q.Shape), name)

	return typeNumber, 0, false
}

func (c *checker) probability(q plan.Question, n *pathNode, f field) (valueType, int, bool) {
	keys := keysOf(q)

	if len(n.segments) < 3 {
		c.fail("'%s.p' needs %s", q.ID, keys.want)

		return typeNumber, 0, false
	}

	if key := n.segments[2]; !slices.Contains(keys.names, key) {
		c.fail("'%s' has no %s '%s'. %s", q.ID, keys.noun, key, keys.has)

		return typeNumber, 0, false
	}

	return f.typeOf(q), 3, true
}

func decisionNeeds(q plan.Question) string {
	// answer.Apply needs something to put in the decision's place, so a pick or rate question
	// carrying min confidence is short the fallback rather than either flag §17.8 names.
	if q.Shape != plan.Noul && q.Policy.MinConfidence != nil {
		return plan.Spelling(q.Origin, "--fallback")
	}

	return plan.Spelling(q.Origin, "--threshold") + " or " +
		plan.Spelling(q.Origin, "--min-confidence")
}

func (c *checker) fail(format string, args ...any) {
	c.err = fmt.Errorf(format, args...)
}

func keysOf(q plan.Question) keyset {
	if q.Shape == plan.Pick {
		names := make([]string, 0, len(q.Options))
		for _, option := range q.Options {
			names = append(names, option.Name)
		}

		return keyset{
			names: names,
			noun:  "option",
			want:  "an option name",
			has:   plan.ShapeName(&q, "--pick") + " has: " + strings.Join(names, ", "),
		}
	}

	if q.Labelled {
		labels := make([]string, 0, len(q.Levels))
		for _, level := range q.Levels {
			labels = append(labels, level.Label)
		}

		return keyset{
			names: labels,
			noun:  "label",
			want:  "a level label",
			has:   plan.ShapeName(&q, "--rate") + " has: " + strings.Join(labels, ", "),
		}
	}

	// A body's levels carry no labels, so the record keys them by index and only the bracket form
	// reaches them, as §17.3 says.
	indexes := make([]string, 0, len(q.Levels))
	for i := range q.Levels {
		indexes = append(indexes, strconv.Itoa(i))
	}

	return keyset{
		names: indexes,
		noun:  "level",
		want:  "a level index",
		has:   fmt.Sprintf("Its levels are numbered 0 to %d", len(q.Levels)-1),
	}
}

type keyset struct {
	names []string
	noun  string
	want  string
	has   string
}

func shapeWord(shape plan.Shape) string {
	switch shape {
	case plan.Pick:
		return "pick"
	case plan.Rate:
		return "rate"
	default:
		return "yes/no"
	}
}

func prefix(n *pathNode, used int) string {
	return (&pathNode{segments: n.segments[:used]}).render()
}

func describe(n node, t valueType) string {
	if _, ok := n.(*pathNode); ok {
		return fmt.Sprintf("'%s', a %s", n.render(), t)
	}

	return fmt.Sprintf("%s, a %s", n.render(), t)
}

func ordered(op kind) bool {
	switch op {
	case kindLt, kindLe, kindGt, kindGe:
		return true
	default:
		return false
	}
}

type valueType int

const (
	typeNumber valueType = iota
	typeString
	typeBoolean
)

func (t valueType) String() string {
	switch t {
	case typeString:
		return "string"
	case typeBoolean:
		return "boolean"
	default:
		return "number"
	}
}
