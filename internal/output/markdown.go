package output

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/frodi-karlsson/onesie/internal/jev"
)

// MarkdownOptions carries what a markdown record says beyond the record itself.
type MarkdownOptions struct {
	// Assert is the gate's source. Empty means the run has no gate and the record shows no verdict.
	Assert string
	// Abstain is the --abstain-if source, quoted when a record abstained.
	Abstain string
}

// WriteMarkdown writes one record as a GitHub flavoured markdown table under an alert for its
// verdict, with the model and usage as a footer.
func WriteMarkdown(w io.Writer, rec Record, opts MarkdownOptions) error {
	var sections []string

	switch {
	case rec.Failure != nil:
		sections = append(sections, alert("CAUTION", "**No answer:** "+codeSpan(rec.Failure.Message, false)))
	case opts.Assert != "":
		sections = append(sections, verdict(rec, opts))
	}

	if rec.Failure == nil {
		sections = append(sections, answerTable(rec.Answers))
	}

	if footer := footerOf(modelsOf(rec.Model), rec.Usage); footer != "" {
		sections = append(sections, footer)
	}

	_, err := io.WriteString(w, strings.Join(sections, "\n"))

	return err
}

func verdict(rec Record, opts MarkdownOptions) string {
	gate := codeSpan(opts.Assert, false)

	switch {
	case rec.AssertFailed:
		return alert("CAUTION", "**Failed:** "+gate+" did not hold.")
	case rec.Abstained:
		return alert("WARNING", "**Unsure:** "+gate+" did not hold and "+
			codeSpan(opts.Abstain, false)+" did, so a person should decide.")
	default:
		return alert("TIP", "**Passed:** "+gate+" held.")
	}
}

func alert(kind, text string) string {
	return "> [!" + kind + "]\n> " + text + "\n"
}

func answerTable(answers []Named) string {
	var b strings.Builder

	b.WriteString(tableHead("question", "answer", "confidence"))

	for _, named := range answers {
		if named.Answer == nil {
			continue
		}

		b.WriteString(tableRow(
			codeSpan(named.ID, true), answerText(named.Answer), confidenceText(named.Answer)))
	}

	return b.String()
}

func tableHead(columns ...string) string {
	return tableRow(columns...) + strings.Repeat("|---", len(columns)) + "|\n"
}

func tableRow(cells ...string) string {
	var b strings.Builder

	for _, cell := range cells {
		b.WriteString("| ")

		if cell != "" {
			b.WriteString(cell + " ")
		}
	}

	b.WriteString("|\n")

	return b.String()
}

func answerText(a *Answer) string {
	if a.Fallback != "" {
		return "fallback " + decisionText(a.Decision)
	}

	if probability, ok := a.Value.(float64); ok {
		if a.Decided {
			return decisionText(a.Decision) + ", " + probabilityText(probability)
		}

		return probabilityText(probability)
	}

	level := fmt.Sprint(a.Value)
	if text, ok := a.Legend[level]; ok {
		level += " " + text
	}

	return codeSpan(level, true)
}

func decisionText(decision any) string {
	switch typed := decision.(type) {
	case bool:
		if typed {
			return "yes"
		}

		return "no"
	case float64:
		return probabilityText(typed)
	default:
		return codeSpan(fmt.Sprint(typed), true)
	}
}

func probabilityText(probability float64) string {
	return strconv.FormatFloat(math.Round(probability*10000)/10000, 'f', -1, 64)
}

func confidenceText(a *Answer) string {
	if a.Confidence == nil {
		return ""
	}

	return strconv.Itoa(int(math.Round(*a.Confidence*100))) + "%"
}

func modelsOf(model string) []string {
	if model == "" {
		return nil
	}

	return []string{model}
}

func footerOf(models []string, usage *jev.Usage) string {
	parts := slices.Clone(models)

	if usage != nil {
		parts = append(parts, fmt.Sprintf("%d in, %d out tokens", usage.InputTokens, usage.OutputTokens))

		if usage.Cost != nil {
			parts = append(parts, "$"+strconv.FormatFloat(math.Round(*usage.Cost*1e8)/1e8, 'f', -1, 64))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return "_" + strings.Join(parts, ", ") + "_\n"
}

func codeSpan(text string, inTable bool) string {
	if text == "" {
		return ""
	}

	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}

		return r
	}, text)

	// GitHub splits table cells before it parses code spans, so a pipe needs its escape even
	// inside one.
	if inTable {
		text = strings.ReplaceAll(text, "|", `\|`)
	}

	fence := strings.Repeat("`", longestBacktickRun(text)+1)

	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		text = " " + text + " "
	}

	return fence + text + fence
}

func longestBacktickRun(text string) int {
	longest, run := 0, 0

	for _, r := range text {
		if r != '`' {
			run = 0

			continue
		}

		run++
		longest = max(longest, run)
	}

	return longest
}

