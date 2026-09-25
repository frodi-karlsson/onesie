package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/answer"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/engine"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jev"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const maxCauses = 5

func calibrateRun(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, calib calibrateFlags, inputMode input.Mode,
	inv *invocation, labels []questionLabel,
) (err error) {
	model, err := resolveModel(settings, flags, inv.plan.Model)
	if err != nil {
		return err
	}

	cuts, err := cutsOf(cmd, calib)
	if err != nil {
		return err
	}

	out, _, err := openOut(settings, flags)
	if err != nil {
		return err
	}

	defer func() {
		err = errors.Join(err, out.release())
	}()

	runErr := calibrateAnswers(cmd, settings, flags, calib.report, inputMode, inv, labels, model, cuts, out)
	if out == nil {
		return runErr
	}

	return errors.Join(runErr, out.finish(runErr))
}

func calibrateAnswers(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, format string, inputMode input.Mode,
	inv *invocation, labels []questionLabel, model string, cuts []float64, out *outFile,
) error {
	built := inv.plan

	set, err := readLabelled(cmd.Context(), settings, inputMode, flags, inv.mapper, inv.namer, labels)
	if err != nil {
		return err
	}

	if len(set.records) == 0 {
		return errors.New("onesie: no record carries a label, so there is nothing to calibrate")
	}

	// The labels and the cuts stay out, since neither changes an answer, and a later stream run
	// with -o json and the same questions reads the file as its own.
	bindErr := bindOut(out, settings, flags, built.Questions, fingerprintInputs{
		model: model, output: output.JSON.String(), input: inputMode.String(),
	})
	if bindErr != nil {
		return bindErr
	}

	resumed, err := resumeLabelled(cmd.Context(), out, flags, inv.namer, built, set)
	if err != nil {
		return err
	}

	if costErr := writeCost(cmd.ErrOrStderr(), set, resumed, flags.out, len(built.Questions)); costErr != nil {
		return costErr
	}

	return withStats(cmd, settings.now, flags, func(stats *collector) error {
		if resumed.book != nil {
			stats.skip(resumed.book.skipped())
		}

		outcomes, result, askErr := askLabelled(cmd, settings, flags, built, model, resumed, out, stats)
		if askErr != nil {
			return askErr
		}

		report := reportOf(built, set, outcomes, cuts)
		report.Asked = result.Records
		report.Stored = resumed.stored

		if flags.usage {
			report.Usage = usageOf(outcomes)
		}

		if writeErr := writeReport(cmd.OutOrStdout(), format, report); writeErr != nil {
			return written(writeErr)
		}

		if result.Failed == 0 {
			return nil
		}

		if causeErr := writeCauses(cmd.ErrOrStderr(), set, outcomes); causeErr != nil {
			return causeErr
		}

		return &recordsError{}
	})
}

func cutsOf(cmd *cobra.Command, calib calibrateFlags) ([]float64, error) {
	if !cmd.Flags().Changed(flagCuts) {
		return calibrate.DefaultCuts(), nil
	}

	cuts, err := calibrate.ParseCuts(calib.cuts)
	if err != nil {
		return nil, fmt.Errorf("onesie: --cuts %w", err)
	}

	return cuts, nil
}

func writeCost(w io.Writer, set labelledSet, resumed resumedSet, answers string, questions int) error {
	line := fmt.Sprintf("asking %d of %s, %s each",
		len(resumed.pending), plural(len(set.records), "record"), plural(questions, "question"))

	if set.unlabelled > 0 {
		line += fmt.Sprintf(", %d unlabelled skipped", set.unlabelled)
	}

	if answers != "" {
		line += fmt.Sprintf(", %d answered in %s", resumed.stored, answers)
	}

	_, err := fmt.Fprintln(w, line)

	return err
}

