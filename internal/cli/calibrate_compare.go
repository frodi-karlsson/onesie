package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
	"github.com/frodi-karlsson/onesie/internal/wording"
)

func compareCalibration(
	cmd *cobra.Command, settings rootSettings, flags runConfig, calib calibrateFlags, inputMode input.Mode,
	inv *invocation, scope calibrateScope, bound []boundRequirement, args setting.CalibrationArguments,
) error {
	questions := scope.scored.Questions

	if refuseErr := refuseUncompared(questions, bound); refuseErr != nil {
		return refuseErr
	}

	if noteErr := writeUncompared(cmd.ErrOrStderr(), scope.skipped, questions); noteErr != nil {
		return noteErr
	}

	set, err := readLabelled(cmd.Context(), settings, inputMode, flags, inv.mapper, inv.namer, scope.labels)
	if err != nil {
		return err
	}

	if len(set.records) == 0 {
		return errors.New("no record carries a label, so there is nothing to calibrate")
	}

	runs := make([]storedRun, 0, len(args.Compare))

	for _, file := range args.Compare {
		run, readErr := readStoredRun(cmd.Context(), settings, file, inv.namer, scope.scored, set)
		if readErr != nil {
			return readErr
		}

		runs = append(runs, run)
	}

	comparison, err := comparisonOf(questions, set, runs, cutsFor(args.Cuts, bound, len(questions)), args.Budgets)
	if err != nil {
		return err
	}

	if noteErr := writeCompareNotices(cmd.ErrOrStderr(), set, runs); noteErr != nil {
		return noteErr
	}

	if noteErr := writeLeftOut(cmd.ErrOrStderr(), comparison); noteErr != nil {
		return noteErr
	}

	comparison.Require = measureEachRun(comparison, questions, bound)

	if writeErr := writeComparison(cmd.OutOrStdout(), calib.report, comparison); writeErr != nil {
		return written(writeErr)
	}

	unmet, err := writeUnmetRuns(cmd.ErrOrStderr(), comparison.Require)
	if err != nil {
		return err
	}

	if unmet {
		return &failError{}
	}

	return nil
}

func refuseUncompared(questions []plan.Question, bound []boundRequirement) error {
	for _, req := range bound {
		if question := questions[req.question]; question.Shape != plan.Noul {
			return fmt.Errorf("--require '%s' reads '%s', a %s question, and --compare compares only yes or "+
				"no questions so far", req.Source, question.ID, shapeWords(question.Shape))
		}
	}

	return nil
}

func writeUncompared(w io.Writer, skipped []string, questions []plan.Question) error {
	if err := writeSkipped(w, skipped, false, ""); err != nil {
		return err
	}

	for _, question := range questions {
		if question.Shape == plan.Noul {
			continue
		}

		if err := output.Notice(w, fmt.Sprintf("skipping '%s', a %s question, since --compare compares only "+
			"yes or no questions so far", question.ID, shapeWords(question.Shape))); err != nil {
			return err
		}
	}

	return nil
}

func readStoredRun(
	ctx context.Context, settings rootSettings, file string, namer *jq.Expr, built *plan.Plan, set labelledSet,
) (run storedRun, err error) {
	if _, statErr := settings.stat(file); errors.Is(statErr, fs.ErrNotExist) {
		return storedRun{}, fmt.Errorf("%s does not exist, so --compare has nothing to read from it", file)
	}

	out, _, err := answersfile.Open(answersOptions(settings), answersfile.Config{
		Path: file, Resume: true, Output: output.JSON.String(), ByID: true, ReadOnly: true,
	})

	defer func() {
		err = settings.laterFailures.keepFirst(err, out.Release())
	}()

	if err != nil {
		return storedRun{}, wordAnswersError(err)
	}

	run = storedRun{
		file: file, records: make([]output.Record, len(set.records)), answered: make([]bool, len(set.records)),
	}

	run.fingerprint, run.printed, err = out.StoredFingerprint()
	if err != nil {
		return storedRun{}, err
	}

	book, err := answersfile.ResumeLedger(ctx, out, answersfile.Format{Mode: output.JSON, Namer: namer})
	if err != nil {
		return storedRun{}, err
	}

	var found []storedLine

	for i, rec := range set.records {
		at, stored := book.StoredAt(rec.id)
		book.Admit(rec.id)

		// The ledger notes only answered lines, so an error line reads as no line at all.
		if stored {
			run.answered[i] = true
			found = append(found, storedLine{index: i, at: at})
		}
	}

	run.unclaimed = book.Unclaimed()

	if readErr := storedAnswers(ctx, out, found, built); readErr != nil {
		return storedRun{}, readErr
	}

	for _, line := range found {
		run.records[line.index] = line.record
	}

	return run, nil
}

