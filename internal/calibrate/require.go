package calibrate

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"

	"github.com/frodi-karlsson/onesie/internal/num"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	percentScale  = 100
	abstainCut    = "abstain"
	atWord        = "at"
	forbiddenInID = "()<>="
)

var (
	measureNames = []string{
		"catches", "false_alarms", "right_when_flagged", "auc", "agreement", "within_one",
		"auc_change", "catches_change", "false_alarms_change", "agreement_change", "within_one_change",
	}
	bounds = []struct {
		word  string
		bound Bound
	}{{"lower", Lower}, {"upper", Upper}}
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

// At is the cut a yes or no requirement reads, named by a number or taken from the file's
// abstain_if. Value is set only for AtNumber.
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
	for _, b := range bounds {
		after, named := strings.CutPrefix(source, b.word)
		if !named {
			continue
		}

		inner, opened := strings.CutPrefix(after, "(")
		if !opened {
			if strings.HasPrefix(strings.TrimLeftFunc(after, unicode.IsSpace), "(") {
				return "", "", fmt.Errorf("writes %s with a space before (. lower and upper take no space before (",
					b.word)
			}

			break
		}

		req.Bound = b.bound

		target, rest, closed := strings.Cut(inner, ")")
		if !closed {
			return "", "", fmt.Errorf("has %s( with no closing )", b.word)
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

	threshold, err := fraction(fields[0], r.Measure.Change())
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

	cut, err := fraction(fields[2], false)
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

func fraction(text string, signed bool) (float64, error) {
	bounds, low := "between 0 and 1", 0.0
	if signed {
		bounds, low = "between -1 and 1", -1
	}

	value, err := strconv.ParseFloat(text, 64)

	switch {
	case err != nil || math.IsNaN(value):
		return 0, fmt.Errorf("takes a number %s, got '%s'", bounds, text)
	case math.Abs(value) > 1 && math.Abs(value) <= percentScale && (signed || value > 0):
		return 0, fmt.Errorf("takes a fraction %s, so write %s, not %s", bounds,
			strconv.FormatFloat(value/percentScale, 'f', -1, 64), text)
	case value < low || value > 1:
		return 0, fmt.Errorf("takes a number %s, got '%s'", bounds, text)
	case value == 0:
		// A negative zero passes the range check, and a report prints the plain zero it stands for.
		return 0, nil
	}

	return value, nil
}

func (r *Requirement) checkCombination() error {
	switch {
	case (r.Measure == AreaUnderCurve || r.Measure == AUCChange) && r.At.Kind != AtNone:
		return fmt.Errorf("reads %s, and auc covers every cut, so at does not apply", r.Measure)
	case !r.Measure.NeedsCut() && r.At.Kind != AtNone:
		return fmt.Errorf("reads %s, and at applies only to catches, false_alarms, right_when_flagged, "+
			"catches_change and false_alarms_change", r.Measure)
	case r.Bound == Lower && (r.Op == OpLE || r.Op == OpLT):
		return errors.New("uses lower with an upper limit. lower takes >= or >, since it reads the low end of the interval")
	case r.Bound == Upper && (r.Op == OpGE || r.Op == OpGT):
		return errors.New("uses upper with a lower limit. upper takes <= or <, since it reads the high end of the interval")
	}

	return nil
}

// Check measures the requirement against one question's score, reading a yes or no measure at cut.
// A measure over no records, or at a cut the score has no row for, does not hold.
func (r Requirement) Check(q QuestionReport, cut float64) Result {
	result := Result{Requirement: r}
	if r.Measure.NeedsCut() {
		result.HasCut, result.Cut = true, cut
	}

	if r.Measure.Change() {
		result.Reason = r.NeedsCompare()

		return result
	}

	if r.Measure == AreaUnderCurve {
		switch {
		case q.YesNo == nil:
			result.Reason = r.notApplicable(q)
		case !q.YesNo.HasAUC:
			result.Reason = "AUC needs both yes and no labels"
		default:
			interval := aucInterval(*q.YesNo)
			result.Value, result.Low, result.High = interval.Value, interval.Low, interval.High
			result.HasValue, result.HasInterval = true, true
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

// CheckChange measures a change requirement against one run of a compared question, reading
// catches_change and false_alarms_change at cut. run is never the baseline, which has no change.
func (r Requirement) CheckChange(q QuestionComparison, run int, cut float64) Result {
	result := Result{Requirement: r}
	if r.Measure.NeedsCut() {
		result.HasCut, result.Cut = true, cut
	}

	var change *Interval

	switch {
	case r.Measure == AUCChange && q.Shape == plan.Noul:
		change = q.Scores[run].Change
	case r.Measure == CatchesChange && q.Shape == plan.Noul:
		flagged := pairedFlags(q.Scores[0].Cases, q.Scores[run].Cases, cut, true)
		change = &flagged
	case r.Measure == FalseAlarmsChange && q.Shape == plan.Noul:
		flagged := pairedFlags(q.Scores[0].Cases, q.Scores[run].Cases, cut, false)
		change = &flagged
	case r.Measure == AgreementChange && q.Shape != plan.Noul:
		change = q.Choices[run].AgreementChange
	case r.Measure == WithinOneChange && q.Shape == plan.Rate:
		change = q.Choices[run].WithinOneChange
	default:
		result.Reason = r.notApplicable(QuestionReport{ID: q.ID, Shape: q.Shape})

		return result
	}

	if change == nil || !change.Defined {
		result.Reason = "the change needs records on both sides"

		return result
	}

	result.Value, result.Low, result.High = change.Value, change.Low, change.High
	result.HasValue, result.HasInterval = true, true
	result.Held = r.Op.holds(result.compared(), r.Threshold)

	return result
}

// NeedsCompare says why a change measure cannot be read outside a --compare, in words that follow
// the requirement.
func (r Requirement) NeedsCompare() string {
	return fmt.Sprintf("reads %s, a change from a baseline, which needs --compare", r.Measure)
}

func pairedFlags(baseline, run []YesNoCase, cut float64, labelledYes bool) Interval {
	var before, after []bool

	for i, c := range baseline {
		if c.Yes != labelledYes {
			continue
		}

		before = append(before, c.Value >= cut)
		after = append(after, run[i].Value >= cut)
	}

	return pairedChange(before, after)
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
	return fmt.Sprintf("'%s' is a %s question, so %s does not apply", q.ID, ShapeName(q.Shape), r.Measure)
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
	case r.Measure.Change():
		measured = fmt.Sprintf("change %s, interval %s to %s", num.Format(r.Value), num.Format(r.Low), num.Format(r.High))
	case r.Measure == AreaUnderCurve:
		measured = fmt.Sprintf("AUC %s, interval %s to %s", num.Format(r.Value), num.Format(r.Low), num.Format(r.High))
	case r.HasInterval:
		// The four place numbers the requirement compared, not the table's percents, so a miss by
		// less than a percent still shows.
		measured = fmt.Sprintf("%d/%d is %s, interval %s to %s", r.Hits, r.Of,
			num.Format(r.Value), num.Format(r.Low), num.Format(r.High))
	default:
		measured = "AUC " + num.Format(r.Value)
	}

	line := fmt.Sprintf("'%s' %s: %s", r.Source, verdict, measured)
	if !r.HasCut {
		return line
	}

	if r.HasValue && r.HasInterval {
		line += ","
	}

	return line + " at cut " + cutText(r.Cut)
}

// Measure names what a requirement reads from a question's score.
type Measure int

// The measures a requirement can read. The first four are yes or no measures, agreement is pick
// and rate, and within_one is rate. Each change measure reads a run less the baseline, and only
// under --compare.
const (
	Catches Measure = iota
	FalseAlarms
	RightWhenFlagged
	AreaUnderCurve
	Agreement
	WithinOne
	AUCChange
	CatchesChange
	FalseAlarmsChange
	AgreementChange
	WithinOneChange
)

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

// YesNo reports whether the measure reads a yes or no question.
func (m Measure) YesNo() bool {
	switch m {
	case Catches, FalseAlarms, RightWhenFlagged, AreaUnderCurve, AUCChange, CatchesChange, FalseAlarmsChange:
		return true
	default:
		return false
	}
}

// NeedsCut reports whether the measure is read at one cut, which every yes or no measure but auc
// and its change is.
func (m Measure) NeedsCut() bool {
	return m.YesNo() && m != AreaUnderCurve && m != AUCChange
}

// Change reports whether the measure reads a run less the baseline of a --compare.
func (m Measure) Change() bool {
	switch m {
	case AUCChange, CatchesChange, FalseAlarmsChange, AgreementChange, WithinOneChange:
		return true
	default:
		return false
	}
}

// Within reports whether the measure reads the share within one level, which only a rate question
// has.
func (m Measure) Within() bool {
	return m == WithinOne || m == WithinOneChange
}

// Op is how a requirement compares its measured bound with its Threshold.
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
