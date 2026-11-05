package cli

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

const flagCuts = "cuts"

var refusedReasons = map[string]string{
	"assert":          "calibrate reports every cut and judges none",
	"abstain-if":      "calibrate reports every cut and judges none",
	"threshold":       "calibrate reports every cut",
	"min-confidence":  "calibrate reports every confidence cut",
	"fallback":        "calibrate scores the answers the model gave",
	"merge":           "calibrate prints a report, not the records",
	"merge-key":       "calibrate prints a report, not the records",
	"quiet":           "calibrate prints a report, and its exit code judges nothing",
	"raw":             "calibrate prints its report as a table or as json",
	"stop-on-error":   "calibrate scores every record it can",
	"stop-on-assert":  "calibrate judges no assertion",
	"unordered":       "calibrate prints its report once every record is answered",
	"print-questions": "a question file carries no labels",
	flagState:         "calibrate reads each state from its input through --map",
	flagStateFile:     "calibrate reads each state from its input through --map",
}

func newCalibrateCmd(settings rootSettings, flags *runFlags, persistent *pflag.FlagSet) *cobra.Command {
	recorder := argv.New()
	calib := &calibrateFlags{}

	var given *setting.Given

	cmd := &cobra.Command{
		Use:   "calibrate [question]",
		Short: "Score questions against records whose answers are known",
		Long:  calibrateLong(),
		Example: "  onesie calibrate --ask urgent='is this urgent' -i jsonl --map '.body' \\\n" +
			"      --label urgent='.is_urgent' --id '.id' --out answers.jsonl --resume < labelled.jsonl\n" +
			"  onesie calibrate -f triage -i jsonl --map '.body' --label urgent='.is_urgent' --id '.id' \\\n" +
			"      --out answers.jsonl --resume --offline --require 'urgent.catches >= 0.95' < labelled.jsonl",
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

			return runCalibrate(cmd, settings, flags, *calib, given, recorder.Events(), positional)
		},
	}

	given = setting.Register(cmd.Flags(), nil, setting.Table(), recorder, setting.CommandCalibrate,
		calibrateTargets(flags, calib))
	given.Inherit(persistent)

	return cmd
}

func calibrateTargets(flags *runFlags, calib *calibrateFlags) map[setting.Name]any {
	targets := runTargets(flags)
	maps.Copy(targets, map[setting.Name]any{
		setting.Output.Name(): &calib.report, setting.Input.Name(): &calib.input, "label": &calib.labels,
		setting.Cuts.Name(): &calib.cuts, "require": &calib.requires, setting.Offline.Name(): &calib.offline,
	})

	return targets
}