type storedRun struct {
	file        string
	fingerprint string
	printed     bool
	records     []output.Record
	answered    []bool
	unclaimed   int
}

func writeCompareNotices(w io.Writer, set labelledSet, runs []storedRun) error {
	baseline := runs[0]

	line := fmt.Sprintf("comparing %d answers files over %s, %s being the baseline", len(runs),
		wording.Plural(len(set.records), "labelled record", "labelled records"), baseline.file)
	if set.unlabelled > 0 {
		line += fmt.Sprintf(", %d unlabelled skipped", set.unlabelled)
	}

	notices := []string{line}

	for _, run := range runs[1:] {
		notices = append(notices, differenceNotice(baseline, run)...)
	}

	baseModels := modelCounts(baseline)

	for _, run := range runs {
		counts := modelCounts(run)
		if run.file != baseline.file && !slices.Equal(slices.Sorted(maps.Keys(counts)), slices.Sorted(maps.Keys(baseModels))) {
			notices = append(notices, fmt.Sprintf("%s was answered by %s and %s by %s", run.file,
				modelList(counts), baseline.file, modelList(baseModels)))
		}

		if len(counts) > 1 {
			notices = append(notices, fmt.Sprintf("%s was answered by %d models, %s, so it spans a model update",
				run.file, len(counts), modelTally(counts)))
		}

		if run.unclaimed > 0 {
			notices = append(notices, fmt.Sprintf("%s holds %s for no labelled record of the input, left out",
				run.file, wording.Plural(run.unclaimed, "answer", "answers")))
		}
	}

	for _, notice := range notices {
		if err := output.Notice(w, notice); err != nil {
			return err
		}
	}

	return nil
}

func differenceNotice(baseline, run storedRun) []string {
	switch {
	case !baseline.printed:
		return []string{fmt.Sprintf("%s has no fingerprint beside it, so onesie cannot say how %s differs from it",
			baseline.file, run.file)}
	case !run.printed:
		return []string{fmt.Sprintf("%s has no fingerprint beside it, so onesie cannot say how it differs from %s",
			run.file, baseline.file)}
	}

	changed, known := answersfile.Differences(baseline.fingerprint, run.fingerprint)

	switch {
	case !known:
		return []string{fmt.Sprintf("onesie cannot read the fingerprints of %s and %s alike, so it cannot say "+
			"how their runs differ", baseline.file, run.file)}
	case len(changed) == 0:
		return nil
	}

	return []string{fmt.Sprintf("%s differs from %s in %s", run.file, baseline.file, joinedAnd(changed))}
}

func modelCounts(run storedRun) map[string]int {
	counts := map[string]int{}

	for i, record := range run.records {
		if run.answered[i] && record.Model != "" {
			counts[record.Model]++
		}
	}

	return counts
}

func modelList(counts map[string]int) string {
	if len(counts) == 0 {
		return "no named model"
	}

	return joinedAnd(slices.Sorted(maps.Keys(counts)))
}

func modelTally(counts map[string]int) string {
	tallies := make([]string, 0, len(counts))
	for _, model := range slices.Sorted(maps.Keys(counts)) {
		tallies = append(tallies, fmt.Sprintf("%s on %s", model, wording.Plural(counts[model], "record", "records")))
	}

	return joinedAnd(tallies)
}

