package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/frodi-karlsson/onesie/internal/qfile"
	"github.com/frodi-karlsson/onesie/internal/settingrow"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

var refusedReasons = map[string]string{
	"assert":          "calibrate reports every cut and judges none",
	"abstain-if":      "calibrate reports every cut and judges none",
	"threshold":       "calibrate reports every cut",
	"min-confidence":  "calibrate reports every confidence cut",
	"fallback":        "calibrate scores the answers the model gave",
	"merge":           "calibrate prints a report, not the records",
	"merge-key":       "calibrate prints a report, not the records",
	"quiet":           "calibrate prints a report, and its exit code judges nothing",
	"stop-on":         "calibrate scores every record it can and judges no assertion",
	"unordered":       "calibrate prints its report once every record is answered",
	"print-questions": "a question file carries no labels",
	flagState:         "calibrate reads each state from its input through --map",
}

func newCalibrateCmd(settings rootSettings, flags *runFlags, persistent *pflag.FlagSet) *cobra.Command {
	recorder := argv.New()
	calib := &calibrateFlags{}

	var given *setting.Given

	cmd := &cobra.Command{
		Use:           "calibrate [question]",
		Short:         "Score questions against records whose answers are known",
		Args:          atMostOneQuestion,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			defer func() {
				err = withDashQuestionHint(err, cmd.Flags(), settings.args, args)
			}()

			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}

			if cmd.Flags().NFlag() == 0 && len(args) == 0 {
				return cmd.Help()
			}

			return runCalibrate(cmd, settings, *calib, given, recorder.Events(), positional)
		},
	}

	given = setting.Register(cmd.Flags(), nil, settingrow.Table(), recorder, settingrow.CommandCalibrate,
		calibrateTargets(flags, calib))
	given.Inherit(persistent)
	given.Within(flags.given)

	return cmd
}

func calibrateTargets(flags *runFlags, calib *calibrateFlags) map[settingrow.Name]any {
	targets := runTargets(flags)
	maps.Copy(targets, map[settingrow.Name]any{
		setting.Output.Name(): &calib.report, setting.Input.Name(): &calib.input, "label": &calib.labels,
		setting.Cuts.Name(): &calib.cuts, "require": &calib.requires, setting.Offline.Name(): &calib.offline,
		"compare": &calib.compare, setting.FalseAlarms.Name(): &calib.budgets,
	})

	return targets
}

func runCalibrate(
	cmd *cobra.Command, settings rootSettings, calib calibrateFlags, given *setting.Given,
	events []argv.Event, positional string,
) (err error) {
	early, err := setting.ParseRun(settingrow.Table(), given, settingrow.CommandCalibrate)
	if err != nil {
		return err
	}

	inputMode, err := input.ParseMode(calib.input)
	if err != nil {
		return err
	}

	flags, err := newRunConfig(early, given, events, positional)
	if err != nil {
		return err
	}
	flags.inputMode = inputMode

	flags.calibrate = &calib

	// A file's gate and policy are ignored, since calibrate reports every cut and a gated file is
	// the one a user calibrates.
	inv, ready, err := build(settings, flags, given, settingrow.CommandCalibrate, true)

	if warnErr := warnAll(cmd.ErrOrStderr(), ready.warnings); warnErr != nil {
		return warnErr
	}

	if err != nil {
		return err
	}

	flags = ready.config

	defer func() {
		err = withKeyHint(err, settings, flags)
	}()

	if noticeErr := announce(cmd.ErrOrStderr(), ready.notices); noticeErr != nil {
		return noticeErr
	}

	scope, err := scopeOf(inv.plan, calib.labels)
	if err != nil {
		return err
	}

	if refuseErr := refuseUnlabelledBody(inv.loaded, scope.scored); refuseErr != nil {
		return refuseErr
	}

	bound, err := resolveRequirements(scope, inv.fileGate, ready.calibration.Requirements)
	if err != nil {
		return err
	}

	if setting.Get(flags.merged, setting.PrintRequest) {
		return printCalibrateRequests(cmd, settings, flags, inputMode, inv, scope)
	}

	if len(ready.calibration.Compare) > 0 {
		return compareCalibration(cmd, settings, flags, calib, inputMode, inv, scope, bound, ready.calibration)
	}

	return liveCalibration(cmd, settings, flags, calib, inputMode, inv, scope, bound, ready.calibration.Cuts)
}

