package calibrate

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/num"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	perThousand       = 1000
	significantDigits = 3
)

// Comparison is several answers files scored against one label set, the first file being the
// baseline. Records counts the input records, and each question compares only the records every
// file answers. Compared counts the records any question compares, which usage covers.
type Comparison struct {
	Runs      []Run
	Records   int
	Compared  int
	Questions []QuestionComparison
	Require   []RunResult
}

// Run is one compared answers file, with the models that answered the records it holds for the
// input. Usage sums the compared records that carry one, and is nil when none does. Its Cost is nil
// unless every one of them carries a cost.
type Run struct {
	File   string
	Models []string
	Usage  *Usage
}

// QuestionComparison is one question compared. LeftOut holds an entry per run in run order, and so
// does Scores for a yes or no question or Choices for a pick or rate question.
type QuestionComparison struct {
	ID       string
	Shape    plan.Shape
	Labelled int
	LeftOut  []LeftOut
	Scores   []RunScore
	Choices  []ChoiceRunScore
	// Moves and ChoiceMoves list each run against the baseline, by shape, with nothing at the
	// baseline's own index.
	Moves       []YesNoMoves
	ChoiceMoves []ChoiceMoves
}

// LeftOut counts the labelled records a run leaves out of a question's comparison: those no line
// of it answers, an error line included, and those whose answer cannot be scored.
type LeftOut struct {
	Unanswered, Unusable int
}

// RunResult is a requirement measured on one run.
type RunResult struct {
	File string
	Result
}

// String names the run before the result.
func (r RunResult) String() string {
	return r.File + ": " + r.Result.String()
}

// WriteComparisonTable renders a comparison for reading, one section per question with a row per
// run.
func WriteComparisonTable(w io.Writer, c Comparison) error {
	var b strings.Builder

	for i, q := range c.Questions {
		if i > 0 {
			b.WriteString("\n")
		}

		writeCompared(&b, c.Runs, q)
	}

	writeUsage(&b, c.Runs, c.Compared)

	_, err := io.WriteString(w, b.String())

	return err
}

func writeCompared(b *strings.Builder, runs []Run, q QuestionComparison) {
	if q.Shape != plan.Noul {
		writeComparedChoices(b, runs, q)

		return
	}

	baseline := q.Scores[0].YesNo
	fmt.Fprintf(b, "%s, %s: compared %d of %d labelled, %d yes, %d no\n",
		printable(q.ID), ShapeName(plan.Noul), baseline.Labelled, q.Labelled, baseline.Yes, baseline.No)

	if baseline.Labelled == 0 {
		return
	}

	b.WriteString("\n")

	files, models := make([]string, 0, len(runs)), make([]string, 0, len(runs))
	aucs, changes := make([]string, 0, len(runs)), make([]string, 0, len(runs))

	for i, run := range runs {
		files = append(files, printable(run.File))
		models = append(models, modelsText(run.Models))
		aucs = append(aucs, intervalText(q.Scores[i].AUC))
		changes = append(changes, changeText(q.Scores[i].Change))
	}

	columns := []column{
		{header: "run", cells: files},
		{header: "model", cells: models},
		{header: "AUC", cells: aucs},
		{header: "change from baseline", cells: changes},
	}

	for at, budget := range q.Scores[0].Budgets {
		columns = append(columns, budgetColumn(budget.Budget, q.Scores, at))
	}

	writeColumns(b, columns...)

	budget := 0.0
	if len(q.Scores[0].Budgets) > 0 {
		budget = q.Scores[0].Budgets[0].Budget
	}

	for r := 1; r < len(q.Moves); r++ {
		writeYesNoMoves(b, runs[0].File, runs[r].File, q.Moves[r], budget)
	}
}

