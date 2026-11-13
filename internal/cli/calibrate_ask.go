package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"math"
	"slices"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie-go"
	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
	"github.com/frodi-karlsson/onesie/internal/wording"
)

const (
	maxCauses = 5
	// costScale keeps ten decimal places of a summed cost, below any price a provider quotes per request.
	costScale = 1e10
)

func liveCalibration(
	cmd *cobra.Command, settings rootSettings, flags runConfig, calib calibrateFlags, inputMode input.Mode,
	inv *invocation, scope calibrateScope, bound []boundRequirement, base []float64,
) (err error) {
	chosen, err := connected(settings, flags)
	if err != nil {
		return err
	}

	model := chosen.modelFor(scope.scored.Model)

	cuts := cutsFor(base, bound, len(scope.scored.Questions))

	if calib.offline {
		if _, statErr := settings.stat(setting.Get(flags.merged, setting.Out)); errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("%s does not exist, so --offline has nothing to read. "+
				"Rerun without --offline to create it", setting.Get(flags.merged, setting.Out))
		}
	}

	out, _, err := openOut(settings, flags, calib.offline)

	defer func() {
		err = settings.laterFailures.keepFirst(err, out.Release())
	}()

	if err != nil {
		return err
	}

	// The labels and the cuts stay out, since neither changes an answer, and a later stream run
	// with -o json and the same questions reads the file as its own.
	inputs := answersfile.FingerprintInputs{
		Model: model, Output: output.JSON.String(), Input: inputMode.String(), Images: inv.images.fingerprint(),
		LongContext: inv.plan.LongContext,
	}

	asked, err := askedPlan(settings, flags, out, scope, inputs)
	if err != nil {
		return err
	}

	if askErr := prepareAsked(cmd, settings, flags, inv, asked, !calib.offline); askErr != nil {
		return askErr
	}

	answers, err := answersFor(cmd, settings, flags, asked, scope.full.Questions)
	if err != nil {
		return err
	}

	runErr := calibrateAnswers(cmd, settings, flags, calib, inputMode, inv, calibrateAsk{
		scope: scope, asked: asked, provider: chosen.provider, model: model, inputs: inputs, cuts: cuts, bound: bound,
		out: out, answers: answers, images: inv.images,
	})
	if out == nil || calib.offline {
		return runErr
	}

	return settings.laterFailures.keepFirst(runErr, out.Finish(runErr))
}

func askedPlan(
	settings rootSettings, flags runConfig, out *answersfile.File, scope calibrateScope, inputs answersfile.FingerprintInputs,
) (*plan.Plan, error) {
	if out == nil || !out.Resuming() || len(scope.skipped) == 0 {
		return scope.scored, nil
	}

	stored, found, err := out.StoredFingerprint()
	if err != nil || !found {
		return scope.scored, err
	}

	whole, err := outFingerprint(settings, flags, scope.full.Questions, inputs)
	if err != nil {
		return nil, err
	}

	if whole.Matches(stored) {
		return scope.full, nil
	}

	return scope.scored, nil
}

func prepareAsked(cmd *cobra.Command, settings rootSettings, flags runConfig, inv *invocation, asked *plan.Plan, online bool) error {
	if err := noteOverriddenBodyState(cmd.ErrOrStderr(), inv.loaded, inv.images, plan.WireAll(asked.Questions)); err != nil {
		return err
	}

	if !online {
		return nil
	}

	return checkProviderNeeds(settings, flags, asked, inv.images)
}

type calibrateAsk struct {
	scope    calibrateScope
	asked    *plan.Plan
	provider onesie.Provider
	model    string
	inputs   answersfile.FingerprintInputs
	cuts     [][]float64
	bound    []boundRequirement
	out      *answersfile.File
	answers  answererFactory
	images   *imaging
}

