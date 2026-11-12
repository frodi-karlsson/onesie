package calibrate

import (
	"math"
	"slices"

	"github.com/frodi-karlsson/onesie/internal/num"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const bisections = 64

// CompareNoul scores a yes or no question in each of several runs over the same records, the first
// run being the baseline. Every run's cases hold the same records, labelled alike, in the same order.
func CompareNoul(runs [][]NoulCase, cuts, budgets []float64) []RunScore {
	scores := make([]RunScore, len(runs))
	ranks := make([]placements, len(runs))

	for i, cases := range runs {
		score := ScoreNoul(cases, 0, cuts)
		scores[i] = RunScore{Noul: score, AUC: aucInterval(score), Budgets: budgetRows(score, budgets), Cases: cases}
		ranks[i] = placementsOf(cases)
	}

	for i := 1; i < len(runs); i++ {
		change := aucChange(scores[0].AUC, scores[i].AUC, correlation(ranks[0], ranks[i]))
		scores[i].Change = &change
	}

	return scores
}

// RunScore is one run's score in a comparison. Change is its AUC less the baseline's, nil on the
// baseline, Budgets holds a row per false alarm budget, and Cases the records scored, in the order
// every run shares.
type RunScore struct {
	Noul    NoulScore
	AUC     Interval
	Change  *Interval
	Budgets []BudgetRow
	Cases   []NoulCase
}

// Interval is a value with its 95 percent interval. Defined is false when there is no value.
type Interval struct {
	Value, Low, High float64
	Defined          bool
}

// BudgetRow is what a run catches at the lowest answered value whose false alarms stay within
// Budget. Found is false when no answered value keeps them within it.
type BudgetRow struct {
	Budget, Cut          float64
	Found                bool
	Catches, FalseAlarms Share
}

func aucInterval(score NoulScore) Interval {
	if !score.HasAUC {
		return Interval{}
	}

	low, high := aucBounds(score.AUC, score.Yes, score.No)

	return Interval{Value: score.AUC, Low: num.Rounded(low), High: num.Rounded(high), Defined: true}
}

func aucBounds(auc float64, yes, no int) (low, high float64) {
	// Newcombe's method 5, from his 2006 paper on intervals for the Mann Whitney statistic. The
	// variance is read at each candidate rather than at the estimate, so a perfect separation keeps
	// some width.
	outside := func(theta float64) bool {
		return (auc-theta)*(auc-theta) > z95*z95*aucVariance(theta, yes, no)
	}

	return boundary(0, auc, outside), boundary(1, auc, outside)
}

func aucVariance(theta float64, yes, no int) float64 {
	spread := float64(yes+no)/2 - 1

	return theta * (1 - theta) / float64(yes*no) * (1 + spread*((1-theta)/(2-theta)+theta/(1+theta)))
}

func boundary(end, estimate float64, outside func(float64) bool) float64 {
	if end == estimate || !outside(end) {
		return end
	}

	inside := estimate
	for range bisections {
		mid := (end + inside) / 2
		if outside(mid) {
			end = mid
		} else {
			inside = mid
		}
	}

	return (end + inside) / 2
}

type placements struct {
	yes, no []float64
}

func placementsOf(cases []NoulCase) placements {
	// DeLong's components: for each yes case the share of no cases it outranks, and for each no case
	// the share of yes cases that outrank it, ties counting half.
	var yesValues, noValues []float64

	for _, c := range cases {
		if c.Yes {
			yesValues = append(yesValues, c.Value)
		} else {
			noValues = append(noValues, c.Value)
		}
	}

	sortedYes, sortedNo := slices.Sorted(slices.Values(yesValues)), slices.Sorted(slices.Values(noValues))
	ranked := placements{yes: make([]float64, len(yesValues)), no: make([]float64, len(noValues))}

	for i, value := range yesValues {
		below, equal := counted(sortedNo, value)
		ranked.yes[i] = (float64(below) + float64(equal)/2) / float64(len(sortedNo))
	}

	for i, value := range noValues {
		below, equal := counted(sortedYes, value)
		above := len(sortedYes) - below - equal
		ranked.no[i] = (float64(above) + float64(equal)/2) / float64(len(sortedYes))
	}

	return ranked
}

func counted(sorted []float64, value float64) (below, equal int) {
	first, _ := slices.BinarySearch(sorted, value)
	last := first
	for last < len(sorted) && sorted[last] == value {
		last++
	}

	return first, last - first
}

func correlation(a, b placements) float64 {
	// Zero when either run has no variance, which widens the change interval rather than narrowing
	// it.
	if len(a.yes) < 2 || len(a.no) < 2 {
		return 0
	}

	varA := covariance(a.yes, a.yes)/float64(len(a.yes)) + covariance(a.no, a.no)/float64(len(a.no))
	varB := covariance(b.yes, b.yes)/float64(len(b.yes)) + covariance(b.no, b.no)/float64(len(b.no))
	shared := covariance(a.yes, b.yes)/float64(len(a.yes)) + covariance(a.no, b.no)/float64(len(a.no))

	if varA <= 0 || varB <= 0 {
		return 0
	}

	return math.Max(-1, math.Min(1, shared/math.Sqrt(varA*varB)))
}

func covariance(a, b []float64) float64 {
	meanA, meanB := mean(a), mean(b)
	total := 0.0

	for i := range a {
		total += (a[i] - meanA) * (b[i] - meanB)
	}

	return total / float64(len(a)-1)
}

func mean(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}

	return total / float64(len(values))
}

