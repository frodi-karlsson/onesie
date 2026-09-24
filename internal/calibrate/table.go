package calibrate

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/width"

	"github.com/frodi-karlsson/onesie/internal/plan"
)

const (
	worstMisses  = 5
	indent       = "  "
	gutter       = "  "
	otherHeading = "other answers"
)

// WriteTable renders the report for reading, one section per question in plan order. Columns are
// sized by their content and ignore the terminal width.
func WriteTable(w io.Writer, r Report) error {
	var b strings.Builder

	for i, q := range r.Questions {
		if i > 0 {
			b.WriteString("\n")
		}

		if err := writeQuestion(&b, q); err != nil {
			return err
		}
	}

	_, err := io.WriteString(w, b.String())

	return err
}

func writeQuestion(b *strings.Builder, q QuestionReport) error {
	switch {
	case q.Shape == plan.Noul && q.YesNo != nil:
		writeYesNo(b, q.ID, *q.YesNo)
	case q.Shape == plan.Pick && q.Pick != nil:
		writeChoice(b, q.ID, q.Shape, "option", *q.Pick, nil)
	case q.Shape == plan.Rate && q.Rate != nil:
		writeChoice(b, q.ID, q.Shape, "level", q.Rate.PickScore, rateLines(*q.Rate))
	default:
		return fmt.Errorf("calibrate: question '%s' has no %s score to report", q.ID, shapeName(q.Shape))
	}

	return nil
}

func writeYesNo(b *strings.Builder, id string, s YesNoScore) {
	fmt.Fprintf(b, "%s, %s: labelled %d, %d yes, %d no, %d failed. %s\n",
		printable(id), shapeName(plan.Noul), s.Labelled, s.Yes, s.No, s.Failed, aucText(s))

	if s.Labelled == 0 {
		return
	}

	fmt.Fprintf(b, "flagged means %s.value >= cut\n\n", printable(id))

	cuts, flagged := make([]string, 0, len(s.Cuts)), make([]string, 0, len(s.Cuts))
	catches, alarms, right := make([]Share, 0, len(s.Cuts)), make([]Share, 0, len(s.Cuts)), make([]Share, 0, len(s.Cuts))

	for _, row := range s.Cuts {
		cuts = append(cuts, cutText(row.Cut))
		flagged = append(flagged, strconv.Itoa(row.Flagged))
		catches = append(catches, row.Catches)
		alarms = append(alarms, row.FalseAlarms)
		right = append(right, row.RightWhenFlagged)
	}

	writeColumns(b,
		column{header: "cut", cells: cuts},
		column{header: "flagged", cells: flagged, right: true},
		column{header: "catches", cells: shareCells(catches)},
		column{header: "false alarms", cells: shareCells(alarms)},
		column{header: "right when flagged", cells: shareCells(right)},
	)

	misses := s.Misses[:min(len(s.Misses), worstMisses)]
	if len(misses) == 0 {
		return
	}

	names, labels, answers := make([]string, 0, len(misses)), make([]string, 0, len(misses)), make([]string, 0, len(misses))
	for _, c := range misses {
		names = append(names, missName(c.Name, c.Line))
		labels = append(labels, "labelled "+yesNoText(c.Yes))
		answers = append(answers, "answered "+cutText(c.Value))
	}

	b.WriteString("\nworst misses\n")
	writeColumns(b, column{cells: names}, column{cells: labels}, column{cells: answers})
}

func aucText(s YesNoScore) string {
	if !s.HasAUC {
		return "AUC needs both yes and no labels"
	}

	return "AUC " + strconv.FormatFloat(s.AUC, 'f', 2, 64)
}

func yesNoText(yes bool) string {
	if yes {
		return "yes"
	}

	return "no"
}

func rateLines(s RateScore) []string {
	lines := []string{"within one level " + countedShare(s.WithinOne)}
	if s.HasMeanDistance {
		lines = append(lines, "mean distance "+strconv.FormatFloat(s.MeanDistance, 'f', 2, 64)+" levels")
	}

	return lines
}