func calibrateLong() string {
	return "calibrate asks the model about labelled records, compares each answer with the label a " +
		"--label jq expression takes from the record, and prints cut, agreement and confusion " +
		"tables to pick a gate from. It never picks the cut.\n\n" +
		"Map only the text a person would read, since a --map that selects the label flatters " +
		"the question.\n\n" +
		"Labels: a yes or no label is true, false, yes, no, y, n, 1 or 0 in any case. A pick or rate " +
		"label is an option or level name as declared, with spaces around it ignored, or a number whose " +
		"text is one, so 4 matches --rate 1,2,3,4,5. null, no result or a string of only spaces leaves " +
		"a record unlabelled " +
		"for that question, and a record no question labels is not asked. A jsonl record reads " +
		"{\"id\":\"T-1\",\"body\":\"the site is down\",\"is_urgent\":true}, and a csv one " +
		"has the header id,body,is_urgent and the row T-1,the site is down,yes.\n\n" +
		"A question with no --label is skipped, left out of the requests and the report, and named " +
		"on stderr. At least one question needs a --label, a --label naming no question exits 2, and " +
		"so does a --require that reads a skipped question. A --resume into an --out file that " +
		"answers every question still asks the skipped ones, so the file stays whole.\n\n" +
		"A yes or no cut row flags a record when its value is at least the cut, so the cut goes into " +
		"a gate as written. A question file's assert, abstain_if, threshold, min_confidence and " +
		"fallback are ignored, since calibrate reports every cut. The same flags are refused.\n\n" +
		"A pick or rate table has a row per name. found is the share of the records labelled with a " +
		"name that were picked as it, and right when picked the share of the records picked as a name " +
		"that carry it as their label.\n\n" +
		"--out keeps the answers as -o json lines, and --resume, which needs --id, asks only the " +
		"records the file does not answer. Changing a label or --cuts reuses every stored answer. " +
		"A plain stream run can resume the file too, given the same questions, model, -i, --map " +
		"and --id, with -o json and no gate or merge.\n\n" +
		"--require turns the report into a check. Each one reads [lower|upper](ID.MEASURE) OP NUMBER " +
		"[at CUT], where MEASURE is catches, false_alarms, right_when_flagged or auc for yes or no, " +
		"agreement for pick and rate, and within_one for rate. OP is >=, >, <= or <, and NUMBER is a " +
		"fraction between 0 and 1. lower and upper compare an end of the 95 percent interval instead " +
		"of the value, so 16 of 16 fails lower(x.catches) >= 0.9. A yes or no measure other than auc is " +
		"read at one cut: at CUT names it, at abstain takes it from the -f file's abstain_if, and " +
		"otherwise the file's assert gives it when it compares ID.value < X or ID.value >= X. The " +
		"report still prints, and each requirement that did not hold is named on stderr.\n\n" +
		"--offline, with --out and --resume, reads every answer from the file and asks nothing, so it " +
		"needs no key and never rewrites the file. A record the file does not answer, a missing file " +
		"or a file whose fingerprint differs exits 2.\n\n" +
		exitCodesSection(calibrateExitCodes)
}

func runCalibrate(
	cmd *cobra.Command, settings rootSettings, parsed *runFlags, calib calibrateFlags, given *setting.Given,
	events []argv.Event, positional string,
) (err error) {
	// Only the refusal is read here. runConfig holds the values the run uses.
	if _, err = setting.ParseRun(setting.Table(), given, setting.CommandCalibrate); err != nil {
		return err
	}

	own := *parsed
	own.input = calib.input

	flags, err := newRunConfig(cmd, own, events, positional)
	if err != nil {
		return err
	}

	inputMode := flags.inputMode
	flags.calibrate = &calib
	flags.cutsSet = cmd.Flags().Changed(flagCuts)
	flags.reportSet = cmd.Flags().Changed("output")

	// A file's gate and policy are ignored, since calibrate reports every cut and a gated file is
	// the one a user calibrates.
	inv, ready, err := build(settings, flags, given, setting.CommandCalibrate, true)

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

	reqs, err := parseRequirements(calib.requires)
	if err != nil {
		return err
	}

	if warnErr := warnIgnoredBodyState(
		cmd.ErrOrStderr(), flags, inv.loaded, "calibrate", inv.images, plan.WireAll(inv.plan.Questions)); warnErr != nil {
		return warnErr
	}

	if !calib.offline {
		if refuseErr := checkProviderNeeds(settings, flags, inv.plan, inv.images); refuseErr != nil {
			return refuseErr
		}
	}

	scope, err := scopeOf(inv.plan, calib.labels)
	if err != nil {
		return err
	}

	if refuseErr := refuseUnlabelledBody(settings, flags.file, scope.scored); refuseErr != nil {
		return refuseErr
	}

	bound, err := resolveRequirements(scope, inv.fileGate, reqs)
	if err != nil {
		return err
	}

	if flags.printRequest {
		if noteErr := writeSkipped(cmd.ErrOrStderr(), scope.skipped, false, ""); noteErr != nil {
			return noteErr
		}

		return calibrateRequests(cmd, settings, flags, inputMode, inv, scope)
	}

	return calibrateRun(cmd, settings, flags, calib, inputMode, inv, scope, bound)
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

func refuseUnlabelledBody(settings rootSettings, name string, scored *plan.Plan) error {
	if name == "" {
		return nil
	}

	data, path, hides, err := readQuestionFile(settings, name)
	if err != nil {
		return err
	}

	loaded, err := loadQuestionFile(data, path, hides, false)
	if err != nil {
		return err
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
	cuts     string
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
