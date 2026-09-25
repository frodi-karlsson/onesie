package calibrate

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
)

const (
	percentScale  = 100
	lowerBound    = "lower("
	upperBound    = "upper("
	abstainCut    = "abstain"
	atWord        = "at"
	forbiddenInID = "()<>="
)

// ParseRequirement reads one --require text, [lower|upper](ID.MEASURE) OP NUMBER [at CUT]. Its
// errors read after the flag name and carry no onesie prefix.
func ParseRequirement(text string) (Requirement, error) {
	source := strings.TrimSpace(text)
	if source == "" {
		return Requirement{}, errors.New("is empty. Write ID.MEASURE OP NUMBER, such as urgent.catches >= 0.95")
	}

	req := Requirement{Source: source}

	target, rest, err := splitTarget(source, &req)
	if err != nil {
		return Requirement{}, err
	}

	if err := req.readTarget(target); err != nil {
		return Requirement{}, err
	}

	if err := req.readComparison(rest); err != nil {
		return Requirement{}, err
	}

	if err := req.checkCombination(); err != nil {
		return Requirement{}, err
	}

	return req, nil
}

// Requirement is one parsed --require. Threshold and At.Value lie in [0,1], and ID is the question
// it names, which the caller resolves against the plan.
type Requirement struct {
	Source, ID string
	Bound      Bound
	Measure    Measure
	Op         Op
	Threshold  float64
	At         At
}

// Bound says which end of a measure a requirement compares, the point value or an end of its 95
// percent interval.
type Bound int

// The bounds a requirement can compare.
const (
	Point Bound = iota
	Lower
	Upper
)

// At is the cut a yes/no requirement reads, named by a number or taken from the file's abstain_if.
// Value is set only for AtNumber.
type At struct {
	Kind  AtKind
	Value float64
}

// AtKind says where a requirement's cut comes from.
type AtKind int

// The places a cut can come from. AtNone leaves it to the gate.
const (
	AtNone AtKind = iota
	AtNumber
	AtAbstain
)

func splitTarget(source string, req *Requirement) (target, rest string, err error) {
	for prefix, bound := range map[string]Bound{lowerBound: Lower, upperBound: Upper} {
		inner, found := strings.CutPrefix(source, prefix)
		if !found {
			continue
		}

		req.Bound = bound

		target, rest, closed := strings.Cut(inner, ")")
		if !closed {
			return "", "", fmt.Errorf("has %s with no closing )", prefix)
		}

		return strings.TrimSpace(target), rest, nil
	}

	at := strings.IndexAny(source, "<>=")
	if at < 0 {
		return "", "", fmt.Errorf("needs an operator, >=, >, <= or <, in '%s'", source)
	}

	return strings.TrimSpace(source[:at]), source[at:], nil
}

func (r *Requirement) readTarget(target string) error {
	dot := strings.LastIndex(target, ".")
	if dot < 0 {
		return fmt.Errorf("needs ID.MEASURE, got '%s'", target)
	}

	r.ID = target[:dot]
	if r.ID == "" || strings.ContainsAny(r.ID, forbiddenInID) || strings.IndexFunc(r.ID, unicode.IsSpace) >= 0 {
		return fmt.Errorf("cannot name the question '%s', since an id here holds no space and none of ( ) < > =",
			r.ID)
	}

	measure, found := measureNamed(target[dot+1:])
	if !found {
		return fmt.Errorf("names the measure '%s', which is not one of %s", target[dot+1:], measureList())
	}

	r.Measure = measure

	return nil
}

func (r *Requirement) readComparison(rest string) error {
	rest = strings.TrimSpace(rest)

	op, after, found := cutOp(rest)
	if !found {
		shown, _, _ := strings.Cut(rest, " ")

		return fmt.Errorf("takes >=, >, <= or <, got '%s'", shown)
	}

	r.Op = op

	fields := strings.Fields(after)
	if len(fields) == 0 {
		return fmt.Errorf("needs a number after %s", op)
	}

	threshold, err := fraction(fields[0])
	if err != nil {
		return err
	}

	r.Threshold = threshold

	switch {
	case len(fields) == 1:
		return nil
	case fields[1] != atWord:
		return fmt.Errorf("has '%s' after the requirement", strings.Join(fields[1:], " "))
	case len(fields) == 2:
		return errors.New("has at with nothing after it. at needs a cut, a number or abstain")
	case len(fields) > 3:
		return fmt.Errorf("has '%s' after the requirement", strings.Join(fields[3:], " "))
	case fields[2] == abstainCut:
		r.At = At{Kind: AtAbstain}

		return nil
	}

	cut, err := fraction(fields[2])
	if err != nil {
		return err
	}

	r.At = At{Kind: AtNumber, Value: cut}

	return nil
}

