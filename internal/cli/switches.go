package cli

import (
	"fmt"
	"strconv"
	"time"
)

const (
	envCache       = "ONESIE_CACHE"
	envCacheTTL    = "ONESIE_CACHE_TTL"
	envLongContext = "ONESIE_LONG_CONTEXT"
)

func checkEnvSwitches(lookupEnv func(string) (string, bool)) error {
	for _, name := range []struct{ variable, flag string }{
		{envCache, flagCache}, {envLongContext, flagLongContext},
	} {
		if err := checkEnvSwitch(lookupEnv, name.variable, name.flag); err != nil {
			return err
		}
	}

	return checkAliasTTL(lookupEnv)
}

func checkEnvSwitch(lookupEnv func(string) (string, bool), name, flag string) error {
	value := setVariable(lookupEnv, name)
	if value == "" {
		return nil
	}

	if _, err := strconv.ParseBool(value); err != nil {
		return fmt.Errorf("%s=%s is not a value --%s takes. Use 1 or 0", name, value, flag)
	}

	return nil
}

func checkAliasTTL(lookupEnv func(string) (string, bool)) error {
	value := setVariable(lookupEnv, envCacheTTL)
	if value == "" {
		return nil
	}

	if ttl, err := time.ParseDuration(value); err != nil || ttl < 0 {
		return fmt.Errorf("%s=%s is not a duration of zero or more, such as 90m or 72h", envCacheTTL, value)
	}

	return nil
}
