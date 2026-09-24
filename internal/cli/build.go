package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/assert"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/qfile"
)

func configOf(
	cmd *cobra.Command, events []argv.Event, positional string, flags *runFlags,
) (plan.Config, input.Mode, error) {
	inputMode, err := input.ParseMode(flags.input)
	if err != nil {
		return plan.Config{}, 0, err
	}

	cfg := plan.Config{
		Raw:            flags.raw,
		Quiet:          flags.quiet,
		HasAssert:      len(flags.assert) > 0,
		HasAbstainIf:   len(flags.abstainIf) > 0,
		Output:         flags.output,
		HasState:       cmd.Flags().Changed(flagState),
		HasStateFile:   cmd.Flags().Changed(flagStateFile),
		Replace:        flags.replace,
		FileName:       flags.file,
		HasAsk:         asked(events),
		GroupFlags:     eventNames(events),
		HasPositional:  positional != "",
		HasModel:       cmd.Flags().Changed(flagModel),
		Usage:          flags.usage,
		PrintQuestions: flags.printQuestions,
		PrintRequest:   flags.printRequest,
		Stats:          flags.stats,
		Streaming:      inputMode.Streaming(),
		RequestMode:    inputMode == input.Request,
		ListModels:     flags.listModels,
		InputName:      inputName(flags.input),
		HasInput:       cmd.Flags().Changed(flagInput),
		Unordered:      flags.unordered,
		StopOnError:    flags.stopOnError,
		Out:            flags.out,
		Resume:         flags.resume,
		StopOnAssert:   flags.stopOnAssert,
		SkipBlank:      flags.skipBlank,
		Merge:          merging(flags),
		MergeName:      mergeName(flags),
		Jobs:           flags.jobs,
		JobsSet:        cmd.Flags().Changed(flagJobs),

		Timeout:          flags.timeout,
		TimeoutSet:       cmd.Flags().Changed(flagTimeout),
		Retries:          flags.retries,
		RetriesSet:       cmd.Flags().Changed(flagRetries),
		MaxRetryAfter:    flags.maxRetryAfter,
		MaxRetryAfterSet: cmd.Flags().Changed(flagMaxRetryAfter),
	}

	return cfg, inputMode, nil
}

func build(
	settings rootSettings, cfg plan.Config, events []argv.Event, positional string,
	flags *runFlags,
) (*invocation, []string, error) {
	// Every mode except --list-models and -i request needs a question, and reporting that here
	// rather than from Assemble keeps the message the same whichever source was missing.
	if positional == "" && len(events) == 0 && flags.file == "" {
		return nil, nil, errors.New("onesie: no question given. Pass a question, --ask, or -f")
	}

	var loaded *qfile.File

	if flags.file != "" {
		data, readErr := settings.readFile(flags.file)
		if readErr != nil {
			return nil, nil, fmt.Errorf("onesie: reading %s: %w", flags.file, readErr)
		}

		var loadErr error

		loaded, loadErr = qfile.Load(data)
		if loadErr != nil {
			return nil, nil, loadErr
		}

		// The file's assertion is the same gate --assert is, §17.5, so it answers the -q rule the
		// same way. The config is built before the file is read, which is why this is not up there
		// with the flags.
		if loaded.Assert != "" && !cfg.HasAssert {
			cfg.AssertName = plan.Spelling(plan.OriginFile, "--assert")
		}

		cfg.HasAssert = cfg.HasAssert || loaded.Assert != ""

		if loaded.AbstainIf != "" && !cfg.HasAbstainIf {
			cfg.AbstainIfName = plan.Spelling(plan.OriginFile, "--abstain-if")
		}

		cfg.HasAbstainIf = cfg.HasAbstainIf || loaded.AbstainIf != ""
	}

	source := plan.Source{
		Events:     events,
		Positional: positional,
		FileName:   flags.file,
		Replace:    flags.replace,
		ReadFile:   settings.readFile,
	}

	if loaded != nil {
		source.File = loaded.Questions
	}

	built, err := plan.Assemble(source)
	if err != nil {
		return nil, nil, err
	}

	built.Model = flags.model
	if built.Model == "" && loaded != nil {
		built.Model = loaded.Model
	}

	warnings, err := plan.Validate(built, cfg)
	if err != nil {
		return nil, warnings, err
	}

	// After validation, since the checker reads a plan the run has accepted, and before any mode
	// dispatches, since §17.3 checks an assertion against the plan and §11 runs every check before
	// any network call.
	var fileAssert, fileAbstain string
	if loaded != nil {
		fileAssert, fileAbstain = loaded.Assert, loaded.AbstainIf
	}

	gate, err := gateOf("--assert", fileAssert, flags.assert, built)
	if err != nil {
		return nil, warnings, err
	}

	abstain, err := gateOf("--abstain-if", fileAbstain, flags.abstainIf, built)
	if err != nil {
		return nil, warnings, err
	}

	return &invocation{plan: built, gate: gate, abstain: abstain, loaded: loaded}, warnings, nil
}

func gateOf(flag, fileSource string, sources []string, built *plan.Plan) (*assert.Expr, error) {
	exprs := make([]*assert.Expr, 0, len(sources)+1)

	// The file leads and the command line follows. The file's expression is the more general one,
	// and argv puts the flag after the -f that named the file. §17.5 combines the two by and, and
	// and is commutative, so the order is a matter of which one an error names first.
	if fileSource != "" {
		// Spelled the way the file spells it, which is the rule plan holds every other message to
		// that names where a setting came from.
		expr, err := gateExpr(fileSource, plan.Spelling(plan.OriginFile, flag), built)
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, expr)
	}

	for _, source := range sources {
		expr, err := gateExpr(source, flag, built)
		if err != nil {
			return nil, err
		}

		exprs = append(exprs, expr)
	}

	return assert.Combine(exprs...), nil
}

func gateExpr(source, named string, built *plan.Plan) (*assert.Expr, error) {
	expr, err := assert.Parse(source)
	if err != nil {
		return nil, fmt.Errorf("onesie: %s: %w", named, err)
	}

	// Each source is checked on its own rather than the combined gate, so a message names the one
	// that carried the mistake. Every source is a boolean in its own right, so a combination adds
	// nothing for the checker to reject.
	if checkErr := assert.Check(expr, built); checkErr != nil {
		return nil, fmt.Errorf("onesie: %s: %w", named, checkErr)
	}

	return expr, nil
}

type invocation struct {
	plan    *plan.Plan
	gate    *assert.Expr
	abstain *assert.Expr
	loaded  *qfile.File
}