func aucChange(baseline, run Interval, rho float64) Interval {
	// Zou and Donner's MOVER, built from the two single run intervals and DeLong's correlation.
	if !baseline.Defined || !run.Defined {
		return Interval{}
	}

	change := run.Value - baseline.Value
	low := change - recovered(run.Value-run.Low, baseline.High-baseline.Value, rho)
	high := change + recovered(run.High-run.Value, baseline.Value-baseline.Low, rho)

	return Interval{
		Value: num.Rounded(change), Low: num.Rounded(math.Max(-1, low)), High: num.Rounded(math.Min(1, high)),
		Defined: true,
	}
}

func recovered(runSide, baselineSide, rho float64) float64 {
	return math.Sqrt(math.Max(0, runSide*runSide+baselineSide*baselineSide-2*rho*runSide*baselineSide))
}

func budgetRows(score NoulScore, budgets []float64) []BudgetRow {
	rows := make([]BudgetRow, 0, len(budgets))

	for _, budget := range budgets {
		row := BudgetRow{Budget: budget}
		allowed := num.Rounded(budget * float64(score.No))

		for _, at := range score.Values {
			if score.No > 0 && float64(at.FalseAlarms.Hits) <= allowed {
				row.Cut, row.Found, row.Catches, row.FalseAlarms = at.Cut, true, at.Catches, at.FalseAlarms

				break
			}
		}

		rows = append(rows, row)
	}

	return rows
}

// CompareChoices scores a pick or rate question in each of several runs over the same records, the
// first run being the baseline. names lists the options, or the levels in order when rate is set.
func CompareChoices(runs [][]ChoiceCase, names []string, rate bool, cuts []float64) []ChoiceRunScore {
	scores := make([]ChoiceRunScore, len(runs))
	right := make([][]bool, len(runs))
	near := make([][]bool, len(runs))

	for i, cases := range runs {
		if rate {
			score := ScoreRate(cases, names, 0, cuts)
			scores[i] = ChoiceRunScore{Pick: score.PickScore, Rate: &score, Cases: cases}
		} else {
			scores[i] = ChoiceRunScore{Pick: ScorePick(cases, names, 0, cuts), Cases: cases}
		}

		right[i], near[i] = make([]bool, len(cases)), make([]bool, len(cases))
		for c, choice := range cases {
			right[i][c] = choice.agrees()
			near[i][c] = withinOne(choice, names)
		}
	}

	for i := 1; i < len(runs); i++ {
		agreement := pairedChange(right[0], right[i])
		scores[i].AgreementChange = &agreement

		if rate {
			within := pairedChange(near[0], near[i])
			scores[i].WithinOneChange = &within
		}
	}

	return scores
}

