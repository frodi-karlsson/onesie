package calibrate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/planbuild"
	"github.com/frodi-karlsson/onesie/internal/runner"
	"github.com/frodi-karlsson/onesie/internal/stored"
)

// ScopeOf splits a plan into the questions a label scores and the ones it skips, and compiles each
// label. specs are the label settings, and speller names them in a refusal.
func ScopeOf(built *plan.Plan, specs []string, speller plan.Speller) (Scope, error) {
	ids := make([]string, 0, len(built.Questions))
	for _, question := range built.Questions {
		ids = append(ids, question.ID)
	}

	labelSetting := speller.Spell("--label")
	exprs := make([]*jq.Expr, len(ids))

	for _, spec := range specs {
		id, source, err := SplitLabel(spec, ids, speller)
		if err != nil {
			return Scope{}, err
		}

		index := slices.Index(ids, id)
		if exprs[index] != nil {
			return Scope{}, fmt.Errorf("%s is given twice for question '%s'", labelSetting, id)
		}

		exprs[index], err = planbuild.Expr(labelSetting+" "+id, source)
		if err != nil {
			return Scope{}, err
		}
	}

	scored := *built
	scored.Questions = nil
	scope := Scope{Full: built, Scored: &scored}

	for index, expr := range exprs {
		question := built.Questions[index]
		if expr == nil {
			scope.Skipped = append(scope.Skipped, question.ID)

			continue
		}

		if question.Shape == plan.Box {
			return Scope{}, fmt.Errorf(
				"question '%s' is a box, and a box cannot be calibrated. Drop its %s", question.ID, labelSetting)
		}

		scored.Questions = append(scored.Questions, question)
		scope.Labels = append(scope.Labels, QuestionLabel{
			ID: question.ID, Shape: question.Shape, Names: namesOf(question), Expr: expr,
		})
	}

	if len(scope.Labels) == 0 {
		return Scope{}, fmt.Errorf("question '%s' has no %s, so asking it would cost "+
			"requests and report nothing. Pass %s %s=EXPR", ids[0], labelSetting, labelSetting, ids[0])
	}

	return scope, nil
}

// Scope is a plan split by its labels. Scored holds the labelled questions, and Skipped names the rest.
type Scope struct {
	Full, Scored *plan.Plan
	Skipped      []string
	Labels       []QuestionLabel
}

// QuestionLabel is the compiled label of one scored question, with the names its shape accepts.
type QuestionLabel struct {
	ID    string
	Shape plan.Shape
	Names []string
	Expr  *jq.Expr
}

func namesOf(question plan.Question) []string {
	names := make([]string, 0, len(question.Options)+len(question.Levels))
	for _, option := range question.Options {
		names = append(names, option.Name)
	}

	for _, level := range question.Levels {
		names = append(names, level.Label)
	}

	return names
}

// ReadLabelled reads every record of the input and its labels. A record that cannot be read comes
// back as a ReadError, and an interrupt as the context's own error.
func ReadLabelled(ctx context.Context, config Config, labels []QuestionLabel) (LabelledSet, error) {
	// Ended on every return, so the goroutine reading the input stops waiting to hand over a record.
	ctx, stop := context.WithCancel(ctx)
	defer stop()

	stream := input.NewStream(config.Input, config.InputMode, input.StreamOptions{
		SkipBlank: config.SkipBlank, Speller: config.Speller, Delimiter: config.Delimiter,
	})
	next := readAhead(ctx, stream)
	reader := &labelReader{
		mapper: config.Mapper, namer: config.Namer, labels: labels, speller: config.Speller,
		seen: map[[sha256.Size]byte]int{},
	}

	var set LabelledSet

	for {
		var pulled pulledRecord

		select {
		case pulled = <-next:
		case <-ctx.Done():
			return LabelledSet{}, ctx.Err()
		}

		rec, ok, err := pulled.rec, pulled.ok, pulled.err
		if err != nil {
			return LabelledSet{}, err
		}

		// An interrupt can land as the input ends, and the set read so far is not the whole input.
		if !ok {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return LabelledSet{}, ctxErr
			}

			return set, nil
		}

		set.Total++
		if set.Total > limits.MaxCalibrateRecords {
			return LabelledSet{}, fmt.Errorf("calibrate reads at most %d records, and %s lists the cap",
				limits.MaxCalibrateRecords, config.Speller.Spell("-V"))
		}

		labelled, found, err := reader.read(ctx, rec)
		if err != nil {
			// An interrupt reaches the expressions as their own failure, and it has to exit 130
			// rather than as the bad record it would otherwise read as.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return LabelledSet{}, ctxErr
			}

			return LabelledSet{}, &ReadError{Err: err, Read: set.Total, Header: rec.Header, Comma: stream.Comma()}
		}

		if !found {
			set.Unlabelled++
			set.inputs = append(set.inputs, inputEntry{id: labelled.id, record: -1})

			continue
		}

		labelled.index = len(set.Records)
		set.inputs = append(set.inputs, inputEntry{id: labelled.id, record: labelled.index})
		set.Records = append(set.Records, labelled)
	}
}

