package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

const maxCauses = 5

func calibrateRun(
	cmd *cobra.Command, settings rootSettings, flags runConfig, calib calibrateFlags, inputMode input.Mode,
	inv *invocation, scope calibrate.Scope, bound []calibrate.BoundRequirement,
) (err error) {
	model, err := resolveModel(settings, scope.Scored.Model)
	if err != nil {
		return err
	}

	base, err := cutsOf(cmd, calib)
	if err != nil {
		return err
	}

	cuts := calibrate.CutsFor(base, bound, len(scope.Scored.Questions))

	if calib.offline {
		if _, statErr := settings.stat(flags.out); errors.Is(statErr, fs.ErrNotExist) {
			return fmt.Errorf("%s does not exist, so --offline has nothing to read. "+
				"Rerun without --offline to create it", flags.out)
		}
	}

	out, _, err := openOut(settings, flags.runFlags, calib.offline)

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
		LongContext: inv.Plan.LongContext,
	}

	asked, err := askedPlan(settings, flags, out, scope, inputs)
	if err != nil {
		return err
	}

	answers, err := answersFor(cmd, settings, flags, asked, scope.Full.Questions, model)
	if err != nil {
		return err
	}

	config := calibrateConfig(settings, flags, inputMode, inv)
	config.Ask = runner.Ask{
		Plan: asked, Model: model, Questions: plan.WireAll(asked.Questions), Usage: flags.usage, Message: text,
	}
	config.Offline = calib.offline

	runErr := calibrateAnswers(cmd, settings, flags, calib, inv, calibrateAsk{
		scope: scope, asked: asked, inputs: inputs, cuts: cuts, bound: bound, out: out, answers: answers,
		config: config,
	})
	if out == nil || calib.offline {
		return runErr
	}

	return settings.laterFailures.keepFirst(runErr, out.Finish(runErr))
}

func askedPlan(
	settings rootSettings, flags runConfig, out *answersfile.File, scope calibrate.Scope, inputs answersfile.FingerprintInputs,
) (*plan.Plan, error) {
	if out == nil || !out.Resuming() || len(scope.Skipped) == 0 {
		return scope.Scored, nil
	}

	stored, found, err := out.StoredFingerprint()
	if err != nil || !found {
		return scope.Scored, err
	}

	whole, err := outFingerprint(settings, flags, scope.Full.Questions, inputs)
	if err != nil {
		return nil, err
	}

	if whole.Matches(stored) {
		return scope.Full, nil
	}

	return scope.Scored, nil
}

type calibrateAsk struct {
	scope   calibrate.Scope
	asked   *plan.Plan
	inputs  answersfile.FingerprintInputs
	cuts    [][]float64
	bound   []calibrate.BoundRequirement
	out     *answersfile.File
	answers runner.AnswererFactory
	config  calibrate.Config
}

func calibrateConfig(settings rootSettings, flags runConfig, inputMode input.Mode, inv *invocation) calibrate.Config {
	return calibrate.Config{
		Input:     settings.stdin,
		InputMode: inputMode,
		SkipBlank: flags.skipBlank,
		Delimiter: flags.parsedDelimiter,
		Mapper:    inv.Mapper,
		Namer:     inv.Namer,
		Images:    inv.images,
		Jobs:      flags.jobs,
		Prune:     flags.prune,
		Speller:   plan.Flags,
	}
}