func calibrateAnswers(
	cmd *cobra.Command, settings rootSettings, flags runConfig, calib calibrateFlags, inputMode input.Mode,
	inv *invocation, run calibrateAsk,
) error {
	scored, out := run.scope.scored, run.out

	keptWhole := len(run.asked.Questions) > len(scored.Questions)
	if !keptWhole {
		if noteErr := writeSkipped(cmd.ErrOrStderr(), run.scope.skipped, false, ""); noteErr != nil {
			return noteErr
		}
	}

	set, err := readLabelled(cmd.Context(), settings, inputMode, flags, inv.mapper, inv.namer, run.scope.labels)
	if err != nil {
		return err
	}

	if len(set.records) == 0 {
		return errors.New("no record carries a label, so there is nothing to calibrate")
	}

	bindErr := bindOut(out, settings, flags, run.asked.Questions, run.inputs)
	if stale := (*answersfile.StaleAnswersError)(nil); calib.offline && errors.As(bindErr, &stale) {
		return &plainError{cause: bindErr, message: fmt.Sprintf("%s does not match this run, since %s, so "+
			"--offline cannot read it. Rerun without --offline and --resume to regenerate it", setting.Get(flags.merged, setting.Out), stale.Cause())}
	}

	if bindErr != nil {
		return bindErr
	}

	resumed, err := resumeLabelled(cmd.Context(), out, flags, inv.namer, scored, set)
	if err != nil {
		return err
	}

	if calib.offline && len(resumed.pending) > 0 {
		return unansweredOffline(setting.Get(flags.merged, setting.Out), resumed.pending)
	}

	if keptWhole {
		askedOf := ""
		if len(resumed.pending) > 0 {
			askedOf = setting.Get(flags.merged, setting.Out)
		}

		if noteErr := writeSkipped(cmd.ErrOrStderr(), run.scope.skipped, true, askedOf); noteErr != nil {
			return noteErr
		}
	}

	costErr := writeCost(cmd.ErrOrStderr(), set, resumed, setting.Get(flags.merged, setting.Out), len(run.asked.Questions), calib.offline)
	if costErr != nil {
		return costErr
	}

	return withStats(cmd, settings.now, flags, func(stats *collector) error {
		return reportLabelled(cmd, flags, calib, run, set, resumed, stats)
	})
}

func reportLabelled(
	cmd *cobra.Command,
	flags runConfig,
	calib calibrateFlags,
	run calibrateAsk,
	set labelledSet,
	resumed resumedSet,
	stats *collector,
) error {
	if resumed.book != nil {
		stats.skip(resumed.book.Skipped())
	}

	outcomes, result, err := askLabelled(cmd, flags, run, resumed, stats, calib.offline)
	if err != nil {
		return err
	}

	report := reportOf(run.scope.scored, set, outcomes, run.cuts)
	report.Asked = result.Records
	report.Stored = resumed.stored
	report.Mocked = flags.mockPath != ""
	report.Require = measureRequirements(report, run.bound)

	if setting.Get(flags.merged, setting.Usage) {
		report.Usage = usageOf(outcomes)
	}

	if writeErr := writeReport(cmd.OutOrStdout(), calib.report, report); writeErr != nil {
		return written(writeErr)
	}

	unmet, err := writeUnmet(cmd.ErrOrStderr(), report.Require)
	if err != nil {
		return err
	}

	if result.Failed > 0 {
		if causeErr := writeCauses(cmd.ErrOrStderr(), set, outcomes); causeErr != nil {
			return causeErr
		}

		return &recordsError{}
	}

	if unmet {
		return &failError{}
	}

	return nil
}

func unansweredOffline(answers string, pending []labelledRecord) error {
	if len(pending) == 1 {
		return fmt.Errorf("%s does not answer %s, so --offline cannot report it. "+
			"Rerun without --offline to ask it", answers, pending[0].name())
	}

	return fmt.Errorf("%s does not answer %s or %d more, so --offline cannot report them. "+
		"Rerun without --offline to ask them", answers, pending[0].name(), len(pending)-1)
}

func writeCost(
	w io.Writer, set labelledSet, resumed resumedSet, answers string, questions int, offline bool,
) error {
	line := fmt.Sprintf("asking %d of %s, %s each",
		len(resumed.pending), wording.Plural(len(set.records), "record", "records"), wording.Plural(questions, "question", "questions"))
	if offline {
		line = fmt.Sprintf("reading %s from %s", wording.Plural(len(set.records), "record", "records"), answers)
	}

	if set.unlabelled > 0 {
		line += fmt.Sprintf(", %d unlabelled skipped", set.unlabelled)
	}

	if answers != "" && !offline {
		line += fmt.Sprintf(", %d answered in %s", resumed.stored, answers)
	}

	return output.Notice(w, line)
}

