package cli

import (
	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/input"
)

func newRunConfig(
	cmd *cobra.Command,
	lookupEnv func(string) (string, bool),
	flags runFlags,
	events []argv.Event,
	positional string,
) (runConfig, error) {
	env, err := readSwitches(cmd, lookupEnv, flags)
	if err != nil {
		return runConfig{}, err
	}

	inputMode, err := input.ParseMode(flags.input)
	if err != nil {
		return runConfig{}, err
	}

	mockPath, mockSpelled := mockSource(lookupEnv, flags.mock)
	changed := cmd.Flags().Changed

	return runConfig{
		runFlags:         flags,
		inputMode:        inputMode,
		events:           events,
		positional:       positional,
		env:              env,
		mockPath:         mockPath,
		mockSpelled:      mockSpelled,
		hasState:         changed(flagState),
		hasStateFile:     changed(flagStateFile),
		hasMap:           changed(flagMap),
		hasID:            changed(flagID),
		hasModel:         changed(flagModel),
		hasInput:         changed(flagInput),
		jobsSet:          changed(flagJobs),
		timeoutSet:       changed(flagTimeout),
		retriesSet:       changed(flagRetries),
		maxRetryAfterSet: changed(flagMaxRetryAfter),
		longContextSet:   changed(flagLongContext),
	}, nil
}

type runConfig struct {
	runFlags

	inputMode   input.Mode
	events      []argv.Event
	positional  string
	env         switches
	mockPath    string
	mockSpelled string

	hasState         bool
	hasStateFile     bool
	hasMap           bool
	hasID            bool
	hasModel         bool
	hasInput         bool
	jobsSet          bool
	timeoutSet       bool
	retriesSet       bool
	maxRetryAfterSet bool
	longContextSet   bool
}

type runFacts struct {
	assert    string
	abstainIf string
}