// Config is what a calibration reads its records from and asks them with. Ask carries the asked
// plan, the model, the usage switch and the words a failure carries. A nil Images attaches none.
type Config struct {
	Input     io.Reader
	InputMode input.Mode
	SkipBlank bool
	Delimiter input.Delimiter
	Mapper    *jq.Expr
	Namer     *jq.Expr
	Images    runner.Images
	Ask       runner.Ask
	Jobs      int
	Prune     bool
	Offline   bool
	Speller   plan.Speller
}

// Error returns the text of the cause.
func (e *ReadError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the cause.
func (e *ReadError) Unwrap() error {
	return e.Err
}

// ReadError is a record that could not be read, after Read records, with the header of the record it
// failed on and the comma it split on.
type ReadError struct {
	Err    error
	Read   int
	Header []string
	Comma  rune
}

func readAhead(ctx context.Context, stream *input.Stream) <-chan pulledRecord {
	next := make(chan pulledRecord)

	// Read on its own goroutine so an interrupt ends a read with no deadline, such as a terminal. A
	// read under way cannot be interrupted, so this goroutine outlives the read until it returns.
	go func() {
		for {
			rec, ok, err := stream.Next()

			select {
			case next <- pulledRecord{rec: rec, ok: ok, err: err}:
			case <-ctx.Done():
				return
			}

			if !ok || err != nil {
				return
			}
		}
	}()

	return next
}

func (r *labelReader) read(ctx context.Context, rec input.Record) (LabelledRecord, bool, error) {
	if rec.Err != nil {
		return LabelledRecord{}, false, rec.Err
	}

	labelled := LabelledRecord{position: rec.Index + 1, line: rec.Line}

	if r.namer != nil {
		id, err := r.uniqueID(ctx, rec)
		if err != nil {
			return LabelledRecord{}, false, &input.LineError{Line: rec.Line, Err: err}
		}

		labelled.id = id
	}

	value := runner.JQValue(rec.State, rec.Wire)
	labelled.labels = make([]*Label, len(r.labels))
	found := false

	for i, label := range r.labels {
		parsed, err := labelOf(ctx, label, value)
		if err != nil {
			return LabelledRecord{}, false, fmt.Errorf("%s %s: %s: %w",
				r.speller.Spell("--label"), label.ID, labelled.Name(), err)
		}

		labelled.labels[i] = parsed
		found = found || parsed != nil
	}

	sent, err := runner.Mapped(ctx, r.mapper, rec.State, rec.Wire, r.speller)
	if err != nil {
		return LabelledRecord{}, false, &input.LineError{Line: rec.Line, Err: err}
	}

	// Cloned, so the record holds its own bytes and not the slack of the buffer they were encoded in.
	// calibrate refuses to run without --map, and a mapped state is always encoded.
	if raw, isRaw := sent.(json.RawMessage); isRaw {
		sent = json.RawMessage(bytes.Clone(raw))
	}

	labelled.sent = sent

	return labelled, found, nil
}

func (r *labelReader) uniqueID(ctx context.Context, rec input.Record) (any, error) {
	id, err := runner.IDOf(ctx, r.namer, rec, r.speller)
	if err != nil {
		return nil, err
	}

	text := jq.IDText(id)

	if unwritable := runner.UnwritableID(output.JSON, text, r.speller); unwritable != nil {
		return nil, unwritable
	}

	key := sha256.Sum256([]byte(text))
	if first, taken := r.seen[key]; taken {
		return nil, fmt.Errorf("%s: '%s' is also the id of line %d", r.speller.Spell("--id"), text, first)
	}

	r.seen[key] = rec.Line

	return id, nil
}

func labelOf(ctx context.Context, label QuestionLabel, value any) (*Label, error) {
	result, err := label.Expr.One(ctx, value)
	if errors.Is(err, jq.ErrNoValue) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	parsed, labelled, err := ParseLabel(label.Shape, label.Names, result)
	if err != nil || !labelled {
		return nil, err
	}

	return &parsed, nil
}

type pulledRecord struct {
	rec input.Record
	ok  bool
	err error
}

type labelReader struct {
	mapper  *jq.Expr
	namer   *jq.Expr
	labels  []QuestionLabel
	speller plan.Speller
	seen    map[[sha256.Size]byte]int
}

// LabelledSet is the labelled records of an input, in input order, with the counts of every record
// read and of the ones no label held.
type LabelledSet struct {
	Records    []LabelledRecord
	Unlabelled int
	Total      int
	inputs     []inputEntry
}

type inputEntry struct {
	id     any
	record int
}

// LabelledRecord is one record a label held, with the state it sends and its labels.
type LabelledRecord struct {
	index    int
	slot     int
	position int
	line     int
	id       any
	sent     any
	labels   []*Label
}

// Line is the input line the record starts on.
func (r LabelledRecord) Line() int {
	return r.line
}

// Sent is the state the record sends, before any image is attached.
func (r LabelledRecord) Sent() any {
	return r.sent
}

// Name names the record by its id, or by its line without one.
func (r LabelledRecord) Name() string {
	if r.id == nil {
		return fmt.Sprintf("line %d", r.line)
	}

	return "record " + jq.IDText(r.id)
}

// ResumeLabelled reads back the answers out already holds for set, and leaves pending the records it
// still has to ask. A stored answer the scorers cannot use is asked again.
func ResumeLabelled(
	ctx context.Context, out *answersfile.File, format answersfile.Format, scored *plan.Plan, set LabelledSet,
) (ResumedSet, error) {
	resumed := ResumedSet{Pending: set.Records, Answered: make([]output.Record, len(set.Records))}

	book, err := answersfile.ResumeLedger(ctx, out, format)
	if err != nil || book == nil {
		return resumed, err
	}

	var found []storedLine

	for i, rec := range set.Records {
		if at, ok := book.StoredAt(rec.id); ok {
			found = append(found, storedLine{index: i, at: at})
		}
	}

	if readErr := storedAnswers(ctx, out, found, scored); readErr != nil {
		return ResumedSet{}, readErr
	}

	for _, stored := range found {
		rec := set.Records[stored.index]
		stored.record.ID = rec.id

		// A line the scorers cannot use answers nothing, so its record is asked again, as a stored
		// error line is, and the report and the exit code count only the new outcome.
		if _, caseErr := casesOf(scored, rec, stored.record); caseErr != nil {
			book.Forget(rec.id)

			continue
		}

		resumed.Answered[stored.index] = stored.record
	}

	resumed.Book = book
	resumed.Pending = nil

	for _, entry := range set.inputs {
		if entry.record < 0 {
			book.Keep(entry.id)

			continue
		}

		rec := &set.Records[entry.record]
		slot, _, known := book.Admit(rec.id)
		rec.slot = slot

		if known {
			resumed.Stored++
		} else {
			resumed.Pending = append(resumed.Pending, *rec)
		}
	}

	book.End()

	return resumed, nil
}

// ResumedSet is a labelled set after a resume. Answered holds the stored answers by record, Pending
// the records still to ask, and Stored counts the ones the file answered.
type ResumedSet struct {
	Book     *answersfile.Ledger
	Pending  []LabelledRecord
	Answered []output.Record
	Stored   int
}

type storedLine struct {
	index  int
	at     answersfile.Span
	record output.Record
}

func storedAnswers(ctx context.Context, out *answersfile.File, lines []storedLine, built *plan.Plan) error {
	spans := make([]answersfile.Span, len(lines))
	for i, stored := range lines {
		spans[i] = stored.at
	}

	return out.ReadSpans(ctx, spans, func(i int, text []byte) {
		lines[i].record = stored.Record(built, text)
	})
}

// AskLabelled asks the pending records of set and writes each answer to out. It returns every
// record's outcome by position. An aborted run returns its result with the cause as the error.
func AskLabelled(
	ctx context.Context, config Config, out *answersfile.File, set ResumedSet, answerers runner.AnswererFactory,
	stats *runner.Collector,
) ([]output.Record, engine.Result, error) {
	answerer, err := answererWhenPending(ctx, set.Pending, answerers, stats)
	if err != nil {
		return nil, engine.Result{}, err
	}

	labelled := &labelledRun{
		answerer: answerer, ask: config.Ask, images: config.Images, out: out, stats: stats,
		outcomes: slices.Clone(set.Answered), book: set.Book,
	}

	result, err := engine.Run(ctx, engine.Config[LabelledRecord, askedLine]{
		Source:   &labelledSource{records: set.Pending},
		Evaluate: labelled.evaluate,
		Write:    labelled.write,
		Jobs:     config.Jobs,
		Abort:    runner.Aborting,
	})

	if result.Aborted {
		return nil, result, result.Cause
	}

	if err != nil {
		return nil, result, err
	}

	// An interrupt landing as the last answer arrives still ends the run with no report.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, result, ctxErr
	}

	// Only a run that asked every record rewrites the file. An interrupted one leaves the appended
	// lines as they are, for the next resume to finish.
	if set.Book != nil && set.Book.Complete() && !config.Offline {
		out.CompactInto(set.Book.TakeOrder(config.Prune), output.JSON)
	}

	return labelled.outcomes, result, nil
}

