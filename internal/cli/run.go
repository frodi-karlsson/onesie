package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/jev-cli/internal/answer"
	"github.com/frodi-karlsson/jev-cli/internal/argv"
	"github.com/frodi-karlsson/jev-cli/internal/input"
	"github.com/frodi-karlsson/jev-cli/internal/jev"
	"github.com/frodi-karlsson/jev-cli/internal/output"
	"github.com/frodi-karlsson/jev-cli/internal/plan"
)

func run(
	cmd *cobra.Command,
	settings rootSettings,
	events []argv.Event,
	positional string,
	flags *runFlags,
) error {
	built, err := plan.Assemble(events, positional, settings.readFile)
	if err != nil {
		return err
	}

	built.Model = flags.model

	warnings, err := plan.Validate(built, plan.Config{
		Raw:          flags.raw,
		Quiet:        flags.quiet,
		Output:       flags.output,
		HasState:     cmd.Flags().Changed("state"),
		HasStateFile: cmd.Flags().Changed("state-file"),
	})

	// Warnings print whether or not validation succeeded, so a run that fails for one reason still
	// reports the others.
	for _, warning := range warnings {
		if _, printErr := fmt.Fprintln(cmd.ErrOrStderr(), warning); printErr != nil {
			return printErr
		}
	}

	if err != nil {
		return err
	}

	inputMode, err := input.ParseMode(flags.input)
	if err != nil {
		return err
	}

	// Both modes are parsed before the request, so a mistyped flag costs nothing.
	outputMode, err := output.ParseMode(outputName(flags), settings.stdoutTTY, false)
	if err != nil {
		return err
	}

	resolved, err := input.Resolve(input.Request{
		Mode:         inputMode,
		Stdin:        settings.stdin,
		StdinTTY:     settings.stdinTTY,
		State:        flags.state,
		HasState:     cmd.Flags().Changed("state"),
		StateFile:    flags.stateFile,
		HasStateFile: cmd.Flags().Changed("state-file"),
		ReadFile:     settings.readFile,
	})
	if err != nil {
		return err
	}

	if resolved.Source == input.SourceNone {
		return errors.New(
			"jev: no state given, pipe one to stdin, or pass --state or --state-file")
	}

	return ask(cmd, settings, built, resolved, outputMode, flags)
}

func ask(
	cmd *cobra.Command,
	settings rootSettings,
	built *plan.Plan,
	resolved input.Resolved,
	outputMode output.Mode,
	flags *runFlags,
) error {
	client, err := settings.newClient(cmd.Context())
	if err != nil {
		return err
	}

	questions := make(map[string]jev.Question, len(built.Questions))
	for _, question := range built.Questions {
		questions[question.ID] = wire(question)
	}

	result, err := client.SystemOne(cmd.Context(), jev.Request{
		State:     resolved.State,
		Model:     built.Model,
		Questions: questions,
	})
	if err != nil {
		// The exit code still comes from the error. This only adds the fallback word the caller
		// asked for, so a shell guard reads a decision rather than an empty string.
		if writeErr := writeFailure(cmd, settings, built, outputMode, flags, err); writeErr != nil {
			return writeErr
		}

		return err
	}

	record := output.Record{Model: result.Model}
	if flags.usage {
		record.Usage = &result.Usage
	}

	for _, question := range built.Questions {
		raw, ok := result.Answers[question.ID]
		if !ok {
			// Normalize would call Kind on a nil interface and panic. SystemOne already checks
			// this, so reaching here means a stub or a future code path skipped it.
			return fmt.Errorf("jev: response carries no answer for '%s'", question.ID)
		}

		normalized, normErr := answer.Normalize(question, raw)
		if normErr != nil {
			return normErr
		}

		answer.Apply(question, normalized)

		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: normalized})
	}

	if flags.quiet {
		return quietResult(built.Questions[0], record.Answers[0].Answer)
	}

	if outputMode == output.Table {
		return output.WriteTable(cmd.OutOrStdout(), record, output.Width(settings.lookupEnv))
	}

	return output.Write(cmd.OutOrStdout(), outputMode, record)
}

