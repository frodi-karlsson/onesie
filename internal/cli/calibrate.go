package cli

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/calibrate"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/jq"
	"github.com/frodi-karlsson/onesie/internal/limits"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

const (
	flagCuts    = "cuts"
	quietReason = "calibrate prints a report, and its exit code judges nothing"
	rawReason   = "calibrate prints its report as a table or as json"
	stateReason = "calibrate reads each state from its input through --map"
)

var refusedFlags = []struct {
	name, short, reason string
	boolean             bool
}{
	{name: "assert", reason: refusedReasons["assert"]},
	{name: "abstain-if", reason: refusedReasons["abstain-if"]},
	{name: "merge", reason: "calibrate prints a report, not the records", boolean: true},
	{name: "merge-key", reason: "calibrate prints a report, not the records"},
	// Each short spelling is a flag of its own, since pflag does not say which spelling set a flag,
	// and a refusal names the one the user typed.
	{name: "quiet", reason: quietReason, boolean: true},
	{name: "q", short: "q", reason: quietReason, boolean: true},
	{name: "raw", reason: rawReason, boolean: true},
	{name: "r", short: "r", reason: rawReason, boolean: true},
	{name: "stop-on-error", reason: "calibrate scores every record it can", boolean: true},
	{name: "stop-on-assert", reason: "calibrate judges no assertion", boolean: true},
	{name: "unordered", reason: "calibrate prints its report once every record is answered", boolean: true},
	{name: "print-questions", reason: "a question file carries no labels", boolean: true},
	{name: flagState, reason: stateReason},
	{name: flagStateFile, reason: stateReason},
}

var refusedReasons = map[string]string{
	"assert":         "calibrate reports every cut and judges none",
	"abstain-if":     "calibrate reports every cut and judges none",
	"threshold":      "calibrate reports every cut",
	"min-confidence": "calibrate reports every confidence cut",
	"fallback":       "calibrate scores the answers the model gave",
}

func newCalibrateCmd(settings rootSettings, flags *runFlags) *cobra.Command {
	recorder := argv.New()
	calib := &calibrateFlags{}

	cmd := &cobra.Command{
		Use:   "calibrate [question]",
		Short: "Score questions against records whose answers are known",
		Long: "calibrate asks the model about labelled records, compares each answer with the label a " +
			"--label jq expression takes from the record, and prints cut, agreement and confusion " +
			"tables to pick a gate from. It never picks the cut.\n\n" +
			"Map only the text a person would read, since a --map that selects the label flatters " +
			"the question.\n\n" +
			"Labels: a yes/no label is true, false, yes, no, 1 or 0 in any case. A pick or rate label " +
			"is an option or level name exactly as declared, or a number whose text is one, so 4 " +
			"matches --rate 1,2,3,4,5. null, no result or an empty string leaves a record unlabelled " +
			"for that question, and a record no question labels is not asked. A jsonl record reads " +
			"{\"id\":\"T-1\",\"body\":\"the site is down\",\"is_urgent\":true}, and a csv one " +
			"has the header id,body,is_urgent and the row T-1,the site is down,yes.\n\n" +
			"A question with no --label is skipped, left out of the requests and the report, and named " +
			"on stderr. At least one question needs a --label, a --label naming no question exits 2, and " +
			"so does a --require that reads a skipped question. A --resume into an --out file that " +
			"answers every question still asks the skipped ones, so the file stays whole.\n\n" +
			"A yes/no cut row flags a record when its value is at least the cut, so the cut goes into " +
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
			"[at CUT], where MEASURE is catches, false_alarms, right_when_flagged or auc for yes/no, " +
			"agreement for pick and rate, and within_one for rate. OP is >=, >, <= or <, and NUMBER is a " +
			"fraction between 0 and 1. lower and upper compare an end of the 95 percent interval instead " +
			"of the value, so 16 of 16 fails lower(x.catches) >= 0.9. A yes/no measure other than auc is " +
			"read at one cut: at CUT names it, at abstain takes it from the -f file's abstain_if, and " +
			"otherwise the file's assert gives it when it compares ID.value < X or ID.value >= X. The " +
			"report still prints, and each requirement that did not hold is named on stderr.\n\n" +
			"--offline, with --out and --resume, reads every answer from the file and asks nothing, so it " +
			"needs no key and never rewrites the file. A record the file does not answer, a missing file " +
			"or a file whose fingerprint differs exits 2.\n\n" +
			exitCodesSection(calibrateExitCodes),
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

			return runCalibrate(cmd, settings, flags, *calib, recorder.Events(), positional)
		},
	}

	for _, name := range []string{"ask", "pick", "rate", "desc", "sep"} {
		cmd.Flags().Var(recorder.Flag(name), name, groupFlagHelp[name])
	}

	for _, name := range []string{"threshold", "min-confidence", "fallback"} {
		cmd.Flags().Var(recorder.Flag(name), name, "refused, "+refusedReasons[name])
		hide(cmd, name)
	}

	for _, refused := range refusedFlags {
		if refused.boolean {
			cmd.Flags().BoolP(refused.name, refused.short, false, "refused, "+refused.reason)
		} else {
			cmd.Flags().StringArrayP(refused.name, refused.short, nil, "refused, "+refused.reason)
		}

		hide(cmd, refused.name)
	}

	cmd.Flags().StringArrayVar(&calib.labels, "label", nil,
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

	bindSharedFlags(cmd, flags)

	return cmd
}

