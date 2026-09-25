package assert

import (
	"fmt"
	"slices"
	"strconv"
)

// Cuts reads, for each question a gate names, the cut it compares that question's value against.
// Only ID.value < X or ID.value >= X gives one, and a single other use of ID.value takes it away.
func Cuts(expr *Expr) map[string]Cut {
	w := &cutWalker{uses: map[string]*cutUse{}}
	if expr != nil {
		w.walk(expr.root)
	}

	cuts := make(map[string]Cut, len(w.uses))

	for id, use := range w.uses {
		switch {
		case use.why != "":
			cuts[id] = Cut{Why: use.why}
		case len(use.cuts) == 1:
			cuts[id] = Cut{Value: use.cuts[0], Readable: true}
		case len(use.cuts) > 1:
			cuts[id] = Cut{Why: fmt.Sprintf("the gate compares %s with both %s and %s",
				valuePath(id), number(use.cuts[0]), number(use.cuts[1]))}
		default:
			cuts[id] = Cut{Why: fmt.Sprintf("the gate never compares %s with < or >=", valuePath(id))}
		}
	}

	return cuts
}

// Cut is the cut a gate reads for one question. Why says, when it is not Readable, what in the gate
// hides it.
type Cut struct {
	Value    float64
	Readable bool
	Why      string
}

type cutWalker struct {
	uses map[string]*cutUse
}

type cutUse struct {
	cuts []float64
	why  string
}

func (w *cutWalker) walk(n node) {
	switch n := n.(type) {
	case *binaryNode:
		w.walk(n.left)
		w.walk(n.right)
	case *notNode:
		w.walk(n.operand)
	case *comparisonNode:
		w.comparison(n)
	case *inNode:
		w.use(n.operand, "the gate tests %s with in")

		for _, member := range n.list {
			w.use(member, "the gate tests %s with in")
		}
	default:
		w.use(n, "the gate reads %s as a whole")
	}
}

func (w *cutWalker) comparison(n *comparisonNode) {
	id, isValue := valueOf(n.left)
	if !isValue {
		w.use(n.left, "the gate compares %s with another value")
		w.use(n.right, "the gate puts %s on the right of a comparison")

		return
	}

	limit, isNumber := n.right.(*numberNode)

	switch {
	case isNumber && (n.op == kindLt || n.op == kindGe):
		use := w.useOf(id)
		if !slices.Contains(use.cuts, limit.value) {
			use.cuts = append(use.cuts, limit.value)
		}
	case isNumber:
		w.hide(id, fmt.Sprintf("the gate compares %s with %s, and only < and >= give a cut",
			valuePath(id), opSymbol(n.op)))
	default:
		w.hide(id, fmt.Sprintf("the gate compares %s with something other than a number", valuePath(id)))
		w.use(n.right, "the gate puts %s on the right of a comparison")
	}
}

func (w *cutWalker) use(n node, why string) {
	switch n := n.(type) {
	case *pathNode:
		if id, isValue := valueOf(n); isValue {
			w.hide(id, fmt.Sprintf(why, valuePath(id)))

			return
		}

		w.useOf(n.segments[0])
	case *callNode:
		for _, arg := range n.args {
			w.use(arg, "the gate reads %s through "+n.name+"()")
		}
	case *binaryNode, *notNode, *comparisonNode, *inNode:
		w.walk(n)
	}
}

func (w *cutWalker) hide(id, why string) {
	if use := w.useOf(id); use.why == "" {
		use.why = why
	}
}

func (w *cutWalker) useOf(id string) *cutUse {
	use, found := w.uses[id]
	if !found {
		use = &cutUse{}
		w.uses[id] = use
	}

	return use
}

func valueOf(n node) (string, bool) {
	path, isPath := n.(*pathNode)
	if !isPath || len(path.segments) != 2 || path.segments[1] != "value" {
		return "", false
	}

	return path.segments[0], true
}

func valuePath(id string) string {
	return (&pathNode{segments: []string{id, "value"}}).render()
}

func number(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}
