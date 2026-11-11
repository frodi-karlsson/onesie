package calibrate

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/num"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

// Comparison is several answers files scored against one label set, the first file being the
// baseline. Records counts the input records, and each question compares only the records every
// file answers.
type Comparison struct {
	Runs      []Run
	Records   int
	Questions []QuestionComparison
	Require   []RunResult
}

// Run is one compared answers file, with the models that answered the records it holds for the
// input.
type Run struct {
	File   string
	Models []string
}

// QuestionComparison is one yes or no question compared. LeftOut and Scores hold an entry per run,
// in run order.
type QuestionComparison struct {
	ID       string
	Labelled int
	LeftOut  []LeftOut
	Scores   []RunScore
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

	_, err := io.WriteString(w, b.String())

	return err
}

func writeCompared(b *strings.Builder, runs []Run, q QuestionComparison) {
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

	signed := twoPlaces(change.Value)
	if change.Value > 0 && signed != "0.00" {
		signed = "+" + signed
	}

	return signed + "  " + twoPlaces(change.Low) + " to " + twoPlaces(change.High)
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

		doc.Runs = append(doc.Runs, jsonRun{File: run.File, Models: models})
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
	Questions []jsonCompared   `json:"questions"`
	Require   []jsonRunRequire `json:"require,omitempty"`
}

type jsonRun struct {
	File   string   `json:"file"`
	Models []string `json:"models"`
}

func comparedJSON(runs []Run, q QuestionComparison) jsonCompared {
	baseline := q.Scores[0].YesNo
	compared := jsonCompared{
		ID: q.ID, Shape: ShapeName(plan.Noul), Labelled: q.Labelled, Compared: baseline.Labelled,
		Yes: baseline.Yes, No: baseline.No,
		LeftOut: make([]jsonLeftOut, 0, len(runs)), Runs: make([]jsonRunScore, 0, len(runs)),
	}

	for i, run := range runs {
		left := q.LeftOut[i]
		compared.LeftOut = append(compared.LeftOut, jsonLeftOut{
			File: run.File, Unanswered: left.Unanswered, Unusable: left.Unusable,
		})
		compared.Runs = append(compared.Runs, runScoreJSON(run.File, q.Scores[i]))
	}

	return compared
}

type jsonCompared struct {
	ID       string         `json:"id"`
	Shape    string         `json:"shape"`
	Labelled int            `json:"labelled"`
	Compared int            `json:"compared"`
	Yes      int            `json:"yes"`
	No       int            `json:"no"`
	LeftOut  []jsonLeftOut  `json:"left_out"`
	Runs     []jsonRunScore `json:"runs"`
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

type jsonRunScore struct {
	File          string        `json:"file"`
	AUC           *jsonInterval `json:"auc"`
	AUCChange     *jsonInterval `json:"auc_change,omitempty"`
	AtFalseAlarms []jsonBudget  `json:"at_false_alarms"`
	Cuts          []jsonCutRow  `json:"cuts"`
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