func writeFailure(
	cmd *cobra.Command,
	settings rootSettings,
	built *plan.Plan,
	mode output.Mode,
	flags *runFlags,
	cause error,
) error {
	// -q suppresses output entirely, so the exit code carries the whole result.
	if flags.quiet {
		return nil
	}

	record := output.Record{Failure: describe(cause)}

	for _, question := range built.Questions {
		record.Answers = append(record.Answers,
			output.Named{ID: question.ID, Answer: answer.Failed(question)})
	}

	if mode == output.Table {
		return output.WriteTable(cmd.OutOrStdout(), record, output.Width(settings.lookupEnv))
	}

	return output.Write(cmd.OutOrStdout(), mode, record)
}

func describe(cause error) *output.Failure {
	var api *jev.APIError
	if errors.As(cause, &api) {
		status := api.Status

		return &output.Failure{Kind: "http", Status: &status, Message: cause.Error()}
	}

	return &output.Failure{Kind: "transport", Message: cause.Error()}
}

func quietResult(question plan.Question, a *answer.Answer) error {
	if question.Shape == plan.Noul {
		// The threshold defaults to a half, so a yes/no question is meaningful under -q with no
		// policy at all, unlike a pick question which has nothing to compare.
		threshold := 0.5
		if question.Policy.Threshold != nil {
			threshold = *question.Policy.Threshold
		}

		probability, ok := a.Value.(float64)
		if ok && probability >= threshold {
			return nil
		}

		return &rejectedError{}
	}

	// A low confidence fallback exits zero in every other output mode. Under -q it is the one
	// signal the exit code has, so it reports rejection instead.
	if a.Fallback == "low_confidence" {
		return &rejectedError{}
	}

	return nil
}

func wire(q plan.Question) jev.Question {
	switch q.Shape {
	case plan.Pick:
		criteria := make(map[string]any, len(q.Options))
		for _, option := range q.Options {
			criteria[option.Name] = option.Desc
		}

		return jev.Choice{Instructions: q.Instructions, Criteria: criteria}
	case plan.Rate:
		criteria := make([]any, 0, len(q.Levels))

		for _, level := range q.Levels {
			if level.Desc != nil {
				criteria = append(criteria, level.Desc)

				continue
			}

			// An undescribed rubric sends the labels themselves, which is the documented
			// behaviour for a bare --rate.
			criteria = append(criteria, level.Label)
		}

		return jev.Score{Instructions: q.Instructions, Criteria: criteria}
	default:
		if q.Criteria == nil {
			return jev.Noul{Instructions: q.Instructions}
		}

		return jev.Noul{
			Instructions: q.Instructions,
			Criteria:     &jev.NoulCriteria{True: q.Criteria.Yes, False: q.Criteria.No},
		}
	}
}

func outputName(flags *runFlags) string {
	if flags.raw {
		return "raw"
	}

	return flags.output
}

type runFlags struct {
	output    string
	raw       bool
	quiet     bool
	usage     bool
	input     string
	state     string
	stateFile string
	model     string
	apiKey    string
	baseURL   string
}

type rejectedError struct{}

func (*rejectedError) Error() string {
	return "policy did not accept the answer"
}

// clientFactory builds the API client. It is injected so tests supply one pointed at a stub server
// rather than steering the real constructor through flags.
type clientFactory func(ctx context.Context) (*jev.Client, error)

func defaultClientFactory(info BuildInfo, flags *runFlags) clientFactory {
	return func(context.Context) (*jev.Client, error) {
		opts := []jev.Option{jev.WithUserAgent("jev-cli/" + info.Version)}

		if flags.apiKey != "" {
			opts = append(opts, jev.WithAPIKey(flags.apiKey))
		}

		if flags.baseURL != "" {
			opts = append(opts, jev.WithBaseURL(flags.baseURL))
		}

		return jev.New(opts...)
	}
}

func worthReporting(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}

	var rejected *rejectedError

	// -q suppresses output entirely, so its rejection is carried by the exit code alone.
	return !errors.As(err, &rejected)
}