func calibrateAnswers(
	cmd *cobra.Command, settings rootSettings, flags runConfig, calib calibrateFlags, inv *invocation,
	run calibrateAsk,
) error {
	scored, out := run.scope.Scored, run.out

	keptWhole := len(run.asked.Questions) > len(scored.Questions)
	if !keptWhole {
		if noteErr := writeSkipped(cmd.ErrOrStderr(), run.scope.Skipped, false, ""); noteErr != nil {
			return noteErr
		}
	}

	set, err := calibrate.ReadLabelled(cmd.Context(), run.config, run.scope.Labels)
	if err != nil {
		return firstIDHint(err, run.config.InputMode)
	}

	if len(set.Records) == 0 {
		return errors.New("no record carries a label, so there is nothing to calibrate")
	}

	bindErr := bindOut(out, settings, flags, run.asked.Questions, run.inputs)
	if stale := (*answersfile.StaleAnswersError)(nil); calib.offline && errors.As(bindErr, &stale) {
		return &plainError{cause: bindErr, message: fmt.Sprintf("%s does not match this run, since %s, so "+
			"--offline cannot read it. Rerun without --offline and --resume to regenerate it", flags.out, stale.Cause())}
	}

	if bindErr != nil {
		return bindErr
	}

	format := answersfile.Format{Mode: output.JSON, Merge: merging(flags), MergeKey: mergeKey(flags), Namer: inv.Namer}

	resumed, err := calibrate.ResumeLabelled(cmd.Context(), out, format, scored, set)
	if err != nil {
		return err
	}

	if calib.offline && len(resumed.Pending) > 0 {
		return unansweredOffline(flags.out, resumed.Pending)
	}

	if keptWhole {
		askedOf := ""
		if len(resumed.Pending) > 0 {
			askedOf = flags.out
		}

		if noteErr := writeSkipped(cmd.ErrOrStderr(), run.scope.Skipped, true, askedOf); noteErr != nil {
			return noteErr
		}
	}

	costErr := writeCost(cmd.ErrOrStderr(), set, resumed, flags.out, len(run.asked.Questions), calib.offline)
	if costErr != nil {
		return costErr
	}

	return withStats(cmd, settings.now, flags, func(stats *runner.Collector) error {
		return reportLabelled(cmd, flags, calib, run, set, resumed, stats)
	})
}

func reportLabelled(
	cmd *cobra.Command,
	flags runConfig,
	calib calibrateFlags,
	run calibrateAsk,
	set calibrate.LabelledSet,
	resumed calibrate.ResumedSet,
	stats *runner.Collector,
) error {
	stats.Skip(resumed.Skipped())

	outcomes, result, err := calibrate.AskLabelled(cmd.Context(), run.config, run.out, resumed, run.answers, stats)
	if result.Aborted {
		return &abortError{cause: result.Cause}
	}

	if err != nil {
		return err
	}

	report := calibrate.ReportOf(run.scope.Scored, set, outcomes, run.cuts)
	report.Asked = result.Records
	report.Stored = resumed.Stored
	report.Mocked = flags.mockPath != ""
	report.Require = calibrate.MeasureRequirements(report, run.bound)

	if flags.usage {
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
		return &rejectedError{}
	}

	return nil
}

func unansweredOffline(answers string, pending []calibrate.LabelledRecord) error {
	if len(pending) == 1 {
		return fmt.Errorf("%s does not answer %s, so --offline cannot report it. "+
			"Rerun without --offline to ask it", answers, pending[0].Name())
	}

	return fmt.Errorf("%s does not answer %s or %d more, so --offline cannot report them. "+
		"Rerun without --offline to ask them", answers, pending[0].Name(), len(pending)-1)
}

func cutsOf(cmd *cobra.Command, calib calibrateFlags) ([]float64, error) {
	if !cmd.Flags().Changed(flagCuts) {
		return calibrate.DefaultCuts(), nil
	}

	cuts, err := calibrate.ParseCuts(calib.cuts)
	if err != nil {
		return nil, fmt.Errorf("--cuts %w", err)
	}

	return cuts, nil
}

func writeCost(
	w io.Writer, set calibrate.LabelledSet, resumed calibrate.ResumedSet, answers string, questions int, offline bool,
) error {
	line := fmt.Sprintf("asking %d of %s, %s each",
		len(resumed.Pending), plural(len(set.Records), "record"), plural(questions, "question"))
	if offline {
		line = fmt.Sprintf("reading %s from %s", plural(len(set.Records), "record"), answers)
	}

	if set.Unlabelled > 0 {
		line += fmt.Sprintf(", %d unlabelled skipped", set.Unlabelled)
	}

	if answers != "" && !offline {
		line += fmt.Sprintf(", %d answered in %s", resumed.Stored, answers)
	}

	return output.Notice(w, line)
}

func writeCauses(w io.Writer, set calibrate.LabelledSet, outcomes []output.Record) error {
	listed, more := 0, 0

	for i, rec := range set.Records {
		failure := outcomes[i].Failure
		if failure == nil {
			continue
		}

		if listed == maxCauses {
			more++

			continue
		}

		listed++

		if err := output.Notice(w, rec.Name()+": "+failure.Message); err != nil {
			return err
		}
	}

	if more == 0 {
		return nil
	}

	return output.Notice(w, "and "+plural(more, "more failed record"))
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