type labelledRun struct {
	answerer runner.Answerer
	ask      runner.Ask
	images   runner.Images
	out      *answersfile.File
	stats    *runner.Collector
	outcomes []output.Record
	book     *answersfile.Ledger
	halted   bool
}

func (r *labelledRun) evaluate(ctx context.Context, rec LabelledRecord) (askedLine, error) {
	key := runner.RecordKey{Position: rec.position, Line: rec.line, ID: rec.id}

	// At ask time rather than read time, since calibrate reads every record ahead and would
	// otherwise hold every image at once. rec.sent keeps the paths, which is what the worst
	// misses print.
	sent, assets, imageErr := r.attach(rec.sent)
	if imageErr != nil {
		bad := &input.LineError{Line: rec.line, Err: imageErr}
		r.stats.RecordFailure(bad, false, 0)

		failed := r.ask.Failure(bad)
		failed.ID = rec.id

		return askedLine{index: rec.index, slot: rec.slot, record: failed}, bad
	}

	record, err := r.ask.Record(ctx, r.answerer, key, sent, assets, r.stats)
	record.ID = rec.id

	return askedLine{index: rec.index, slot: rec.slot, record: record, halted: runner.Halting(err)}, err
}

func (r *labelledRun) attach(state any) (any, onesie.Assets, error) {
	if r.images == nil {
		return state, nil, nil
	}

	return r.images.Attach(state)
}