func askLabelled(
	cmd *cobra.Command, flags runConfig, run calibrateAsk, resumed resumedSet, stats *collector, offline bool,
) ([]output.Record, engine.Result, error) {
	asker, err := answererWhenPending(cmd.Context(), resumed.pending, run.answers, stats)
	if err != nil {
		return nil, engine.Result{}, err
	}

	book := resumed.book
	labelled := &labelledRun{
		run: run, asker: asker, questions: plan.WireAll(run.asked.Questions), usage: setting.Get(flags.merged, setting.Usage), stats: stats,
		outcomes: slices.Clone(resumed.answered), book: book,
	}

	result, err := engine.Run(cmd.Context(), engine.Config[labelledRecord, askedLine]{
		Source:   &labelledSource{records: resumed.pending},
		Evaluate: labelled.evaluate,
		Write:    labelled.write,
		Jobs:     setting.Get(flags.merged, setting.Jobs),
		Abort:    aborting,
	})

	if result.Aborted {
		return nil, result, &abortError{cause: result.Cause}
	}

	if err != nil {
		return nil, result, err
	}

	// An interrupt landing as the last answer arrives still ends the run with no report.
	if ctxErr := cmd.Context().Err(); ctxErr != nil {
		return nil, result, ctxErr
	}

	// Only a run that asked every record rewrites the file. An interrupted one leaves the appended
	// lines as they are, for the next resume to finish.
	if book != nil && book.Complete() && !offline {
		run.out.CompactInto(book.TakeOrder(setting.Get(flags.merged, setting.Prune)), output.JSON)
	}

	return labelled.outcomes, result, nil
}

type labelledRun struct {
	run       calibrateAsk
	asker     answerer
	questions onesie.Questions
	usage     bool
	stats     *collector
	outcomes  []output.Record
	book      *answersfile.Ledger
	halted    bool
}

func (r *labelledRun) evaluate(ctx context.Context, rec labelledRecord) (askedLine, error) {
	asked := r.run.asked
	key := recordKey{position: rec.position, line: rec.line, id: rec.id}

	// At ask time rather than read time, since calibrate reads every record ahead and would
	// otherwise hold every image at once. rec.sent keeps the paths, which is what the worst
	// misses print.
	sent, assets, imageErr := r.run.images.attach(rec.sent)
	if imageErr != nil {
		r.stats.recordFailure(imageErr, false, 0)

		failed := failureRecord(asked, imageErr)
		failed.ID = rec.id

		return askedLine{index: rec.index, slot: rec.slot, record: failed}, imageErr
	}

	record, _, err := askAndRecord(ctx, asking{
		provider: r.run.provider, asker: r.asker, built: asked, model: r.run.model, questions: r.questions,
		withUsage: r.usage, stats: r.stats,
	}, key, sent, assets)
	record.ID = rec.id

	return askedLine{index: rec.index, slot: rec.slot, record: record, halted: halting(err)}, err
}

func (r *labelledRun) write(l askedLine) error {
	// Nothing from the first record a mock file does not answer on reaches the file or the
	// ledger, so the file holds only the records before it and a resume asks the rest.
	if r.halted || l.halted {
		r.halted = true

		return nil
	}

	r.outcomes[l.index] = l.record

	out := r.run.out
	if out == nil {
		return nil
	}

	start := out.Offset()
	err := output.Write(out, output.JSON, l.record, false)

	if r.book != nil {
		r.book.Wrote(l.slot, answersfile.Span{Start: start, End: out.Offset()})
	}

	return err
}

func answererWhenPending(
	ctx context.Context, pending []labelledRecord, answerers answererFactory, stats *collector,
) (answerer, error) {
	if len(pending) == 0 {
		return nil, nil
	}

	return answerers(ctx, stats)
}

func (s *labelledSource) Next() (labelledRecord, bool, error) {
	if s.next >= len(s.records) {
		return labelledRecord{}, false, nil
	}

	rec := s.records[s.next]
	s.next++

	return rec, true, nil
}

type labelledSource struct {
	records []labelledRecord
	next    int
}

type askedLine struct {
	index  int
	slot   int
	record output.Record
	halted bool
}

