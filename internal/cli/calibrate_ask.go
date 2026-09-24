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
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
)

const maxCauses = 5

func calibrateRun(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, calib calibrateFlags, inputMode input.Mode,
	inv *invocation, labels []questionLabel,
) error {
	built := inv.plan

	model, err := resolveModel(settings, flags, built.Model)
	if err != nil {
		return err
	}

	set, err := readLabelled(cmd.Context(), settings, inputMode, flags, inv.mapper, inv.namer, labels)
	if err != nil {
		return err
	}

	if len(set.records) == 0 {
		return errors.New("onesie: no record carries a label, so there is nothing to calibrate")
	}

	cuts, err := cutsOf(cmd, calib)
	if err != nil {
		return err
	}

	if costErr := writeCost(cmd.ErrOrStderr(), set, len(built.Questions)); costErr != nil {
		return costErr
	}

	return withStats(cmd, settings.now, flags, func(stats *collector) error {
		answered, result, askErr := askLabelled(cmd, settings, flags, built, model, set, stats)
		if askErr != nil {
			return askErr
		}

		report := reportOf(built, set, answered, cuts)
		report.Asked = result.Records

		if writeErr := writeReport(cmd.OutOrStdout(), calib.report, report); writeErr != nil {
			return written(writeErr)
		}

		if result.Failed == 0 {
			return nil
		}

		if causeErr := writeCauses(cmd.ErrOrStderr(), set, answered); causeErr != nil {
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

func writeCost(w io.Writer, set labelledSet, questions int) error {
	line := fmt.Sprintf("asking %d of %s, %s each",
		len(set.records), plural(len(set.records), "record"), plural(questions, "question"))

	if set.unlabelled > 0 {
		line += fmt.Sprintf(", %d unlabelled skipped", set.unlabelled)
	}

	_, err := fmt.Fprintln(w, line)

	return err
}

func askLabelled(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, built *plan.Plan, model string,
	set labelledSet, stats *collector,
) ([]output.Record, engine.Result, error) {
	client, err := settings.newClient(cmd.Context(), observing(stats)...)
	if err != nil {
		return nil, engine.Result{}, err
	}

	questions := wireAll(built.Questions)
	answered := make([]output.Record, len(set.records))

	result, err := engine.Run(cmd.Context(), engine.Config[labelledRecord, askedLine]{
		Source: &labelledSource{records: set.records},
		Evaluate: func(ctx context.Context, rec labelledRecord) (askedLine, error) {
			record, evalErr := evaluate(ctx, client, built, model, questions, rec.sent, flags.usage, stats)
			if evalErr == nil {
				if _, evalErr = casesOf(built, rec, record); evalErr != nil {
					record = failureRecord(built, evalErr)
					stats.unusable()
				}
			}

			return askedLine{index: rec.index, record: record}, evalErr
		},
		Write: func(l askedLine) error {
			answered[l.index] = l.record

			return nil
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

	return answered, result, nil
}

func (c *collector) unusable() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// The request already counted as a success when it arrived, and its answer turned out unusable.
	c.failed++
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
	record output.Record
}

func reportOf(built *plan.Plan, set labelledSet, answered []output.Record, cuts []float64) calibrate.Report {
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
		cases, err := casesOf(built, rec, answered[i])
		if err != nil {
			report.Failed++

			for q, label := range rec.labels {
				if label != nil {
					failed[q]++
				}
			}

			continue
		}

		models[answered[i].Model] = struct{}{}

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
		name = idText(rec.id)
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

func writeCauses(w io.Writer, set labelledSet, answered []output.Record) error {
	listed, more := 0, 0

	for i, rec := range set.records {
		failure := answered[i].Failure
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

	_, err := fmt.Fprintf(w, "onesie: and %d more failed %s\n", more, noun(more, "record"))

	return err
}

func noun(count int, word string) string {
	if count == 1 {
		return word
	}

	return word + "s"
}

func writeReport(w io.Writer, format string, report calibrate.Report) error {
	if format == "json" {
		return calibrate.WriteJSON(w, report)
	}

	return calibrate.WriteTable(w, report)
}
