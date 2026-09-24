package cli

import (
	"errors"
	"fmt"
	"slices"

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
		Long: "calibrate asks Jev about labelled records, compares each answer with the label a " +
			"--label jq expression takes from the record, and prints cut, agreement and confusion " +
			"tables to pick a gate from. It never picks the cut.\n\n" +
			"Map only the text a person would read, since a --map that selects the label flatters " +
			"the question.",
		Args:          cobra.MaximumNArgs(1),
		Hidden:        true,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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
		"[ID=]EXPR, repeatable, a jq expression run on each record whose result is the right answer "+
			"for question ID")
	cmd.Flags().StringVar(&calib.cuts, flagCuts, "",
		"comma separated cuts between 0 and 1. Defaults to 0.05 to 0.95 in steps of 0.05")
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
	cmd.Flags().StringVar(&flags.apiKey, "api-key", "",
		"api key. Prefer TYPESAFE_API_KEY, OPENROUTER_API_KEY or onesie auth set, since argv is visible in ps")
	cmd.Flags().StringVar(&flags.baseURL, flagBaseURL, "", "api root override")
	cmd.Flags().StringVarP(&flags.file, "file", "f", "", "question file or request body")
	cmd.Flags().IntVarP(&flags.jobs, flagJobs, "j", 1, "records in flight at once")
	cmd.Flags().IntVar(&flags.timeout, flagTimeout, int(limits.DefaultAttemptTimeout.Seconds()),
		"seconds per attempt")
	cmd.Flags().IntVar(&flags.retries, flagRetries, limits.DefaultRetries,
		"retries after a failed attempt")
	cmd.Flags().IntVar(&flags.maxRetryAfter, flagMaxRetryAfter,
		int(limits.DefaultMaxRetryAfter.Seconds()),
		"honour a server Retry-After up to this many seconds")
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

	if checkErr := checkCalibrate(cmd, cfg, inputMode, calib, events); checkErr != nil {
		return checkErr
	}

	// Ahead of build, whose plan rules would otherwise answer a file's gate or policy key with a
	// message about another key it needs, when calibrate refuses the key itself.
	if refuseErr := refuseFile(settings, flags.file); refuseErr != nil {
		return refuseErr
	}

	inv, warnings, err := build(settings, cfg, events, positional, flags)

	for _, warning := range warnings {
		if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), warning); printErr != nil {
			return printErr
		}
	}

	if err != nil {
		return err
	}

	if _, labelErr := labelsOf(inv.plan, calib.labels); labelErr != nil {
		return labelErr
	}

	return errors.New("onesie: calibrate cannot ask yet")
}

func checkCalibrate(
	cmd *cobra.Command, cfg plan.Config, inputMode input.Mode, calib calibrateFlags, events []argv.Event,
) error {
	if err := checkRefused(cmd, events); err != nil {
		return err
	}

	if err := checkCalibrateInput(cfg, inputMode); err != nil {
		return err
	}

	if !cfg.HasMap {
		return errors.New("onesie: calibrate needs --map. Without it the whole record, label included, " +
			"would be the state, and the report would flatter the question")
	}

	switch calib.report {
	case "", "auto", "table", "json":
	default:
		return fmt.Errorf("onesie: calibrate -o takes table, json or auto, got %s", calib.report)
	}

	if cmd.Flags().Changed(flagCuts) {
		if _, err := calibrate.ParseCuts(calib.cuts); err != nil {
			return fmt.Errorf("onesie: --cuts %w", err)
		}
	}

	if cfg.Resume && !cfg.HasID {
		return errors.New(
			"onesie: calibrate resumes by id, since which records are asked follows the labels. Pass --id")
	}

	if cfg.PrintRequest && cfg.Out != "" {
		return errors.New("onesie: --print-request writes request bodies to stdout, " +
			"so --out has no answers to hold. Drop --out")
	}

	// A dry run is left to plan, whose message names --print-request as the reason.
	if cfg.Usage && !cfg.PrintRequest && cfg.Out == "" && calib.report != "json" {
		return errors.New("onesie: --usage adds token counts to the answers file or the json report, " +
			"and this run writes neither")
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

func refuseFile(settings rootSettings, name string) error {
	if name == "" {
		return nil
	}

	data, err := settings.readFile(name)
	if err != nil {
		return fmt.Errorf("onesie: reading %s: %w", name, err)
	}

	loaded, err := qfile.Load(data)
	if err != nil {
		return err
	}

	if loaded.Assert != "" {
		return refusal(refusedReasons["assert"], plan.Spelling(plan.OriginFile, "--assert"))
	}

	if loaded.AbstainIf != "" {
		return refusal(refusedReasons["abstain-if"], plan.Spelling(plan.OriginFile, "--abstain-if"))
	}

	for _, question := range loaded.Questions {
		for _, set := range []struct {
			given bool
			name  string
		}{
			{question.Policy.Threshold != nil, "threshold"},
			{question.Policy.MinConfidence != nil, "min-confidence"},
			{question.Policy.Fallback != nil, "fallback"},
		} {
			if set.given {
				return refusal(refusedReasons[set.name], plan.Spelling(question.Origin, "--"+set.name))
			}
		}

		if question.Shape == plan.Rate && !question.Labelled {
			return fmt.Errorf("onesie: question '%s' from a request body has no level names to label with",
				question.ID)
		}
	}

	return nil
}

func labelsOf(built *plan.Plan, specs []string) ([]questionLabel, error) {
	ids := make([]string, 0, len(built.Questions))
	for _, question := range built.Questions {
		ids = append(ids, question.ID)
	}

	exprs := make([]*jq.Expr, len(ids))

	for _, spec := range specs {
		id, source, err := calibrate.SplitLabel(spec, ids)
		if err != nil {
			return nil, fmt.Errorf("onesie: %w", err)
		}

		index := slices.Index(ids, id)
		if exprs[index] != nil {
			return nil, fmt.Errorf("onesie: --label is given twice for question '%s'", id)
		}

		exprs[index], err = exprOf(true, "--label "+id, source)
		if err != nil {
			return nil, err
		}
	}

	labels := make([]questionLabel, 0, len(ids))

	for index, expr := range exprs {
		if expr == nil {
			return nil, fmt.Errorf("onesie: question '%s' has no --label, so asking it would cost "+
				"requests and report nothing. Pass --label %s=EXPR", ids[index], ids[index])
		}

		labels = append(labels, questionLabel{question: index, expr: expr})
	}

	return labels, nil
}

type calibrateFlags struct {
	labels []string
	cuts   string
	report string
}

type questionLabel struct {
	question int
	expr     *jq.Expr
}