func refusedOnCalibrate(root *cobra.Command, err error) error {
	var unknown *pflag.NotExistError
	if !errors.As(err, &unknown) {
		return nil
	}

	known := rootFlag(root, unknown)
	if known == nil {
		return nil
	}

	reason, refused := refusedReasons[known.Name]
	if !refused {
		return nil
	}

	spelled := "--" + known.Name
	if known.Shorthand != "" {
		spelled = "-" + known.Shorthand + "/" + spelled
	}

	return &plainError{cause: err, message: fmt.Sprintf("%s, so %s does not apply. Drop it", reason, spelled)}
}

func refuseUnlabelledBody(loaded *qfile.File, scored *plan.Plan) error {
	if loaded == nil {
		return nil
	}

	for _, question := range loaded.Questions {
		labelled := slices.ContainsFunc(scored.Questions, func(q plan.Question) bool { return q.ID == question.ID })
		if labelled && question.Shape == plan.Rate && !question.Labelled {
			return fmt.Errorf("question '%s' from a request body has no level names to label with",
				question.ID)
		}
	}

	return nil
}

func scopeOf(built *plan.Plan, specs []string) (calibrateScope, error) {
	ids := make([]string, 0, len(built.Questions))
	for _, question := range built.Questions {
		ids = append(ids, question.ID)
	}

	exprs := make([]*jq.Expr, len(ids))

	for _, spec := range specs {
		id, source, err := calibrate.SplitLabel(spec, ids)
		if err != nil {
			return calibrateScope{}, err
		}

		index := slices.Index(ids, id)
		if exprs[index] != nil {
			return calibrateScope{}, fmt.Errorf("--label is given twice for question '%s'", id)
		}

		exprs[index], err = exprOf(true, "--label "+id, source)
		if err != nil {
			return calibrateScope{}, err
		}
	}

	scored := *built
	scored.Questions = nil
	scope := calibrateScope{full: built, scored: &scored}

	for index, expr := range exprs {
		question := built.Questions[index]
		if expr == nil {
			scope.skipped = append(scope.skipped, question.ID)

			continue
		}

		if question.Shape == plan.Box {
			return calibrateScope{}, fmt.Errorf(
				"question '%s' is a box, and a box cannot be calibrated. Drop its --label", question.ID)
		}

		scored.Questions = append(scored.Questions, question)
		scope.labels = append(scope.labels, questionLabel{
			id: question.ID, shape: question.Shape, names: namesOf(question), expr: expr,
		})
	}

	if len(scope.labels) == 0 {
		return calibrateScope{}, fmt.Errorf("question '%s' has no --label, so asking it would cost "+
			"requests and report nothing. Pass --label %s=EXPR", ids[0], ids[0])
	}

	return scope, nil
}

type calibrateScope struct {
	full    *plan.Plan
	scored  *plan.Plan
	skipped []string
	labels  []questionLabel
}

func writeSkipped(w io.Writer, skipped []string, inReport bool, askedOf string) error {
	if len(skipped) == 0 {
		return nil
	}

	quoted := make([]string, len(skipped))
	for i, id := range skipped {
		quoted[i] = "'" + id + "'"
	}

	names := quoted[0]
	if len(quoted) > 1 {
		names = strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}

	has, pronoun, subject := "has", "it", "it has"
	if len(skipped) > 1 {
		has, pronoun, subject = "have", "them", "they have"
	}

	line := fmt.Sprintf("skipping %s, which %s no --label", names, has)

	switch {
	case askedOf != "":
		line = fmt.Sprintf("skipping %s in the report, since %s no --label, but asking %s of each "+
			"record %s lacks, so the file keeps answering every question", names, subject, pronoun, askedOf)
	case inReport:
		line = fmt.Sprintf("skipping %s in the report, since %s no --label", names, subject)
	}

	return output.Notice(w, line)
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

type calibrateFlags struct {
	labels   []string
	requires []string
	compare  []string
	cuts     string
	budgets  string
	report   string
	input    string
	offline  bool
}

type questionLabel struct {
	id    string
	shape plan.Shape
	names []string
	expr  *jq.Expr
}
