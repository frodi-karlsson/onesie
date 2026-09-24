// Package calibrate reads the labels a calibration run compares answers with, and scores and
// reports how well the answers agree with them.
package calibrate

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// SplitLabel splits a --label value into the question id it names and its jq source. A bare
// expression belongs to the only question asked.
func SplitLabel(spec string, ids []string) (id, source string, err error) {
	if id, source, found := longestID(spec, ids); found {
		return id, source, nil
	}

	prefix, rest, found := strings.Cut(spec, "=")
	named := found && !strings.HasPrefix(rest, "=")

	if named && identifier.MatchString(prefix) {
		return "", "", fmt.Errorf("--label names an unknown question '%s'. Questions: %s",
			prefix, strings.Join(ids, ", "))
	}

	if len(ids) != 1 {
		return "", "", fmt.Errorf("--label needs ID=EXPR when more than one question is asked. Questions: %s",
			strings.Join(ids, ", "))
	}

	return ids[0], spec, nil
}

func longestID(spec string, ids []string) (id, source string, found bool) {
	for _, candidate := range ids {
		rest, named := strings.CutPrefix(spec, candidate+"=")
		if !named || strings.HasPrefix(rest, "=") || len(candidate) <= len(id) && found {
			continue
		}

		id, source, found = candidate, rest, true
	}

	return id, source, found
}

// ParseLabel reads one label result for a question of the given shape, whose option or level names
// are names. The second result is false when the value leaves the record unlabelled.
func ParseLabel(shape plan.Shape, names []string, value any) (Label, bool, error) {
	if value == nil || value == "" {
		return Label{}, false, nil
	}

	if shape == plan.Noul {
		yes, ok := yesNo(value)
		if !ok {
			return Label{}, false, fmt.Errorf("%s is not a yes/no label. Use true, false, yes, no, 1 or 0",
				shown(value))
		}

		return Label{Yes: yes}, true, nil
	}

	text, ok := nameText(value)
	if !ok || !slices.Contains(names, text) {
		return Label{}, false, fmt.Errorf("%s is not a %s label. Use %s", shown(value), shape, oneOf(names))
	}

	return Label{Name: text}, true, nil
}

// Label is one record's right answer for one question. Yes holds a yes/no label and Name holds a
// pick or rate label.
type Label struct {
	Yes  bool
	Name string
}

func yesNo(value any) (bool, bool) {
	if flag, ok := value.(bool); ok {
		return flag, true
	}

	text, ok := value.(string)
	if !ok {
		text, ok = numberText(value)
	}

	if !ok {
		return false, false
	}

	switch strings.ToLower(text) {
	case "true", "yes", "1":
		return true, true
	case "false", "no", "0":
		return false, true
	default:
		return false, false
	}
}

func nameText(value any) (string, bool) {
	if text, ok := value.(string); ok {
		return text, true
	}

	return numberText(value)
}

func numberText(value any) (string, bool) {
	switch value.(type) {
	case int, float64, json.Number, *big.Int:
	default:
		return "", false
	}

	id, err := jq.ID(value)
	if err != nil {
		return "", false
	}

	number, ok := id.(json.Number)

	return number.String(), ok
}

func shown(value any) string {
	if text, ok := value.(string); ok {
		return strconv.Quote(text)
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}

	return string(encoded)
}

func oneOf(names []string) string {
	switch len(names) {
	case 0:
		return "a declared name"
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
	}
}