// NewMarkdownTable builds the markdown writer for a stream.
func NewMarkdownTable(w io.Writer, opts MarkdownTableOptions) *MarkdownTable {
	return &MarkdownTable{w: w, opts: opts}
}

// MarkdownTable writes a stream as one markdown table, a row per record under a header written
// with the first, then a summary alert and a footer. It is stateful, so one run uses one.
type MarkdownTable struct {
	w       io.Writer
	opts    MarkdownTableOptions
	started bool
	tally   tally
	models  []string
	usage   *jev.Usage
}

// MarkdownTableOptions fixes the columns a MarkdownTable writes.
type MarkdownTableOptions struct {
	// IDs are the question ids, one column each, in question order.
	IDs []string
	// ID adds an id column ahead of the answers, for a run that names its records with --id.
	ID bool
	// Gate adds a gate column, passed, failed or unsure per row, when the run carries an assertion.
	Gate bool
}

// Write writes one record as a row, after the header when it is the first.
func (m *MarkdownTable) Write(rec Record) error {
	var out strings.Builder

	if !m.started {
		m.started = true

		out.WriteString(m.head())
	}

	m.count(rec)

	var cells []string
	if m.opts.ID {
		cells = append(cells, codeSpan(cell(rec.ID), true))
	}

	for _, id := range m.opts.IDs {
		cells = append(cells, streamAnswer(rec, id))
	}

	if m.opts.Gate {
		cells = append(cells, gateText(rec))
	}

	failure := ""
	if rec.Failure != nil {
		failure = codeSpan(rec.Failure.Message, true)
	}

	out.WriteString(tableRow(append(cells, failure)...))

	_, err := io.WriteString(m.w, out.String())

	return err
}

// Finish writes the summary alert and the footer. complete is false for a run that was
// interrupted, which gets the footer and no summary. A stream with no records writes nothing.
func (m *MarkdownTable) Finish(complete bool) error {
	if !m.started {
		return nil
	}

	var out strings.Builder

	if complete {
		out.WriteString("\n" + m.tally.alert())
	}

	if footer := footerOf(m.models, m.usage); footer != "" {
		out.WriteString("\n" + footer)
	}

	_, err := io.WriteString(m.w, out.String())

	return err
}

func (m *MarkdownTable) head() string {
	var columns []string
	if m.opts.ID {
		columns = append(columns, "id")
	}

	for _, id := range m.opts.IDs {
		columns = append(columns, codeSpan(id, true))
	}

	if m.opts.Gate {
		columns = append(columns, "gate")
	}

	return tableHead(append(columns, "error")...)
}

func (m *MarkdownTable) count(rec Record) {
	m.tally.add(rec, m.opts.Gate)

	if rec.Model != "" && !slices.Contains(m.models, rec.Model) {
		m.models = append(m.models, rec.Model)
	}

	if rec.Usage == nil {
		return
	}

	if m.usage == nil {
		m.usage = &jev.Usage{}
	}

	m.usage.InputTokens += rec.Usage.InputTokens
	m.usage.OutputTokens += rec.Usage.OutputTokens

	if rec.Usage.Cost != nil {
		cost := *rec.Usage.Cost
		if m.usage.Cost != nil {
			cost += *m.usage.Cost
		}

		m.usage.Cost = &cost
	}
}

func streamAnswer(rec Record, id string) string {
	for _, named := range rec.Answers {
		if named.ID == id && named.Answer != nil {
			return answerText(named.Answer)
		}
	}

	return ""
}

func gateText(rec Record) string {
	switch {
	case rec.Failure != nil:
		return ""
	case rec.AssertFailed:
		return "failed"
	case rec.Abstained:
		return "unsure"
	default:
		return "passed"
	}
}

type tally struct {
	records, passed, failed, unsure, answered, unanswered int
}

func (t *tally) add(rec Record, gated bool) {
	t.records++

	switch {
	case rec.Failure != nil:
		t.unanswered++
	case !gated:
		t.answered++
	case rec.AssertFailed:
		t.failed++
	case rec.Abstained:
		t.unsure++
	default:
		t.passed++
	}
}

func (t *tally) alert() string {
	kind := "TIP"

	switch {
	case t.failed > 0 || t.unanswered > 0:
		kind = "CAUTION"
	case t.unsure > 0:
		kind = "WARNING"
	}

	var parts []string

	for _, part := range []struct {
		count int
		label string
	}{
		{t.passed, "passed"},
		{t.failed, "failed"},
		{t.unsure, "unsure"},
		{t.answered, "answered"},
		{t.unanswered, "with no answer"},
	} {
		if part.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", part.count, part.label))
		}
	}

	records := "records"
	if t.records == 1 {
		records = "record"
	}

	return alert(kind, fmt.Sprintf("%d %s: %s.", t.records, records, strings.Join(parts, ", ")))
}