// ChoiceRunScore is one run's score for a pick or rate question in a comparison. Rate is set for a
// rate question, whose PickScore is Pick. The changes are nil on the baseline, and WithinOneChange
// on a pick question. Cases holds the records scored, in the order every run shares.
type ChoiceRunScore struct {
	Pick                             PickScore
	Rate                             *RateScore
	AgreementChange, WithinOneChange *Interval
	Cases                            []ChoiceCase
}

// ListMoves fills the record lists of each run against the baseline. A yes or no question reads
// them at assertCut when hasAssert is set, and otherwise at each run's cut at the first false alarm
// budget.
func (q *QuestionComparison) ListMoves(assertCut float64, hasAssert bool) {
	if q.Shape != plan.Noul {
		q.ChoiceMoves = make([]ChoiceMoves, len(q.Choices))
		for r := 1; r < len(q.Choices); r++ {
			q.ChoiceMoves[r] = MoveChoices(q.Choices[0].Cases, q.Choices[r].Cases)
		}

		return
	}

	q.Moves = make([]NoulMoves, len(q.Scores))
	baseline := q.Scores[0]

	for r := 1; r < len(q.Scores); r++ {
		run := q.Scores[r]
		baseCut, runCut, from := assertCut, assertCut, CutFromAssert

		if !hasAssert {
			baseCut, runCut, from = budgetCuts(baseline, run)
		}

		q.Moves[r] = MoveNoul(baseline.Cases, run.Cases, baseCut, runCut, from)
	}
}

func budgetCuts(baseline, run RunScore) (baseCut, runCut float64, from CutSource) {
	if len(baseline.Budgets) == 0 || !baseline.Budgets[0].Found || !run.Budgets[0].Found {
		return 0, 0, CutFromNone
	}

	return baseline.Budgets[0].Cut, run.Budgets[0].Cut, CutFromBudget
}

func pairedChange(baseline, run []bool) Interval {
	n := len(baseline)
	if n == 0 {
		return Interval{}
	}

	var both, baseOnly, runOnly, neither int

	for i := range baseline {
		switch {
		case baseline[i] && run[i]:
			both++
		case baseline[i]:
			baseOnly++
		case run[i]:
			runOnly++
		default:
			neither++
		}
	}

	// Newcombe's method 10, from his 1998 paper on paired differences of proportions: the two Wilson
	// intervals joined by MOVER, with phi as their correlation. Method 10 shrinks a positive phi by
	// n/2, which widens the interval on a small set. Without that it is his method 8.
	before, after := wilson(both+baseOnly, n), wilson(both+runOnly, n)
	p1, p2 := float64(both+baseOnly)/float64(n), float64(both+runOnly)/float64(n)

	phi := 0.0
	if margins := float64(both+baseOnly) * float64(runOnly+neither) * float64(both+runOnly) * float64(baseOnly+neither); margins > 0 {
		concordance := float64(both)*float64(neither) - float64(baseOnly)*float64(runOnly)
		if concordance > 0 {
			concordance = math.Max(concordance-float64(n)/2, 0)
		}

		phi = concordance / math.Sqrt(margins)
	}

	change := p2 - p1
	low := change - recovered(p2-after.Low, before.High-p1, phi)
	high := change + recovered(after.High-p2, p1-before.Low, phi)

	return Interval{
		Value: num.Rounded(change), Low: num.Rounded(math.Max(-1, low)), High: num.Rounded(math.Min(1, high)),
		Defined: true,
	}
}