func reportOf(built *plan.Plan, set labelledSet, outcomes []output.Record, cuts [][]float64) calibrate.Report {
	report := calibrate.Report{
		Records:    set.total,
		Labelled:   len(set.records),
		Unlabelled: set.unlabelled,
	}

	noul := make([][]calibrate.NoulCase, len(built.Questions))
	choices := make([][]calibrate.ChoiceCase, len(built.Questions))
	failed := make([]int, len(built.Questions))
	models := map[string]struct{}{}

	for i, rec := range set.records {
		cases, err := casesOf(built, rec, outcomes[i])
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
			case built.Questions[q].Shape == plan.Noul:
				noul[q] = append(noul[q], c.noul)
			default:
				choices[q] = append(choices[q], c.choice)
			}
		}
	}

	report.Models = slices.Sorted(maps.Keys(models))

	for q, question := range built.Questions {
		scored := calibrate.QuestionReport{ID: question.ID, Shape: question.Shape}

		switch question.Shape {
		case plan.Noul:
			score := calibrate.ScoreNoul(noul[q], failed[q], cuts[q])
			scored.Noul = &score
		case plan.Pick:
			score := calibrate.ScorePick(choices[q], namesOf(question), failed[q], cuts[q])
			scored.Pick = &score
		case plan.Rate:
			score := calibrate.ScoreRate(choices[q], namesOf(question), failed[q], cuts[q])
			scored.Rate = &score
		}

		report.Questions = append(report.Questions, scored)
	}

	return report
}

func casesOf(built *plan.Plan, rec labelledRecord, record output.Record) ([]recordCase, error) {
	if record.Failure != nil {
		return nil, errors.New(record.Failure.Message)
	}

	cases := make([]recordCase, len(built.Questions))

	for q, question := range built.Questions {
		if rec.labels[q] == nil {
			continue
		}

		scored, err := caseOf(question, rec.labels[q], rec, record)
		if err != nil {
			return nil, err
		}

		cases[q] = scored
	}

	return cases, nil
}

func caseOf(question plan.Question, label *calibrate.Label, rec labelledRecord, record output.Record) (recordCase, error) {
	name := ""
	if rec.id != nil {
		name = jq.IDText(rec.id)
	}

	given, err := answerTo(question, record)
	if err != nil {
		return recordCase{}, err
	}

	if given.Value == nil {
		return recordCase{}, fmt.Errorf("question '%s' has an answer with no value", question.ID)
	}

	if question.Shape == plan.Noul {
		value, isNumber := given.Value.(float64)
		if !isNumber || !unitRange(value) {
			return recordCase{}, fmt.Errorf("question '%s' answered %v, which lies outside [0,1]", question.ID, given.Value)
		}

		return recordCase{noul: calibrate.NoulCase{
			Name: name, ID: rec.id, Line: rec.line, Yes: label.Yes, Value: value, Text: sentText(rec.sent),
		}}, nil
	}

	picked, isName := given.Value.(string)
	if !isName {
		return recordCase{}, fmt.Errorf("question '%s' picked %v, which is not a name", question.ID, given.Value)
	}

	if given.Confidence == nil || !unitRange(*given.Confidence) {
		return recordCase{}, fmt.Errorf("question '%s' answered with a confidence outside [0,1]", question.ID)
	}

	return recordCase{choice: calibrate.ChoiceCase{
		Name: name, Label: label.Name, Picked: picked, ID: rec.id, Line: rec.line,
		Confidence: *given.Confidence, Text: sentText(rec.sent),
	}}, nil
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
	noul   calibrate.NoulCase
	choice calibrate.ChoiceCase
}

func writeCauses(w io.Writer, set labelledSet, outcomes []output.Record) error {
	listed, more := 0, 0

	for i, rec := range set.records {
		failure := outcomes[i].Failure
		if failure == nil {
			continue
		}

		if listed == maxCauses {
			more++

			continue
		}

		listed++

		if err := output.Notice(w, rec.name()+": "+failure.Message); err != nil {
			return err
		}
	}

	if more == 0 {
		return nil
	}

	return output.Notice(w, "and "+wording.Plural(more, "more error", "more errors"))
}

func writeReport(w io.Writer, format string, report calibrate.Report) error {
	if format == "json" {
		return calibrate.WriteJSON(w, report)
	}

	return calibrate.WriteTable(w, report)
}

func usageOf(outcomes []output.Record) *calibrate.Usage {
	total := &calibrate.Usage{}

	for _, record := range outcomes {
		if record.Usage == nil {
			continue
		}

		total.Records++
		total.InputTokens += record.Usage.InputTokens
		total.OutputTokens += record.Usage.OutputTokens

		if record.Usage.Cost != nil {
			cost := *record.Usage.Cost
			if total.Cost != nil {
				cost += *total.Cost
			}

			total.Cost = &cost
		}
	}

	// A sum of many small costs gathers float noise in its last places, which a report would print.
	if total.Cost != nil {
		rounded := math.Round(*total.Cost*costScale) / costScale
		total.Cost = &rounded
	}

	return total
}
