package cli

import (
	"fmt"
	"time"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/input"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func newRunConfig(flags runFlags, events []argv.Event, positional string) (runConfig, error) {
	inputMode, err := input.ParseMode(flags.input)
	if err != nil {
		return runConfig{}, err
	}

	var delimiter input.Delimiter
	if flags.typed(setting.Delimiter.Name()) {
		delimiter, err = input.ParseDelimiter(flags.delimiter)
		if err != nil {
			return runConfig{}, fmt.Errorf("--%s %w", flagDelimiter, err)
		}
	}

	return runConfig{
		runFlags:        flags,
		inputMode:       inputMode,
		events:          events,
		positional:      positional,
		parsedDelimiter: delimiter,
	}, nil
}

type runConfig struct {
	runFlags

	inputMode   input.Mode
	events      []argv.Event
	positional  string
	merged      *setting.Merged
	env         switches
	mockPath    string
	mockSpelled string

	parsedDelimiter input.Delimiter

	calibrate *calibrateFlags
	stdoutTTY bool
}

func (c runConfig) withMerged(merged *setting.Merged) runConfig {
	c.merged = merged
	c.env = switches{
		cache:       setting.Get(merged, setting.Cache),
		cacheFrom:   merged.Source(setting.Cache.Name()),
		longContext: setting.Get(merged, setting.LongContext),
		aliasTTL:    setting.Derived(setting.NewView(merged, nil, setting.Facts{}), setting.CacheTTL),
	}
	c.mockPath = setting.Get(merged, setting.Mock)

	switch merged.Source(setting.Mock.Name()) {
	case setting.SourceFlag:
		c.mockSpelled = flagMock
	case setting.SourceEnv:
		c.mockSpelled = envMock
	default:
		c.mockSpelled = ""
	}

	return c
}

type switches struct {
	cache       bool
	cacheFrom   setting.Source
	longContext bool
	aliasTTL    time.Duration
}

type runFacts struct {
	name      string
	assert    string
	abstainIf string
	bodyState bool
}