func writeComparedChoices(b *strings.Builder, runs []Run, q QuestionComparison) {
	compared := q.Choices[0].Pick.Labelled
	fmt.Fprintf(b, "%s, %s: compared %d of %d labelled\n", printable(q.ID), ShapeName(q.Shape), compared, q.Labelled)

	if compared == 0 {
		return
	}

	b.WriteString("\n")

	files, models := make([]string, 0, len(runs)), make([]string, 0, len(runs))
	agreements, agreementChanges := make([]Share, 0, len(runs)), make([]string, 0, len(runs))
	within, withinChanges := make([]Share, 0, len(runs)), make([]string, 0, len(runs))

	for i, run := range runs {
		score := q.Choices[i]
		files = append(files, printable(run.File))
		models = append(models, modelsText(run.Models))
		agreements = append(agreements, score.Pick.Agreement)
		agreementChanges = append(agreementChanges, changeText(score.AgreementChange))

		if score.Rate != nil {
			within = append(within, score.Rate.WithinOne)
			withinChanges = append(withinChanges, changeText(score.WithinOneChange))
		}
	}

	columns := []column{
		{header: "run", cells: files},
		{header: "model", cells: models},
		{header: "agreement", cells: shareCells(agreements)},
		{header: "change from baseline", cells: agreementChanges},
	}

	// A rate row carries two changes, so each names its measure.
	if q.Shape == plan.Rate {
		columns[3].header = "agreement change"
		columns = append(columns,
			column{header: "within one level", cells: shareCells(within)},
			column{header: "within one change", cells: withinChanges},
		)
	}

	writeColumns(b, columns...)

	for r := 1; r < len(q.ChoiceMoves); r++ {
		writeChoiceMoves(b, runs[0].File, runs[r].File, q.ChoiceMoves[r])
	}
}

func writeUsage(b *strings.Builder, runs []Run, compared int) {
	if !slices.ContainsFunc(runs, func(run Run) bool { return run.Usage != nil }) {
		return
	}

	files, records := make([]string, 0, len(runs)), make([]string, 0, len(runs))
	inputs, outputs, costs := make([]string, 0, len(runs)), make([]string, 0, len(runs)), make([]string, 0, len(runs))

	for _, run := range runs {
		files = append(files, printable(run.File))

		usage := run.Usage
		if usage == nil {
			records, inputs, outputs, costs = append(records, "-"), append(inputs, "-"), append(outputs, "-"), append(costs, "-")

			continue
		}

		per := float64(usage.Records)
		records = append(records, strconv.Itoa(usage.Records))
		inputs = append(inputs, strconv.Itoa(int(math.Round(float64(usage.InputTokens)/per))))
		outputs = append(outputs, strconv.Itoa(int(math.Round(float64(usage.OutputTokens)/per))))

		if usage.Cost == nil {
			costs = append(costs, "-")
		} else {
			costs = append(costs, "$"+significant(*usage.Cost/per*perThousand))
		}
	}

	fmt.Fprintf(b, "\nusage, mean per record over the %d records any question compares\n\n", compared)
	writeColumns(b,
		column{header: "run", cells: files},
		column{header: "records", cells: records, right: true},
		column{header: "input tokens", cells: inputs, right: true},
		column{header: "output tokens", cells: outputs, right: true},
		column{header: "cost per 1000", cells: costs, right: true},
	)
}

func significant(value float64) string {
	if value == 0 {
		return "0"
	}

	scale := math.Pow(10, float64(max(0, significantDigits-1-int(math.Floor(math.Log10(math.Abs(value)))))))

	return strconv.FormatFloat(math.Round(value*scale)/scale, 'f', -1, 64)
}

func modelsText(models []string) string {
	if len(models) == 0 {
		return "-"
	}

	printed := make([]string, len(models))
	for i, model := range models {
		printed[i] = printable(model)
	}

	return strings.Join(printed, ", ")
}

func intervalText(i Interval) string {
	if !i.Defined {
		return "-"
	}

	return twoPlaces(i.Value) + "  " + twoPlaces(i.Low) + "-" + twoPlaces(i.High)
}