func cutOp(text string) (Op, string, bool) {
	for _, op := range []Op{OpGE, OpLE, OpGT, OpLT} {
		after, found := strings.CutPrefix(text, op.String())
		if !found {
			continue
		}

		if strings.HasPrefix(after, "=") || strings.HasPrefix(after, "<") || strings.HasPrefix(after, ">") {
			return 0, "", false
		}

		return op, after, true
	}

	return 0, "", false
}

func fraction(text string) (float64, error) {
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(value) || value < 0 {
		return 0, fmt.Errorf("takes a number between 0 and 1, got '%s'", text)
	}

	if value >= 2 && value <= percentScale && value == math.Trunc(value) {
		return 0, fmt.Errorf("takes a fraction between 0 and 1, so write %s, not %s",
			strconv.FormatFloat(value/percentScale, 'f', -1, 64), text)
	}

	if value > 1 {
		return 0, fmt.Errorf("takes a number between 0 and 1, got %s", text)
	}

	// Abs turns a negative zero, which passes the range check, into the zero a report prints.
	return math.Abs(value), nil
}

func (r *Requirement) checkCombination() error {
	switch {
	case r.Measure == AreaUnderCurve && r.Bound != Point:
		return errors.New("reads auc, and auc has no interval, so lower and upper do not apply")
	case r.Measure == AreaUnderCurve && r.At.Kind != AtNone:
		return errors.New("reads auc, and auc covers every cut, so at does not apply")
	case !r.Measure.YesNo() && r.At.Kind != AtNone:
		return fmt.Errorf("reads %s, and at applies only to catches, false_alarms and right_when_flagged",
			r.Measure)
	case r.Bound == Lower && (r.Op == OpLE || r.Op == OpLT):
		return errors.New("uses lower with an upper limit. lower takes >= or >, since it reads the low end of the interval")
	case r.Bound == Upper && (r.Op == OpGE || r.Op == OpGT):
		return errors.New("uses upper with a lower limit. upper takes <= or <, since it reads the high end of the interval")
	}

	return nil
}

// Check measures the requirement against one question's score, reading a yes/no measure at cut.
// A measure over no records, or at a cut the score has no row for, does not hold.
func (r Requirement) Check(q QuestionReport, cut float64) Result {
	result := Result{Requirement: r}
	if r.Measure.NeedsCut() {
		result.HasCut, result.Cut = true, cut
	}

	if r.Measure == AreaUnderCurve {
		switch {
		case q.YesNo == nil:
			result.Reason = r.notApplicable(q)
		case !q.YesNo.HasAUC:
			result.Reason = "AUC needs both yes and no labels"
		default:
			result.Value, result.HasValue = q.YesNo.AUC, true
		}
	} else {
		share, reason := r.shareOf(q, cut)
		switch {
		case reason != "":
			result.Reason = reason
		default:
			result.Value, result.Low, result.High = share.Rate, share.Low, share.High
			result.Hits, result.Of = share.Hits, share.Of
			result.HasValue, result.HasInterval = true, true
		}
	}

	result.Held = result.HasValue && r.Op.holds(result.compared(), r.Threshold)

	return result
}

// Result is a requirement measured. Value is the point value, Low and High its interval, and
// Reason says why a result without a value did not hold.
type Result struct {
	Requirement

	Held                          bool
	Value, Low, High              float64
	Hits, Of                      int
	HasValue, HasInterval, HasCut bool
	Cut                           float64
	Reason                        string
}