func askLabelled(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, built *plan.Plan, model string,
	resumed resumedSet, out *outFile, stats *collector,
) ([]output.Record, engine.Result, error) {
	client, err := settings.newClient(cmd.Context(), observing(stats)...)
	if err != nil {
		return nil, engine.Result{}, err
	}

	questions := wireAll(built.Questions)
	outcomes := slices.Clone(resumed.answered)
	book := resumed.book

	result, err := engine.Run(cmd.Context(), engine.Config[labelledRecord, askedLine]{
		Source: &labelledSource{records: resumed.pending},
		Evaluate: func(ctx context.Context, rec labelledRecord) (askedLine, error) {
			record, evalErr := evaluate(ctx, client, built, model, questions, rec.sent, flags.usage, stats)
			if evalErr == nil {
				if _, evalErr = casesOf(built, rec, record); evalErr != nil {
					// The tokens were spent, so the failed line carries them into the usage total.
					record = spent(failureRecord(built, evalErr), record.Usage)

					stats.unusable()
				}
			}

			record.ID = rec.id

			return askedLine{index: rec.index, slot: rec.slot, record: record}, evalErr
		},
		Write: func(l askedLine) error {
			outcomes[l.index] = l.record

			if out == nil {
				return nil
			}

			start := out.offset()
			writeErr := output.Write(out, output.JSON, l.record)

			if book != nil {
				book.wrote(l.slot, span{start: start, end: out.offset()})
			}

			return writeErr
		},
		Jobs:  flags.jobs,
		Abort: aborting,
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
	if book != nil && book.complete() {
		out.compactInto(book.takeOrder(flags.prune), output.JSON)
	}

	return outcomes, result, nil
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
}

func reportOf(built *plan.Plan, set labelledSet, outcomes []output.Record, cuts []float64) calibrate.Report {
	report := calibrate.Report{
		Records:    set.total,
		Labelled:   len(set.records),
		Unlabelled: set.unlabelled,
	}

	yesNo := make([][]calibrate.YesNoCase, len(built.Questions))
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
				yesNo[q] = append(yesNo[q], c.yesNo)
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
			score := calibrate.ScoreYesNo(yesNo[q], failed[q], cuts)
			scored.YesNo = &score
		case plan.Pick:
			score := calibrate.ScorePick(choices[q], namesOf(question), failed[q], cuts)
			scored.Pick = &score
		case plan.Rate:
			score := calibrate.ScoreRate(choices[q], namesOf(question), failed[q], cuts)
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

	name := ""
	if rec.id != nil {
		name = jq.IDText(rec.id)
	}

	cases := make([]recordCase, len(built.Questions))

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
				return nil, unusableAnswer(question.ID, "answered %v, which lies outside [0,1]", given.Value)
			}

			cases[q].yesNo = calibrate.YesNoCase{
				Name: name, ID: rec.id, Line: rec.line, Yes: rec.labels[q].Yes, Value: value,
			}

			continue
		}

		picked, isName := given.Value.(string)
		if !isName {
			return nil, unusableAnswer(question.ID, "picked %v, which is not a name", given.Value)
		}

		if given.Confidence == nil || !unitRange(*given.Confidence) {
			return nil, unusableAnswer(question.ID, "answered with a confidence outside [0,1]")
		}

		cases[q].choice = calibrate.ChoiceCase{
			Name: name, Label: rec.labels[q].Name, Picked: picked, ID: rec.id, Line: rec.line,
			Confidence: *given.Confidence,
		}
	}

	return cases, nil
}

func answerTo(question plan.Question, record output.Record) (*answer.Answer, error) {
	for _, named := range record.Answers {
		if named.ID == question.ID && named.Answer != nil {
			return named.Answer, nil
		}
	}

	return nil, unusableAnswer(question.ID, "has no answer")
}

func unusableAnswer(id, format string, args ...any) error {
	// A 200 whose body the scorers cannot use, the kind a stream reports as a response failure.
	return &jev.ResponseError{
		Status:  http.StatusOK,
		Message: fmt.Sprintf("question '%s' ", id) + fmt.Sprintf(format, args...),
	}
}

func unitRange(value float64) bool {
	// NaN fails both comparisons, so it is refused with the values outside the range.
	return value >= 0 && value <= 1
}

type recordCase struct {
	yesNo  calibrate.YesNoCase
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

		cause := strings.TrimPrefix(failure.Message, "onesie: ")
		if _, err := fmt.Fprintf(w, "onesie: %s: %s\n", rec.name(), cause); err != nil {
			return err
		}
	}

	if more == 0 {
		return nil
	}

	_, err := fmt.Fprintf(w, "onesie: and %s\n", plural(more, "more failed record"))

	return err
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

	return total
}