func changeText(change *Interval) string {
	switch {
	case change == nil:
		return "baseline"
	case !change.Defined:
		return "-"
	}

	return signedTwoPlaces(change.Value) + "  " + twoPlaces(change.Low) + " to " + twoPlaces(change.High)
}

func twoPlaces(value float64) string {
	text := strconv.FormatFloat(value, 'f', 2, 64)
	if text == "-0.00" {
		return "0.00"
	}

	return text
}

func budgetColumn(budget float64, scores []RunScore, at int) column {
	catches := make([]Share, len(scores))
	for i, score := range scores {
		catches[i] = score.Budgets[at].Catches
	}

	cells := shareCells(catches)
	for i, score := range scores {
		row := score.Budgets[at]
		if !row.Found {
			cells[i] = "-"

			continue
		}

		cells[i] += " at " + cutText(row.Cut)
	}

	return column{header: "catches at " + percentText(budget) + " false alarms", cells: cells}
}

func percentText(fraction float64) string {
	return strconv.FormatFloat(num.Rounded(fraction*percentScale), 'f', -1, 64) + "%"
}

// WriteComparisonJSON renders a comparison as one json object on one line.
func WriteComparisonJSON(w io.Writer, c Comparison) error {
	doc := jsonComparison{
		Runs:      make([]jsonRun, 0, len(c.Runs)),
		Records:   c.Records,
		Compared:  c.Compared,
		Questions: make([]jsonCompared, 0, len(c.Questions)),
		Require:   runRequireJSON(c.Require),
	}

	if len(c.Runs) > 0 {
		doc.Baseline = c.Runs[0].File
	}

	for _, run := range c.Runs {
		models := run.Models
		if models == nil {
			models = []string{}
		}

		doc.Runs = append(doc.Runs, jsonRun{File: run.File, Models: models, Usage: run.Usage})
	}

	for _, q := range c.Questions {
		doc.Questions = append(doc.Questions, comparedJSON(c.Runs, q))
	}

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	return enc.Encode(doc)
}

type jsonComparison struct {
	Baseline  string           `json:"baseline"`
	Runs      []jsonRun        `json:"runs"`
	Records   int              `json:"records"`
	Compared  int              `json:"compared"`
	Questions []jsonCompared   `json:"questions"`
	Require   []jsonRunRequire `json:"require,omitempty"`
}

type jsonRun struct {
	File   string   `json:"file"`
	Models []string `json:"models"`
	Usage  *Usage   `json:"usage,omitempty"`
}

func comparedJSON(runs []Run, q QuestionComparison) jsonCompared {
	compared := jsonCompared{
		ID: q.ID, Shape: ShapeName(q.Shape), Labelled: q.Labelled,
		LeftOut: make([]jsonLeftOut, 0, len(runs)), Runs: make([]jsonRunScore, 0, len(runs)),
	}

	if q.Shape == plan.Noul {
		baseline := q.Scores[0].YesNo
		compared.Compared, compared.Yes, compared.No = baseline.Labelled, &baseline.Yes, &baseline.No
	} else {
		compared.Compared = q.Choices[0].Pick.Labelled
	}

	for i, run := range runs {
		left := q.LeftOut[i]
		compared.LeftOut = append(compared.LeftOut, jsonLeftOut{
			File: run.File, Unanswered: left.Unanswered, Unusable: left.Unusable,
		})

		if q.Shape == plan.Noul {
			compared.Runs = append(compared.Runs, runScoreJSON(run.File, q.Scores[i]))
		} else {
			compared.Runs = append(compared.Runs, choiceScoreJSON(run.File, q.Choices[i]))
		}
	}

	for r := 1; r < len(q.Moves); r++ {
		compared.Changes = append(compared.Changes, yesNoMovesJSON(runs[r].File, q.Moves[r]))
	}

	for r := 1; r < len(q.ChoiceMoves); r++ {
		compared.Changes = append(compared.Changes, choiceMovesJSON(runs[r].File, q.ChoiceMoves[r]))
	}

	return compared
}