func (r *labelledRun) write(l askedLine) error {
	// Nothing from the first record a mock file does not answer on reaches the file or the
	// ledger, so the file holds only the records before it and a resume asks the rest.
	if r.halted || l.halted {
		r.halted = true

		return nil
	}

	r.outcomes[l.index] = l.record

	if r.out == nil {
		return nil
	}

	start := r.out.Offset()
	err := output.Write(r.out, output.JSON, l.record)

	if r.book != nil {
		r.book.Wrote(l.slot, answersfile.Span{Start: start, End: r.out.Offset()})
	}

	return err
}

func answererWhenPending(
	ctx context.Context, pending []LabelledRecord, answerers runner.AnswererFactory, stats *runner.Collector,
) (runner.Answerer, error) {
	if len(pending) == 0 {
		return nil, nil
	}

	return answerers(ctx, stats)
}

func (s *labelledSource) Next() (LabelledRecord, bool, error) {
	if s.next >= len(s.records) {
		return LabelledRecord{}, false, nil
	}

	rec := s.records[s.next]
	s.next++

	return rec, true, nil
}

type labelledSource struct {
	records []LabelledRecord
	next    int
}

type askedLine struct {
	index  int
	slot   int
	record output.Record
	halted bool
}

// ReportOf scores the outcomes of a labelled set against its labels, at the cuts of each question.
func ReportOf(scored *plan.Plan, set LabelledSet, outcomes []output.Record, cuts [][]float64) Report {
	report := Report{
		Records:    set.Total,
		Labelled:   len(set.Records),
		Unlabelled: set.Unlabelled,
	}

	yesNo := make([][]YesNoCase, len(scored.Questions))
	choices := make([][]ChoiceCase, len(scored.Questions))
	failed := make([]int, len(scored.Questions))
	models := map[string]struct{}{}

	for i, rec := range set.Records {
		cases, err := casesOf(scored, rec, outcomes[i])
		if err != nil {
			report.Failed++

			for q, label := range rec.labels {
				if label != nil {
					failed[q]++
				}
			}

			continue
		}

		models[outcomes[i].Model] = struct{}{}

		for q, c := range cases {
			switch {
			case rec.labels[q] == nil:
			case scored.Questions[q].Shape == plan.Noul:
				yesNo[q] = append(yesNo[q], c.yesNo)
			default:
				choices[q] = append(choices[q], c.choice)
			}
		}
	}

	report.Models = slices.Sorted(maps.Keys(models))

	for q, question := range scored.Questions {
		scoredQuestion := QuestionReport{ID: question.ID, Shape: question.Shape}

		switch question.Shape {
		case plan.Noul:
			score := ScoreYesNo(yesNo[q], failed[q], cuts[q])
			scoredQuestion.YesNo = &score
		case plan.Pick:
			score := ScorePick(choices[q], namesOf(question), failed[q], cuts[q])
			scoredQuestion.Pick = &score
		case plan.Rate:
			score := ScoreRate(choices[q], namesOf(question), failed[q], cuts[q])
			scoredQuestion.Rate = &score
		}

		report.Questions = append(report.Questions, scoredQuestion)
	}

	return report
}

