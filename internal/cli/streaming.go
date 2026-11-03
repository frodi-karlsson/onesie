package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/answersfile"
	"github.com/frodi-karlsson/onesie/internal/output"
	"github.com/frodi-karlsson/onesie/internal/plan"
	"github.com/frodi-karlsson/onesie/internal/runner"
)

func stream(cmd *cobra.Command, in streamInputs) error {
	if in.flags.printRequest {
		return streamRequests(cmd, in.settings, in.flags, in.inv, in.resume)
	}

	model, err := resolveModel(in.settings, in.inv.Plan.Model)
	if err != nil {
		return err
	}

	outcome, err := runner.Stream(cmd.Context(), streamConfig(cmd, in, model))
	if err != nil {
		return err
	}

	// The records' own outcome wins, since a note that failed to print is the smaller loss.
	if result := streamResult(outcome.Result, outcome.FalseAsserts, outcome.Abstains); result != nil {
		return result
	}

	return outcome.NoticeErr
}

type streamInputs struct {
	settings   rootSettings
	flags      runConfig
	inv        *invocation
	outputMode output.Mode
	stats      *runner.Collector
	answers    *answersfile.File
	resume     answersfile.Resume
	answerers  runner.AnswererFactory
}

func streamConfig(cmd *cobra.Command, in streamInputs, model string) runner.StreamConfig {
	inv, flags := in.inv, in.flags

	return runner.StreamConfig{
		Input:      in.settings.stdin,
		InputMode:  flags.inputMode,
		SkipBlank:  flags.skipBlank,
		Delimiter:  flags.parsedDelimiter,
		Out:        cmd.OutOrStdout(),
		ErrOut:     cmd.ErrOrStderr(),
		OutputMode: in.outputMode,
		Ask: runner.Ask{
			Plan: inv.Plan, Model: model, Questions: plan.WireAll(inv.Plan.Questions), Usage: flags.usage,
			Message: text,
		},
		Gate:         inv.Gate,
		Abstain:      inv.Abstain,
		Mapper:       inv.Mapper,
		Namer:        inv.Namer,
		Images:       inv.images,
		Answerers:    in.answerers,
		Stats:        in.stats,
		Answers:      in.answers,
		Resume:       in.resume,
		Jobs:         flags.jobs,
		Unordered:    flags.unordered,
		StopOnError:  flags.stopOnError,
		StopOnAssert: flags.stopOnAssert,
		NoDedup:      flags.noDedup,
		DedupLimit:   in.settings.dedupLimit,
		Merge:        merging(flags),
		MergeKey:     mergeKey(flags),
		Prune:        flags.prune,
		Speller:      plan.Flags,
	}
}

func withStats(
	cmd *cobra.Command,
	now func() time.Time,
	flags runConfig,
	body func(*runner.Collector) error,
) error {
	stats := &runner.Collector{}
	started := now()

	err := body(stats)

	if !flags.stats {
		return err
	}

	summary := stats.Snapshot(now().Sub(started))

	// A run that failed before it read a record or made an attempt never started, and a line of
	// zeros beside the error reads as a run that was made and came back empty. An empty stream is
	// the other case: it succeeded, and 0 records is the measurement. A resume that skipped every
	// record read them all, so its gate's exit code gets the line that explains it.
	if err != nil && summary.Records == 0 && summary.Attempts == 0 && summary.Skipped == 0 {
		return err
	}

	// Stderr, because stdout carries the answers.
	//
	// The run's own error wins, since a summary that failed to print is the smaller loss.
	if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), summary); printErr != nil && err == nil {
		return printErr
	}

	return err
}