type jsonCompared struct {
	ID       string         `json:"id"`
	Shape    string         `json:"shape"`
	Labelled int            `json:"labelled"`
	Compared int            `json:"compared"`
	Yes      *int           `json:"yes,omitempty"`
	No       *int           `json:"no,omitempty"`
	LeftOut  []jsonLeftOut  `json:"left_out"`
	Runs     []jsonRunScore `json:"runs"`
	Changes  []jsonChanges  `json:"changes,omitempty"`
}

func yesNoMovesJSON(file string, moves YesNoMoves) jsonChanges {
	entry := jsonChanges{
		File: file, CutFrom: cutFromJSON(moves.From), Small: &moves.Small,
		MeanMove: &jsonMeanMove{Yes: moves.MeanYes, No: moves.MeanNo},
		GotWorse: make([]jsonMove, 0, len(moves.Worse)), GotBetter: make([]jsonMove, 0, len(moves.Better)),
	}

	if moves.From != CutFromNone {
		entry.BaselineCut, entry.RunCut = &moves.BaseCut, &moves.RunCut
	}

	for _, list := range []struct {
		moves []YesNoMove
		into  *[]jsonMove
	}{{moves.Worse, &entry.GotWorse}, {moves.Better, &entry.GotBetter}} {
		for _, move := range list.moves {
			label := yesNoText(move.Case.Yes)
			before, after, size := move.Before, move.After, move.Move
			*list.into = append(*list.into, jsonMove{
				jsonRecord: recordJSON(move.Case.ID, move.Case.Line), Label: label, Before: before, After: after,
				Move: &size, Crossed: &move.Crossed, Small: &move.Small, CutMoved: &move.CutMoved,
			})
		}
	}

	return entry
}

func cutFromJSON(from CutSource) string {
	switch from {
	case CutFromAssert:
		return "assert"
	case CutFromBudget:
		return "false_alarms"
	default:
		return "none"
	}
}

func choiceMovesJSON(file string, moves ChoiceMoves) jsonChanges {
	entry := jsonChanges{
		File: file, Unchanged: &moves.Unchanged,
		GotWorse: make([]jsonMove, 0, len(moves.Worse)), GotBetter: make([]jsonMove, 0, len(moves.Better)),
	}

	for _, list := range []struct {
		moves []ChoiceMove
		into  *[]jsonMove
	}{{moves.Worse, &entry.GotWorse}, {moves.Better, &entry.GotBetter}} {
		for _, move := range list.moves {
			before, after := move.BeforeConfidence, move.Case.Confidence
			*list.into = append(*list.into, jsonMove{
				jsonRecord: recordJSON(move.Case.ID, move.Case.Line), Label: move.Case.Label,
				Before: move.BeforePick, After: move.Case.Picked, BeforeConfidence: &before, AfterConfidence: &after,
			})
		}
	}

	return entry
}

// jsonChanges lists one run against the baseline. A yes or no question fills the cut and small
// fields, and a pick or rate question unchanged.
type jsonChanges struct {
	File        string        `json:"file"`
	CutFrom     string        `json:"cut_from,omitempty"`
	BaselineCut *float64      `json:"baseline_cut,omitempty"`
	RunCut      *float64      `json:"run_cut,omitempty"`
	GotWorse    []jsonMove    `json:"got_worse"`
	GotBetter   []jsonMove    `json:"got_better"`
	Small       *int          `json:"small,omitempty"`
	MeanMove    *jsonMeanMove `json:"mean_move,omitempty"`
	Unchanged   *int          `json:"unchanged,omitempty"`
}

type jsonMeanMove struct {
	Yes float64 `json:"yes"`
	No  float64 `json:"no"`
}