func bindSharedFlags(cmd *cobra.Command, flags *runFlags) {
	// Every default is the root's, since pflag writes a default into its field when the flag is
	// registered, and this runs after the root registered the same field.
	cmd.Flags().StringVarP(&flags.input, flagInput, "i", "text", "required, jsonl, csv or tsv")
	// The root's text default stays in the field, and help leaves it out, since calibrate refuses it.
	cmd.Flags().Lookup(flagInput).DefValue = ""
	cmd.Flags().StringVar(&flags.mapSource, flagMap, "",
		"jq expression run on each record, whose result is the state sent. Map only the text a person "+
			"would read")
	cmd.Flags().StringVar(&flags.idSource, flagID, "",
		"jq expression run on each record, whose string or number result names it")
	cmd.Flags().StringVarP(&flags.model, flagModel, "m", "", "model override")
	cmd.Flags().StringVar(&flags.baseURL, flagBaseURL, "", "api root override")
	cmd.Flags().StringVarP(&flags.file, "file", "f", "",
		"question file or request body, or the name of one saved in .onesie/questions or the config dir, "+
			"or of a built-in starter set")
	cmd.Flags().BoolVar(&flags.replace, "replace", false, "--ask overrides an id from -f")
	cmd.Flags().IntVarP(&flags.jobs, flagJobs, "j", 1, "records in flight at once")
	cmd.Flags().IntVar(&flags.timeout, flagTimeout, int(limits.DefaultAttemptTimeout.Seconds()),
		"seconds per attempt")
	cmd.Flags().IntVar(&flags.retries, flagRetries, limits.DefaultRetries,
		"retries after a failed attempt")
	cmd.Flags().IntVar(&flags.maxRetryAfter, flagMaxRetryAfter,
		int(limits.DefaultMaxRetryAfter.Seconds()),
		"most seconds to wait out a server Retry-After")
	cmd.Flags().StringVar(&flags.out, "out", "", "write the answers to this file as -o json lines")
	cmd.Flags().BoolVar(&flags.resume, "resume", false,
		"with --out and --id, skip the ids the file already answers")
	cmd.Flags().BoolVar(&flags.prune, "prune", false,
		"with --resume, drop the answered ids the input no longer has")
	cmd.Flags().BoolVar(&flags.skipBlank, "skip-blank", false, "drop blank lines")
	cmd.Flags().BoolVar(&flags.stats, "stats", false,
		"write a one line summary of the run to stderr at exit")
	cmd.Flags().BoolVar(&flags.usage, "usage", false,
		"add the api usage object to the answers file and the json report")
	cmd.Flags().BoolVar(&flags.printRequest, "print-request", false,
		"write api shaped request bodies to stdout and exit")
	cmd.Flags().BoolVar(&flags.cache, flagCache, false, cacheHelp)
}

func hide(cmd *cobra.Command, name string) {
	cmd.Flags().Lookup(name).Hidden = true
}