func joinedAnd(items []string) string {
	if len(items) == 1 {
		return items[0]
	}

	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func comparisonOf(
	questions []plan.Question, set labelledSet, runs []storedRun, cuts [][]float64, budgets []float64,
) (calibrate.Comparison, error) {
	comparison := calibrate.Comparison{Runs: make([]calibrate.Run, 0, len(runs)), Records: set.total}

	for _, run := range runs {
		comparison.Runs = append(comparison.Runs, calibrate.Run{
			File: run.file, Models: slices.Sorted(maps.Keys(modelCounts(run))),
		})
	}

	inSet := make([]bool, len(set.records))

	for q, question := range questions {
		if question.Shape != plan.Noul {
			continue
		}

		compared, err := compareQuestion(q, question, set, runs, cuts[q], budgets, inSet)
		if err != nil {
			return calibrate.Comparison{}, err
		}

		comparison.Questions = append(comparison.Questions, compared)
	}

	for r, run := range runs {
		comparison.Runs[r].Usage = comparedUsage(run, inSet)
	}

	return comparison, nil
}

func comparedUsage(run storedRun, inSet []bool) *calibrate.Usage {
	var records []output.Record

	costed := 0

	for i, record := range run.records {
		if !inSet[i] || record.Usage == nil {
			continue
		}

		records = append(records, record)

		if record.Usage.Cost != nil {
			costed++
		}
	}

	if len(records) == 0 {
		return nil
	}

	usage := usageOf(records)

	// A cost summed over some of the records would understate the run, so it shows only when every
	// record carries one.
	if costed < usage.Records {
		usage.Cost = nil
	}

	return usage
}

func compareQuestion(
	q int, question plan.Question, set labelledSet, runs []storedRun, cuts, budgets []float64, inSet []bool,
) (calibrate.QuestionComparison, error) {
	compared := calibrate.QuestionComparison{ID: question.ID, LeftOut: make([]calibrate.LeftOut, len(runs))}
	cases := make([][]calibrate.YesNoCase, len(runs))
	answered := make([]int, len(runs))
	unusable := make([]error, len(runs))

	for _, rec := range set.records {
		label := rec.labels[q]
		if label == nil {
			continue
		}

		compared.Labelled++
		recordCases := make([]calibrate.YesNoCase, len(runs))
		usable := true

		for r, run := range runs {
			scored, ok, caseErr := storedCase(run, rec, question, label, &compared.LeftOut[r])
			if ok {
				answered[r]++
				recordCases[r] = scored
			}

			if caseErr != nil && unusable[r] == nil {
				unusable[r] = fmt.Errorf("%s: %w", rec.name(), caseErr)
			}

			usable = usable && ok
		}

		if !usable {
			continue
		}

		inSet[rec.index] = true

		for r := range runs {
			cases[r] = append(cases[r], recordCases[r])
		}
	}

	for r, run := range runs {
		switch {
		case answered[r] > 0 || compared.Labelled == 0:
		case unusable[r] != nil:
			return calibrate.QuestionComparison{}, fmt.Errorf("%s answers question '%s' in no way that can be "+
				"scored, as for %v", run.file, question.ID, unusable[r])
		default:
			return calibrate.QuestionComparison{}, fmt.Errorf("%s holds no answer to question '%s' for a "+
				"labelled record. Every file compared needs the same question ids and record ids", run.file, question.ID)
		}
	}

	compared.Scores = calibrate.CompareYesNo(cases, cuts, budgets)

	return compared, nil
}

func storedCase(
	run storedRun, rec labelledRecord, question plan.Question, label *calibrate.Label, left *calibrate.LeftOut,
) (calibrate.YesNoCase, bool, error) {
	if !run.answered[rec.index] {
		left.Unanswered++

		return calibrate.YesNoCase{}, false, nil
	}

	scored, err := caseOf(question, label, rec, run.records[rec.index])
	if err != nil {
		left.Unusable++

		return calibrate.YesNoCase{}, false, err
	}

	return scored.yesNo, true, nil
}

func writeLeftOut(w io.Writer, comparison calibrate.Comparison) error {
	for _, q := range comparison.Questions {
		var parts []string

		for r, left := range q.LeftOut {
			file := comparison.Runs[r].File
			if left.Unanswered > 0 {
				parts = append(parts, fmt.Sprintf("%d unanswered in %s", left.Unanswered, file))
			}

			if left.Unusable > 0 {
				parts = append(parts, fmt.Sprintf("%d unusable in %s", left.Unusable, file))
			}
		}

		if len(parts) == 0 {
			continue
		}

		if err := output.Notice(w, fmt.Sprintf("left out of '%s': %s", q.ID, joinedAnd(parts))); err != nil {
			return err
		}
	}

	return nil
}

func measureEachRun(
	comparison calibrate.Comparison, questions []plan.Question, bound []boundRequirement,
) []calibrate.RunResult {
	if len(bound) == 0 {
		return nil
	}

	var results []calibrate.RunResult

	for r, run := range comparison.Runs {
		report := calibrate.Report{Questions: make([]calibrate.QuestionReport, len(questions))}
		compared := 0

		for q, question := range questions {
			report.Questions[q] = calibrate.QuestionReport{ID: question.ID, Shape: question.Shape}

			if question.Shape == plan.Noul {
				report.Questions[q].YesNo = &comparison.Questions[compared].Scores[r].YesNo
				compared++
			}
		}

		for _, result := range measureRequirements(report, bound) {
			results = append(results, calibrate.RunResult{File: run.File, Result: result})
		}
	}

	return results
}

func writeComparison(w io.Writer, format string, comparison calibrate.Comparison) error {
	if format == "json" {
		return calibrate.WriteComparisonJSON(w, comparison)
	}

	return calibrate.WriteComparisonTable(w, comparison)
}

func writeUnmetRuns(w io.Writer, results []calibrate.RunResult) (bool, error) {
	unmet := false

	for _, result := range results {
		if result.Held {
			continue
		}

		unmet = true

		if err := output.Notice(w, result.String()); err != nil {
			return unmet, err
		}
	}

	return unmet, nil
}