func writeChoice(b *strings.Builder, id string, shape plan.Shape, noun string, s PickScore, extra []string) {
	fmt.Fprintf(b, "%s, %s: labelled %d, %d failed. agreement %s\n",
		printable(id), shapeName(shape), s.Labelled, s.Failed, inlineShare(s.Agreement))

	if s.Labelled == 0 {
		return
	}

	for _, line := range extra {
		b.WriteString(line + "\n")
	}

	b.WriteString("\n")
	writeNames(b, noun, s.Names)
	b.WriteString("\n")
	writeGrid(b, s)
	b.WriteString("\n")
	writeConfidence(b, s.Confidence)
	writeChoiceMisses(b, s.Misses)
}

func writeNames(b *strings.Builder, noun string, rows []PickRow) {
	names, labelled, picked := make([]string, 0, len(rows)), make([]string, 0, len(rows)), make([]string, 0, len(rows))
	found, right := make([]Share, 0, len(rows)), make([]Share, 0, len(rows))

	for _, row := range rows {
		names = append(names, printable(row.Name))
		labelled = append(labelled, strconv.Itoa(row.Labelled))
		picked = append(picked, strconv.Itoa(row.Picked))
		found = append(found, row.Found)
		right = append(right, row.RightWhenPicked)
	}

	writeColumns(b,
		column{header: noun, cells: names},
		column{header: "labelled", cells: labelled, right: true},
		column{header: "picked", cells: picked, right: true},
		column{header: "found", cells: shareCells(found)},
		column{header: "right when picked", cells: shareCells(right)},
	)
}

func writeGrid(b *strings.Builder, s PickScore) {
	names := make([]string, 0, len(s.Names))
	for _, row := range s.Names {
		names = append(names, printable(row.Name))
	}

	columns := []column{{header: `labelled \ picked`, cells: names}}
	for j, name := range names {
		columns = append(columns, countColumn(name, s.Grid, func(row []int) int { return row[j] }))
	}

	if s.Other != nil {
		cells := make([]string, 0, len(s.Other))
		for _, count := range s.Other {
			cells = append(cells, strconv.Itoa(count))
		}

		columns = append(columns, column{header: uniqueHeading(otherHeading, names), cells: cells, right: true})
	}

	writeColumns(b, columns...)
}

func countColumn(header string, grid [][]int, cell func([]int) int) column {
	cells := make([]string, 0, len(grid))
	for _, row := range grid {
		cells = append(cells, strconv.Itoa(cell(row)))
	}

	return column{header: header, cells: cells, right: true}
}

func writeConfidence(b *strings.Builder, rows []ConfidenceRow) {
	cuts := make([]string, 0, len(rows))
	answered, agreement := make([]Share, 0, len(rows)), make([]Share, 0, len(rows))

	for _, row := range rows {
		cuts = append(cuts, cutText(row.Cut))
		answered = append(answered, row.Answered)
		agreement = append(agreement, row.Agreement)
	}

	writeColumns(b,
		column{header: "confidence", cells: cuts},
		column{header: "answered", cells: shareCells(answered)},
		column{header: "agreement", cells: shareCells(agreement)},
	)
}

func writeChoiceMisses(b *strings.Builder, all []ChoiceCase) {
	misses := all[:min(len(all), worstMisses)]
	if len(misses) == 0 {
		return
	}

	names, labels := make([]string, 0, len(misses)), make([]string, 0, len(misses))
	picked, confidence := make([]string, 0, len(misses)), make([]string, 0, len(misses))

	for _, c := range misses {
		names = append(names, missName(c.Name, c.Line))
		labels = append(labels, "labelled "+printable(c.Label))
		picked = append(picked, "picked "+printable(c.Picked))
		confidence = append(confidence, "confidence "+cutText(c.Confidence))
	}

	b.WriteString("\nworst misses\n")
	writeColumns(b, column{cells: names}, column{cells: labels}, column{cells: picked}, column{cells: confidence})
}

func missName(name string, line int) string {
	if name != "" {
		return printable(name)
	}

	return "line " + strconv.Itoa(line)
}