func (r Requirement) shareOf(q QuestionReport, cut float64) (Share, string) {
	if r.Measure.YesNo() {
		if q.YesNo == nil {
			return Share{}, r.notApplicable(q)
		}

		row, found := rowAt(q.YesNo.Cuts, cut)
		if !found {
			return Share{}, "the report has no row at cut " + cutText(cut)
		}

		switch r.Measure {
		case Catches:
			return defined(row.Catches, "no record is labelled yes")
		case FalseAlarms:
			return defined(row.FalseAlarms, "no record is labelled no")
		default:
			return defined(row.RightWhenFlagged, "no record is flagged")
		}
	}

	switch {
	case r.Measure == Agreement && q.Pick != nil:
		return defined(q.Pick.Agreement, "no record is labelled")
	case q.Rate != nil && r.Measure == Agreement:
		return defined(q.Rate.Agreement, "no record is labelled")
	case q.Rate != nil:
		return defined(q.Rate.WithinOne, "no record is labelled")
	default:
		return Share{}, r.notApplicable(q)
	}
}

func rowAt(rows []CutRow, cut float64) (CutRow, bool) {
	for _, row := range rows {
		if row.Cut == cut {
			return row, true
		}
	}

	return CutRow{}, false
}

func defined(share Share, reason string) (Share, string) {
	if !share.Defined {
		return Share{}, reason
	}

	return share, ""
}

func (r Requirement) notApplicable(q QuestionReport) string {
	return fmt.Sprintf("'%s' is a %s question, so %s does not apply", q.ID, shapeName(q.Shape), r.Measure)
}

func (r Result) compared() float64 {
	switch r.Bound {
	case Lower:
		return r.Low
	case Upper:
		return r.High
	default:
		return r.Value
	}
}

// String renders the result as one line, the requirement, whether it held, the measured share or
// the reason it has none, and the cut it was read at.
func (r Result) String() string {
	verdict := "held"
	if !r.Held {
		verdict = "did not hold"
	}

	measured := r.Reason

	switch {
	case !r.HasValue:
	case r.HasInterval:
		measured = countedShare(Share{
			Hits: r.Hits, Of: r.Of, Rate: r.Value, Low: r.Low, High: r.High, Defined: true,
		})
	default:
		measured = "AUC " + strconv.FormatFloat(r.Value, 'f', 2, 64)
	}

	line := fmt.Sprintf("'%s' %s: %s", r.Source, verdict, measured)
	if r.HasCut {
		line += " at cut " + cutText(r.Cut)
	}

	return line
}

// Measure names what a requirement reads from a question's score.
type Measure int

// The measures a requirement can read. The first four are yes/no measures, agreement is pick and
// rate, and within_one is rate.
const (
	Catches Measure = iota
	FalseAlarms
	RightWhenFlagged
	AreaUnderCurve
	Agreement
	WithinOne
)

var measureNames = []string{"catches", "false_alarms", "right_when_flagged", "auc", "agreement", "within_one"}

func measureNamed(name string) (Measure, bool) {
	for i, known := range measureNames {
		if name == known {
			return Measure(i), true
		}
	}

	return 0, false
}

func measureList() string {
	return strings.Join(measureNames[:len(measureNames)-1], ", ") + " or " + measureNames[len(measureNames)-1]
}

// String is the measure as a requirement spells it.
func (m Measure) String() string {
	if m < 0 || int(m) >= len(measureNames) {
		return "measure(" + strconv.Itoa(int(m)) + ")"
	}

	return measureNames[m]
}

// YesNo reports whether the measure reads a yes/no question.
func (m Measure) YesNo() bool {
	return m <= AreaUnderCurve
}

// NeedsCut reports whether the measure is read at one cut, which every yes/no measure but auc is.
func (m Measure) NeedsCut() bool {
	return m < AreaUnderCurve
}

// Op is a requirement's comparison.
type Op int

// The comparisons a requirement can make.
const (
	OpGE Op = iota
	OpGT
	OpLE
	OpLT
)

// String is the operator as a requirement spells it.
func (o Op) String() string {
	switch o {
	case OpGE:
		return ">="
	case OpGT:
		return ">"
	case OpLE:
		return "<="
	default:
		return "<"
	}
}

func (o Op) holds(value, threshold float64) bool {
	switch o {
	case OpGE:
		return value >= threshold
	case OpGT:
		return value > threshold
	case OpLE:
		return value <= threshold
	default:
		return value < threshold
	}
}
