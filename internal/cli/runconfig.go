package cli

import (
	"fmt"
	"time"

	"github.com/frodi-karlsson/onesie/internal/argv"
	"github.com/frodi-karlsson/onesie/internal/input"
	"github.com/frodi-karlsson/onesie/internal/settingrow"
	setting "github.com/frodi-karlsson/onesie/internal/settings"
)

func newRunConfig(parsed *setting.Merged, given *setting.Given, events []argv.Event, positional string) (runConfig, error) {
	inputMode, err := input.ParseMode(setting.Get(parsed, setting.Input))
	if err != nil {
		return runConfig{}, err
	}

	var delimiter input.Delimiter
	if given.Count(setting.Delimiter.Name()) > 0 {
		delimiter, err = input.ParseDelimiter(setting.Get(parsed, setting.Delimiter))
		if err != nil {
			return runConfig{}, fmt.Errorf("--%s %w", flagDelimiter, err)
		}
	}

	return runConfig{
		merged:          parsed,
		given:           given,
		inputMode:       inputMode,
		events:          events,
		positional:      positional,
		parsedDelimiter: delimiter,
	}, nil
}

type runConfig struct {
	given *setting.Given

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
	c.mockSpelled = merged.Origin(setting.Mock.Name()).Spelled

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
	bodyState bool
}

func (c runConfig) typed(name settingrow.Name) bool {
	return c.given.Count(name) > 0
}

func (c runConfig) list(name settingrow.Name) []string {
	if c.merged == nil {
		return nil
	}
	if values, ok := c.merged.Value(name).([]string); ok {
		return values
	}
	return nil
}
