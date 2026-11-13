package output

import (
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/num"
	"github.com/frodi-karlsson/onesie/internal/wording"
)

// WriteMarkdown writes one record as a GitHub flavoured markdown table under an alert for its
// verdict, with the model and usage as a footer.
func WriteMarkdown(w io.Writer, rec Record, opts MarkdownOptions) error {
	var sections []string

	switch {
	case rec.Verdict() == Error:
		sections = append(sections, alert("CAUTION", "**Error:** "+codeSpan(rec.Failure.Message, false)))
	case opts.Assert != "":
		sections = append(sections, verdictAlert(rec, opts))
	}

	// A failed record keeps its table only for the fallbacks it carries, which are the answers a
	// shell guard reads.
	if rec.Failure == nil || anyAnswer(rec.Answers) {
		sections = append(sections, answerTable(rec.Answers))
	}

	if footer := footerOf(modelsOf(rec.Model), rec.Usage); footer != "" {
		sections = append(sections, footer)
	}

	_, err := io.WriteString(w, strings.Join(sections, "\n"))

	return err
}

// MarkdownOptions carries what a markdown record says beyond the record itself.
type MarkdownOptions struct {
	// Assert is the gate's source. Empty means the run has no gate and the record shows no verdict.
	Assert string
	// Abstain is the --abstain-if source, quoted when a record abstained.
	Abstain string
}

func verdictAlert(rec Record, opts MarkdownOptions) string {
	gate := codeSpan(opts.Assert, false)

	switch rec.Verdict() {
	case Fail:
		return alert("CAUTION", "**Fail:** "+gate+" did not hold.")
	case Unsure:
		return alert("WARNING", "**Unsure:** "+gate+" did not hold and "+
			codeSpan(opts.Abstain, false)+" did, so a person should decide.")
	default:
		return alert("TIP", "**Pass:** "+gate+" held.")
	}
}

func anyAnswer(answers []Named) bool {
	return slices.ContainsFunc(answers, func(named Named) bool { return named.Answer != nil })
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

func modelsOf(model string) []string {
	if model == "" {
		return nil
	}

	return []string{model}
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
	usage   *onesie.Usage
}

// MarkdownTableOptions fixes the columns a MarkdownTable writes.
type MarkdownTableOptions struct {
	// IDs are the question ids, one column each, in question order.
	IDs []string
	// ID adds an id column ahead of the answers, for a run that names its records with --id.
	ID bool
	// Verdict adds a verdict column, pass, fail, unsure or error per row, when the run is gated.
	Verdict bool
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
		cells = append(cells, idCell(rec.ID))
	}

	for _, id := range m.opts.IDs {
		cells = append(cells, streamAnswer(rec, id))
	}

	if m.opts.Verdict {
		cells = append(cells, rec.Verdict().String())
	}

	failure := ""
	if rec.Failure != nil {
		failure = codeSpan(rec.Failure.Message, true)
	}

	out.WriteString(tableRow(append(cells, failure)...))

	_, err := io.WriteString(m.w, out.String())

	return err
}

// Finish writes the summary alert and the footer. whole is false for a run that ended early, by
// an interrupt, an abort or a stop flag, and the summary then says it stopped.
func (m *MarkdownTable) Finish(whole bool) error {
	var out strings.Builder

	// Written even for no records, so a comment built from the output is never empty.
	if m.started {
		out.WriteString("\n")
	}

	out.WriteString(m.tally.alert(whole))

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

	if m.opts.Verdict {
		columns = append(columns, "verdict")
	}

	return tableHead(append(columns, "error")...)
}