func runCalibrate(
	cmd *cobra.Command, settings rootSettings, flags *runFlags, calib calibrateFlags,
	events []argv.Event, positional string,
) error {
	cfg, inputMode, err := configOf(cmd, events, positional, flags)
	if err != nil {
		return err
	}

	_, mockSpelled := mockSource(settings, flags)
	cfg.Mock = mockSpelled

	reqs, err := checkCalibrate(cmd, cfg, inputMode, calib, events)
	if err != nil {
		return err
	}

	// A file's gate and policy are ignored, since calibrate reports every cut and a gated file is
	// the one a user calibrates.
	inv, warnings, err := build(settings, cfg, events, positional, flags, true)

	for _, warning := range warnings {
		if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), warning); printErr != nil {
			return printErr
		}
	}

	if err != nil {
		return err
	}

	if warnErr := warnIgnoredBodyState(cmd.ErrOrStderr(), cfg, inv.loaded, "calibrate"); warnErr != nil {
		return warnErr
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

func checkCalibrate(
	cmd *cobra.Command, cfg plan.Config, inputMode input.Mode, calib calibrateFlags, events []argv.Event,
) ([]calibrate.Requirement, error) {
	if err := checkRefused(cmd, events); err != nil {
		return nil, err
	}

	if err := checkCalibrateInput(cfg, inputMode); err != nil {
		return nil, err
	}

	if err := checkOffline(cfg, calib); err != nil {
		return nil, err
	}

	if !cfg.HasMap {
		return nil, errors.New("onesie: calibrate needs --map. Without it the whole record, label included, " +
			"would be the state, and the report would flatter the question")
	}

	switch calib.report {
	case "", "auto", "table", "json":
	default:
		return nil, fmt.Errorf("onesie: calibrate -o takes table, json or auto, got %s", calib.report)
	}

	if cfg.PrintRequest && cmd.Flags().Changed("output") {
		return nil, errors.New("onesie: -o does not apply to --print-request, which writes a request body")
	}

	if cmd.Flags().Changed(flagCuts) {
		if _, err := calibrate.ParseCuts(calib.cuts); err != nil {
			return nil, fmt.Errorf("onesie: --cuts %w", err)
		}
	}

	// A dry run is left to plan, whose message drops --resume rather than asking for an --id it
	// would then refuse as well.
	if cfg.Resume && !cfg.HasID && !cfg.PrintRequest {
		return nil, errors.New(
			"onesie: calibrate resumes by id, since which records are asked follows the labels. Pass --id")
	}

	// A resume is left to plan, whose message names --resume as the flag with nothing to do.
	if cfg.PrintRequest && cfg.Out != "" && !cfg.Resume {
		return nil, errors.New("onesie: --print-request writes request bodies to stdout, " +
			"so --out has no answers to hold. Drop --out")
	}

	// A dry run is left to plan, whose message names --print-request as the reason.
	if cfg.Usage && !cfg.PrintRequest && cfg.Out == "" && calib.report != "json" {
		return nil, errors.New("onesie: --usage adds token counts to the answers file or the json report, " +
			"and this run writes neither")
	}

	return parseRequirements(calib.requires)
}

func checkOffline(cfg plan.Config, calib calibrateFlags) error {
	switch {
	case !calib.offline:
		return nil
	case cfg.Mock != "":
		return fmt.Errorf("onesie: --offline reads every answer from --out, and %s answers from a file of "+
			"its own. Drop one", cfg.Mock)
	case cfg.PrintRequest:
		return errors.New("onesie: --offline reads answers, and --print-request sends no request. Drop one")
	case cfg.Out == "":
		return errors.New("onesie: --offline reads every answer from --out. Pass --out FILE --resume")
	case !cfg.Resume:
		return errors.New("onesie: --offline needs --resume, since it reads the answers --out already holds")
	case cfg.Prune:
		return errors.New("onesie: --offline never rewrites --out, so --prune has nothing to drop. Drop one")
	}

	return nil
}

func checkRefused(cmd *cobra.Command, events []argv.Event) error {
	for _, refused := range refusedFlags {
		if !cmd.Flags().Changed(refused.name) {
			continue
		}

		spelled := "--" + refused.name
		if refused.short != "" {
			spelled = "-" + refused.short
		}

		return refusal(refused.reason, spelled)
	}

	for _, event := range events {
		if reason, found := refusedReasons[event.Name]; found {
			return refusal(reason, "--"+event.Name)
		}
	}

	return nil
}

func refusal(reason, spelled string) error {
	return fmt.Errorf("onesie: %s, so %s does not apply. Drop it", reason, spelled)
}

func checkCalibrateInput(cfg plan.Config, inputMode input.Mode) error {
	if !cfg.HasInput {
		return errors.New("onesie: calibrate needs -i jsonl, csv or tsv, since only a record has fields to label")
	}

	switch inputMode {
	case input.JSONL, input.CSV, input.TSV:
		return nil
	default:
		return fmt.Errorf("onesie: calibrate needs -i jsonl, csv or tsv. -i %s carries no field to label",
			cfg.InputName)
	}
}

func refuseUnlabelledBody(settings rootSettings, name string, scored *plan.Plan) error {
	if name == "" {
		return nil
	}

	data, _, err := readQuestionFile(settings, name)
	if err != nil {
		return err
	}

	loaded, err := qfile.Load(data)
	if err != nil {
		return err
	}

	for _, question := range loaded.Questions {
		labelled := slices.ContainsFunc(scored.Questions, func(q plan.Question) bool { return q.ID == question.ID })
		if labelled && question.Shape == plan.Rate && !question.Labelled {
			return fmt.Errorf("onesie: question '%s' from a request body has no level names to label with",
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
			return calibrateScope{}, fmt.Errorf("onesie: %w", err)
		}

		index := slices.Index(ids, id)
		if exprs[index] != nil {
			return calibrateScope{}, fmt.Errorf("onesie: --label is given twice for question '%s'", id)
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

		scored.Questions = append(scored.Questions, question)
		scope.labels = append(scope.labels, questionLabel{
			id: question.ID, shape: question.Shape, names: namesOf(question), expr: expr,
		})
	}

	if len(scope.labels) == 0 {
		return calibrateScope{}, fmt.Errorf("onesie: question '%s' has no --label, so asking it would cost "+
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
	offline  bool
}

type questionLabel struct {
	id    string
	shape plan.Shape
	names []string
	expr  *jq.Expr
}
