package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/plan"
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

func newCalibrateCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	recorder := argv.New()
	calib := &calibrateFlags{}

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
				err = withKeyHint(withDashQuestionHint(err, cmd.Flags(), settings.args, args), settings)
			}()

			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}

			if cmd.Flags().NFlag() == 0 && len(args) == 0 {
				return cmd.Help()
			}

			return runCalibrate(cmd, settings, flags, *calib, recorder.Events(), positional)
		},
	}

	for _, name := range []string{"ask", "pick", "rate", "box", "desc", "sep"} {
		cmd.Flags().Var(groupFlag(recorder, name), name, groupFlagHelp[name])
	}

	cmd.Flags().Lookup("box").NoOptDefVal = argv.Bare

	cmd.Flags().Var(nonEmptyList{values: &calib.labels}, "label",
		"[ID=]EXPR, a jq expression run on the record, not on the mapped state, whose result is the "+
			"right answer for question ID. A question with none is skipped. With one question ID= may be "+
			"left out")
	cmd.Flags().StringVar(&calib.cuts, flagCuts, "",
		"comma separated cuts between 0 and 1. Defaults to 0.05 to 0.95 in steps of 0.05")
	cmd.Flags().StringArrayVar(&calib.requires, "require", nil,
		"[lower|upper](ID.MEASURE) OP NUMBER [at CUT], a requirement the report must meet, or calibrate "+
			"exits 1. Repeatable")
	cmd.Flags().BoolVar(&calib.offline, "offline", false,
		"with --out and --resume, read every answer from the file and ask nothing, needing no key")
	cmd.Flags().StringVarP(&calib.report, "output", "o", "", "the report, table, json or auto, which means table")
	refuseEmpty(cmd.Flags(), "output")
	cmd.Flags().StringVarP(&calib.input, flagInput, "i", "", "required, jsonl, csv or tsv")
	refuseEmpty(cmd.Flags(), flagInput)

	addRunFlags(cmd.Flags(), flags, runFlagHelp{
		image:      "NAME=PATH, repeatable, puts a PNG or JPEG in every record's state under NAME. Experimental on TypeSafe's side",
		imageField: "repeatable, a state key whose value is a path to a PNG or JPEG, read per record. Experimental on TypeSafe's side",
		mapSource: "jq expression run on each record, whose result is the state sent. Map only the text a person " +
			"would read",
		idSource:  "jq expression run on each record, whose string or number result names it",
		out:       "write the answers to this file as -o json lines",
		resume:    "with --out and --id, skip the ids the file already answers",
		prune:     "with --resume, drop the answered ids the input no longer has",
		skipBlank: "drop blank lines",
		usage:     "add the api usage object to the answers file and the json report",
	})

	return cmd
}

func calibrateLong() string {
	return "calibrate asks the model about labelled records, compares each answer with the label a " +
		"--label jq expression takes from the record, and prints cut, agreement and confusion " +
		"tables to pick a gate from. It never picks the cut.\n\n" +
		"Map only the text a person would read, since a --map that selects the label flatters " +
		"the question.\n\n" +
		"Labels: a yes or no label is true, false, yes, no, 1 or 0 in any case. A pick or rate label " +
		"is an option or level name exactly as declared, or a number whose text is one, so 4 " +
		"matches --rate 1,2,3,4,5. null, no result or an empty string leaves a record unlabelled " +
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
	cmd *cobra.Command, settings rootSettings, parsed *runFlags, calib calibrateFlags,
	events []argv.Event, positional string,
) error {
	own := *parsed
	own.input = calib.input

	flags, err := newRunConfig(cmd, settings.lookupEnv, own, events, positional)
	if err != nil {
		return err
	}

	inputMode := flags.inputMode

	reqs, err := checkCalibrate(cmd, flags, calib)
	if err != nil {
		return err
	}

	// A file's gate and policy are ignored, since calibrate reports every cut and a gated file is
	// the one a user calibrates.
	inv, warnings, err := build(settings, flags, true)

	if warnErr := warnAll(cmd.ErrOrStderr(), warnings); warnErr != nil {
		return warnErr
	}

	if err != nil {
		return err
	}

	if warnErr := warnIgnoredBodyState(
		cmd.ErrOrStderr(), flags, inv.loaded, "calibrate", inv.images, wireAll(inv.plan.Questions)); warnErr != nil {
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

func checkCalibrate(cmd *cobra.Command, flags runConfig, calib calibrateFlags) ([]calibrate.Requirement, error) {
	if err := checkCalibrateInput(flags); err != nil {
		return nil, err
	}

	if err := checkOffline(flags, calib); err != nil {
		return nil, err
	}

	if !flags.hasMap {
		return nil, errors.New("calibrate needs --map. Without it the whole record, label included, " +
			"would be the state, and the report would flatter the question")
	}

	switch calib.report {
	case "", "auto", "table", "json":
	default:
		return nil, fmt.Errorf("calibrate -o takes table, json or auto, got %s", calib.report)
	}

	if flags.printRequest && cmd.Flags().Changed("output") {
		return nil, errors.New("-o does not apply to --print-request, which writes a request body")
	}

	if cmd.Flags().Changed(flagCuts) {
		if _, err := calibrate.ParseCuts(calib.cuts); err != nil {
			return nil, fmt.Errorf("--cuts %w", err)
		}
	}

	// A dry run is left to the flag rules, whose message drops --resume rather than asking for an
	// --id it would then refuse as well.
	if flags.resume && !flags.hasID && !flags.printRequest {
		return nil, errors.New(
			"calibrate resumes by id, since which records are asked follows the labels. Pass --id")
	}

	// A resume is left to the flag rules, whose message names --resume as the flag with nothing to
	// do.
	if flags.printRequest && flags.out != "" && !flags.resume {
		return nil, errors.New("--print-request writes request bodies to stdout, " +
			"so --out has no answers to hold. Drop --out")
	}

	// A dry run is left to the flag rules, whose message names --print-request as the reason.
	if flags.usage && !flags.printRequest && flags.out == "" && calib.report != "json" {
		return nil, errors.New("--usage adds token counts to the answers file or the json report, " +
			"and this run writes neither")
	}

	return parseRequirements(calib.requires)
}

func checkOffline(flags runConfig, calib calibrateFlags) error {
	switch {
	case !calib.offline:
		return nil
	case flags.mockSpelled != "":
		return fmt.Errorf("--offline reads every answer from --out, and %s answers from a file of "+
			"its own. Drop one", flags.mockSpelled)
	case flags.printRequest:
		return errors.New("--offline reads answers, and --print-request sends no request. Drop one")
	case flags.out == "":
		return errors.New("--offline reads every answer from --out. Pass --out FILE --resume")
	case !flags.resume:
		return errors.New("--offline needs --resume, since it reads the answers --out already holds")
	case flags.prune:
		return errors.New("--offline never rewrites --out, so --prune has nothing to drop. Drop one")
	}

	return nil
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

func checkCalibrateInput(flags runConfig) error {
	if !flags.hasInput {
		return errors.New("calibrate needs -i jsonl, csv or tsv, since only a record has fields to label")
	}

	switch flags.inputMode {
	case input.JSONL, input.CSV, input.TSV:
		return nil
	default:
		return fmt.Errorf("calibrate needs -i jsonl, csv or tsv. -i %s carries no field to label",
			inputName(flags.input))
	}
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

	_, err := fmt.Fprintln(w, line)

	return err
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