// jsonMove is one record's answer before and after. Before and After are values for a yes or no
// question and names for a pick or rate question, which adds the confidences.
type jsonMove struct {
	jsonRecord
	Label            string   `json:"label"`
	Before           any      `json:"before"`
	After            any      `json:"after"`
	Move             *float64 `json:"move,omitempty"`
	Crossed          *bool    `json:"crossed,omitempty"`
	Small            *bool    `json:"small,omitempty"`
	CutMoved         *bool    `json:"cut_moved,omitempty"`
	BeforeConfidence *float64 `json:"before_confidence,omitempty"`
	AfterConfidence  *float64 `json:"after_confidence,omitempty"`
}

func choiceScoreJSON(file string, score ChoiceRunScore) jsonRunScore {
	entry := jsonRunScore{File: file, Agreement: &score.Pick.Agreement}

	if score.AgreementChange != nil {
		entry.AgreementChange = intervalJSON(*score.AgreementChange)
	}

	if score.Rate != nil {
		entry.WithinOne = &score.Rate.WithinOne
	}

	if score.WithinOneChange != nil {
		entry.WithinOneChange = intervalJSON(*score.WithinOneChange)
	}

	return entry
}

type jsonLeftOut struct {
	File       string `json:"file"`
	Unanswered int    `json:"unanswered"`
	Unusable   int    `json:"unusable"`
}

func runScoreJSON(file string, score RunScore) jsonRunScore {
	entry := jsonRunScore{
		File: file, AUC: intervalJSON(score.AUC), AtFalseAlarms: make([]jsonBudget, 0, len(score.Budgets)),
		Cuts: cutRowsJSON(score.YesNo.Cuts),
	}

	if score.Change != nil {
		entry.AUCChange = intervalJSON(*score.Change)
	}

	for _, row := range score.Budgets {
		entry.AtFalseAlarms = append(entry.AtFalseAlarms, jsonBudget{
			Budget: row.Budget, Cut: definedOrNull(row.Cut, row.Found), Catches: row.Catches,
			FalseAlarms: row.FalseAlarms,
		})
	}

	return entry
}

// jsonRunScore is one run of a compared question. A yes or no question fills the AUC fields, and a
// pick or rate question the agreement fields, so each leaves the others out.
type jsonRunScore struct {
	File            string        `json:"file"`
	AUC             *jsonInterval `json:"auc,omitempty"`
	AUCChange       *jsonInterval `json:"auc_change,omitempty"`
	AtFalseAlarms   []jsonBudget  `json:"at_false_alarms,omitempty"`
	Cuts            []jsonCutRow  `json:"cuts,omitempty"`
	Agreement       *Share        `json:"agreement,omitempty"`
	AgreementChange *jsonInterval `json:"agreement_change,omitempty"`
	WithinOne       *Share        `json:"within_one,omitempty"`
	WithinOneChange *jsonInterval `json:"within_one_change,omitempty"`
}

func intervalJSON(i Interval) *jsonInterval {
	if !i.Defined {
		return nil
	}

	return &jsonInterval{Value: i.Value, Low: i.Low, High: i.High}
}

type jsonInterval struct {
	Value float64 `json:"value"`
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
}

type jsonBudget struct {
	Budget      float64  `json:"budget"`
	Cut         *float64 `json:"cut"`
	Catches     Share    `json:"catches"`
	FalseAlarms Share    `json:"false_alarms"`
}

func runRequireJSON(results []RunResult) []jsonRunRequire {
	if len(results) == 0 {
		return nil
	}

	plain := make([]Result, len(results))
	for i, r := range results {
		plain[i] = r.Result
	}

	out := make([]jsonRunRequire, 0, len(results))
	for i, entry := range requireJSON(plain) {
		out = append(out, jsonRunRequire{File: results[i].File, jsonRequire: entry})
	}

	return out
}

type jsonRunRequire struct {
	File string `json:"file"`
	jsonRequire
}