func printable(text string) string {
	if !utf8.ValidString(text) || strings.IndexFunc(text, func(r rune) bool { return !strconv.IsPrint(r) }) >= 0 {
		return strconv.Quote(text)
	}

	return text
}

func uniqueHeading(heading string, taken []string) string {
	unique := heading
	for n := 2; slices.Contains(taken, unique); n++ {
		unique = heading + " " + strconv.Itoa(n)
	}

	return unique
}

func writeColumns(b *strings.Builder, columns ...column) {
	widths := make([]int, len(columns))
	headed := false

	for i, c := range columns {
		widths[i] = displayWidth(c.header)
		headed = headed || c.header != ""

		for _, cell := range c.cells {
			widths[i] = max(widths[i], displayWidth(cell))
		}
	}

	if headed {
		writeRow(b, widths, columns, func(c column) string { return c.header }, false)
	}

	for r := range columns[0].cells {
		writeRow(b, widths, columns, func(c column) string { return c.cells[r] }, true)
	}
}

type column struct {
	header string
	cells  []string
	right  bool
}

func writeRow(b *strings.Builder, widths []int, columns []column, text func(column) string, aligned bool) {
	cells := make([]string, 0, len(columns))
	for i, c := range columns {
		cells = append(cells, pad(text(c), widths[i], aligned && c.right))
	}

	b.WriteString(strings.TrimRight(indent+strings.Join(cells, gutter), " ") + "\n")
}

func pad(text string, width int, right bool) string {
	fill := strings.Repeat(" ", max(0, width-displayWidth(text)))
	if right {
		return fill + text
	}

	return text + fill
}

func displayWidth(text string) int {
	total := 0

	for _, r := range text {
		switch {
		case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		case width.LookupRune(r).Kind() == width.EastAsianWide, width.LookupRune(r).Kind() == width.EastAsianFullwidth:
			total += 2
		default:
			total++
		}
	}

	return total
}

func shareCells(shares []Share) []string {
	type parts struct{ hits, of, rate, low, high string }

	all := make([]parts, 0, len(shares))
	var hw, ow, rw, lw, hiw int

	for _, s := range shares {
		p := parts{hits: strconv.Itoa(s.Hits), of: strconv.Itoa(s.Of), rate: "-"}
		if s.Defined {
			p.rate, p.low, p.high = percent(s.Rate)+"%", percent(s.Low), percent(s.High)+"%"
		}

		hw, ow, rw = max(hw, len(p.hits)), max(ow, len(p.of)), max(rw, len(p.rate))
		lw, hiw = max(lw, len(p.low)), max(hiw, len(p.high))
		all = append(all, p)
	}

	cells := make([]string, 0, len(all))

	for _, p := range all {
		interval := strings.Repeat(" ", lw+1+hiw)
		if p.low != "" {
			interval = pad(p.low, lw, true) + "-" + pad(p.high, hiw, false)
		}

		cells = append(cells, pad(p.hits, hw, true)+"/"+pad(p.of, ow, false)+" "+pad(p.rate, rw, true)+" "+interval)
	}

	return cells
}

func inlineShare(s Share) string {
	if !s.Defined {
		return "-"
	}

	return percent(s.Rate) + "% " + percent(s.Low) + "-" + percent(s.High) + "%"
}

func countedShare(s Share) string {
	return strconv.Itoa(s.Hits) + "/" + strconv.Itoa(s.Of) + " " + inlineShare(s)
}

func percent(rate float64) string {
	rounded := int(math.Round(rate * 100))

	switch {
	case rounded >= 100 && rate < 1:
		rounded = 99
	case rounded <= 0 && rate > 0:
		rounded = 1
	}

	return strconv.Itoa(rounded)
}

func cutText(cut float64) string {
	text := strconv.FormatFloat(cut, 'f', -1, 64)
	if _, decimals, _ := strings.Cut(text, "."); len(decimals) >= 2 {
		return text
	}

	return strconv.FormatFloat(cut, 'f', 2, 64)
}