func (m *MarkdownTable) count(rec Record) {
	m.tally.add(rec)

	if rec.Model != "" && !slices.Contains(m.models, rec.Model) {
		m.models = append(m.models, rec.Model)
	}

	if rec.Usage == nil {
		return
	}

	if m.usage == nil {
		m.usage = &onesie.Usage{}
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

func idCell(id any) string {
	// A record that failed before its id was read has none, which differs from an empty id.
	if id == nil {
		return ""
	}

	return codeSpan(jq.IDText(id), true)
}

func streamAnswer(rec Record, id string) string {
	for _, named := range rec.Answers {
		if named.ID == id && named.Answer != nil {
			return answerText(named.Answer)
		}
	}

	return ""
}

type tally struct {
	records int
	counts  Tally
}

func (t *tally) add(rec Record) {
	t.records++
	t.counts[rec.Verdict()]++
}

func (t *tally) alert(whole bool) string {
	kind := "TIP"

	switch {
	case t.counts[Fail] > 0 || t.counts[Error] > 0:
		kind = "CAUTION"
	case t.counts[Unsure] > 0:
		kind = "WARNING"
	}

	var parts []string

	for _, verdict := range []Verdict{Pass, Fail, Unsure, Answered, Error} {
		if count := t.counts[verdict]; count > 0 {
			parts = append(parts, Count(verdict, count))
		}
	}

	counted := wording.Plural(t.records, "record", "records")

	if !whole {
		counted = "stopped after " + counted
	}

	if len(parts) == 0 {
		return alert(kind, counted+".")
	}

	return alert(kind, counted+": "+strings.Join(parts, ", ")+".")
}

func alert(kind, text string) string {
	return "> [!" + kind + "]\n> " + text + "\n"
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

	if edges, isBox := a.Box(); isBox {
		return fmt.Sprintf("left %s, top %s, right %s, bottom %s",
			probabilityText(edges.Left), probabilityText(edges.Top),
			probabilityText(edges.Right), probabilityText(edges.Bottom))
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
	return num.Format(probability)
}

func confidenceText(a *Answer) string {
	// A fallback replaced the answer the confidence belongs to.
	if a.Confidence == nil || a.Fallback != "" {
		return ""
	}

	return num.Format(*a.Confidence)
}

func footerOf(models []string, usage *onesie.Usage) string {
	parts := make([]string, 0, len(models)+2)
	for _, model := range models {
		parts = append(parts, codeSpan(model, false))
	}

	if usage != nil {
		parts = append(parts, fmt.Sprintf("%d in, %d out tokens", usage.InputTokens, usage.OutputTokens))

		if usage.Cost != nil {
			parts = append(parts, Cost(*usage.Cost))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	return "_" + strings.Join(parts, ", ") + "_\n"
}

func codeSpan(text string, inTable bool) string {
	if text == "" {
		return "_empty_"
	}

	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}

		return r
	}, text)

	// A GFM compliant renderer splits a cell at a pipe after a backslash even inside a code
	// span, so a cell holding one leaves markdown for html, where no pipe survives as a pipe.
	if inTable && strings.ContainsRune(text, '|') {
		return htmlCode(text)
	}

	fence := strings.Repeat("`", longestBacktickRun(text)+1)

	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") || spaceBound(text) {
		text = " " + text + " "
	}

	return fence + text + fence
}

func htmlCode(text string) string {
	var b strings.Builder

	b.WriteString("<code>")

	// Every ASCII punctuation mark, not only the html ones, since markdown still parses the text
	// between html tags and would read an asterisk or a backslash in it as markup.
	for _, r := range text {
		if r <= unicode.MaxASCII && (unicode.IsPunct(r) || unicode.IsSymbol(r)) {
			fmt.Fprintf(&b, "&#%d;", r)

			continue
		}

		b.WriteRune(r)
	}

	b.WriteString("</code>")

	return b.String()
}

func spaceBound(text string) bool {
	// CommonMark strips one space from each end of a span that has both, unless it is all spaces,
	// and it counts only U+0020 as a space, so a non breaking space between two is stripped too.
	return strings.HasPrefix(text, " ") && strings.HasSuffix(text, " ") && strings.Trim(text, " ") != ""
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

// Cost writes a cost in dollars, rounded to the hundred millionth, which keeps a provider's float
// noise out of a line meant for people.
func Cost(dollars float64) string {
	return "$" + strconv.FormatFloat(math.Round(dollars*1e8)/1e8, 'f', -1, 64)
}