func casesOf(built *plan.Plan, rec LabelledRecord, record output.Record) ([]recordCase, error) {
	if record.Failure != nil {
		return nil, errors.New(record.Failure.Message)
	}

	name := ""
	if rec.id != nil {
		name = jq.IDText(rec.id)
	}

	cases := make([]recordCase, len(built.Questions))
	text := sentText(rec.sent)

	for q, question := range built.Questions {
		if rec.labels[q] == nil {
			continue
		}

		given, err := answerTo(question, record)
		if err != nil {
			return nil, err
		}

		if question.Shape == plan.Noul {
			value, isNumber := given.Value.(float64)
			if !isNumber || !unitRange(value) {
				return nil, fmt.Errorf("question '%s' answered %v, which lies outside [0,1]", question.ID, given.Value)
			}

			cases[q].yesNo = YesNoCase{
				Name: name, ID: rec.id, Line: rec.line, Yes: rec.labels[q].Yes, Value: value,
				Text: text,
			}

			continue
		}

		picked, isName := given.Value.(string)
		if !isName {
			return nil, fmt.Errorf("question '%s' picked %v, which is not a name", question.ID, given.Value)
		}

		if given.Confidence == nil || !unitRange(*given.Confidence) {
			return nil, fmt.Errorf("question '%s' answered with a confidence outside [0,1]", question.ID)
		}

		cases[q].choice = ChoiceCase{
			Name: name, Label: rec.labels[q].Name, Picked: picked, ID: rec.id, Line: rec.line,
			Confidence: *given.Confidence, Text: text,
		}
	}

	return cases, nil
}

func sentText(sent any) string {
	raw, isRaw := sent.(json.RawMessage)
	if !isRaw {
		if text, isText := sent.(string); isText {
			return text
		}

		return ""
	}

	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}

	return string(raw)
}

func answerTo(question plan.Question, record output.Record) (*answer.Answer, error) {
	for _, named := range record.Answers {
		if named.ID == question.ID && named.Answer != nil {
			return named.Answer, nil
		}
	}

	return nil, fmt.Errorf("question '%s' has no answer", question.ID)
}

func unitRange(value float64) bool {
	// NaN fails both comparisons, so it is refused with the values outside the range.
	return value >= 0 && value <= 1
}

type recordCase struct {
	yesNo  YesNoCase
	choice ChoiceCase
}
