package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/frodi-karlsson/onesie/internal/cache"
)

const (
	envCache       = "ONESIE_CACHE"
	envCacheTTL    = "ONESIE_CACHE_TTL"
	envLongContext = "ONESIE_LONG_CONTEXT"
)

type switches struct {
	cache       bool
	longContext bool
	aliasTTL    time.Duration
}

func readSwitches(cmd *cobra.Command, lookupEnv func(string) (string, bool), flags runFlags) (switches, error) {
	read, err := envSwitches(lookupEnv)
	if err != nil {
		return switches{}, err
	}

	if cmd.Flags().Changed(flagCache) {
		read.cache = flags.cache
	}

	if cmd.Flags().Changed(flagLongContext) {
		read.longContext = flags.longContext
	}

	return read, nil
}

func envSwitches(lookupEnv func(string) (string, bool)) (switches, error) {
	cached, err := envSwitch(lookupEnv, envCache, flagCache)
	if err != nil {
		return switches{}, err
	}

	long, err := envSwitch(lookupEnv, envLongContext, flagLongContext)
	if err != nil {
		return switches{}, err
	}

	ttl, err := aliasTTL(lookupEnv)
	if err != nil {
		return switches{}, err
	}

	return switches{cache: cached, longContext: long, aliasTTL: ttl}, nil
}

func envSwitch(lookupEnv func(string) (string, bool), name, flag string) (bool, error) {
	value, found := lookupEnv(name)
	if !found || value == "" {
		return false, nil
	}

	on, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s=%s is not a value --%s takes. Use 1 or 0", name, value, flag)
	}

	return on, nil
}

func aliasTTL(lookupEnv func(string) (string, bool)) (time.Duration, error) {
	value, found := lookupEnv(envCacheTTL)
	if !found || value == "" {
		return 0, nil
	}

	ttl, err := time.ParseDuration(value)
	if err != nil || ttl < 0 {
		return 0, fmt.Errorf("%s=%s is not a duration of zero or more, such as 90m or 72h", envCacheTTL, value)
	}

	if ttl == 0 {
		return cache.NoAliasTTL, nil
	}

	return ttl, nil
}

func longContext(flags runConfig) bool {
	return flags.env.longContext
}
